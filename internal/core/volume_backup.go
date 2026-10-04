package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/storage"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// Volume backups archive a service's volumes (gzipped tar, one top-level
// directory per volume) through a short-lived, network-less helper container
// that mounts them, so neither the service image nor the manager needs tar.
const (
	volumeHelperComponent = "volume-helper"
	volumeStageComponent  = "volume-stage"
	volumeHelperImage     = probeHelperImage
	// dataVolumeName names a database's data volume inside an archive.
	dataVolumeName   = "data"
	helperVolumeRoot = "/v"
	helperStageRoot  = "/stage"
	preBackupTimeout = 10 * time.Minute
)

// volumeNames lists what a volume backup of svc archives: a database's data
// volume (PostgreSQL excepted: pg_dump), or an app's volumes.
func volumeNames(svc store.Service) []string {
	switch {
	case svc.Kind == store.ServiceKindPostgres:
		return nil
	case svc.Kind.IsDatabase():
		return []string{dataVolumeName}
	}
	names := make([]string, len(svc.Volumes))
	for i, v := range svc.Volumes {
		names[i] = v.Name
	}
	return names
}

// backupKind is how a service is backed up.
func backupKind(svc store.Service) (store.BackupKind, error) {
	switch {
	case svc.Kind == store.ServiceKindPostgres:
		return store.BackupKindPostgres, nil
	case len(volumeNames(svc)) > 0:
		return store.BackupKindVolume, nil
	}
	return "", fmt.Errorf("%w: %s has no volumes to back up", ErrInvalid, svc.Name)
}

// helperMounts mounts the named volumes of svc under root/<name>.
func helperMounts(svc store.Service, names []string, root string, readOnly bool) []mount.Mount {
	mounts := make([]mount.Mount, 0, len(names))
	for _, name := range names {
		var m mount.Mount
		if svc.Kind.IsDatabase() {
			m = mount.Mount{Type: mount.TypeVolume, Source: DataVolume(svc)}
		} else {
			m = appVolumeMount(svc, store.Volume{Name: name})
		}
		m.Target, m.ReadOnly = root+"/"+name, readOnly
		mounts = append(mounts, m)
	}
	return mounts
}

// startVolumeHelper runs an idle helper with mounts; commands run in it
// over exec. The caller removes it.
func startVolumeHelper(ctx context.Context, dk *docker.Client, name string, mounts []mount.Mount) (string, error) {
	// A fixed public image: no registry credential.
	if err := dk.EnsureImage(ctx, volumeHelperImage, ""); err != nil {
		return "", fmt.Errorf("pull %s: %w", volumeHelperImage, err)
	}
	return dk.Run(ctx, client.ContainerCreateOptions{
		Name: name,
		Config: &container.Config{
			Image:  volumeHelperImage,
			Cmd:    []string{"sleep", "2147483647"},
			Labels: map[string]string{docker.LabelManaged: "true", docker.LabelComponent: volumeHelperComponent},
		},
		HostConfig: &container.HostConfig{
			NetworkMode: "none",
			Mounts:      mounts,
			LogConfig:   docker.DefaultLogConfig(),
		},
	})
}

// dumpVolumes streams an archive of the backup's volumes to storage. The
// service keeps running: files are copied as they are (run a pre-backup
// command to flush them first).
func (c *Core) dumpVolumes(ctx context.Context, svc store.Service, st storage.Storage, target store.BackupTarget, b *store.Backup) error {
	if svc.PreBackup != "" {
		if err := c.preBackup(ctx, svc); err != nil {
			return err
		}
	}
	dk := c.dockerFor(svc.ServerID)
	id, err := startVolumeHelper(ctx, dk, "kipitiny-backup-"+strings.ToLower(b.ID), helperMounts(svc, b.Volumes, helperVolumeRoot, true))
	if err != nil {
		return err
	}
	defer c.removeHelper(ctx, dk, id)
	return c.upload(ctx, st, target, b, func(w io.Writer) error {
		err := dk.Exec(ctx, id, docker.ExecOptions{
			Cmd:    append([]string{"tar", "-czf", "-", "--numeric-owner", "-C", helperVolumeRoot}, b.Volumes...),
			Stdout: w,
		})
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		return nil
	})
}

// preBackup runs the service's pre-backup command in one running replica.
// With none running, the files are at rest already.
func (c *Core) preBackup(ctx context.Context, svc store.Service) error {
	ctx, cancel := context.WithTimeout(ctx, preBackupTimeout)
	defer cancel()
	cts, err := c.activeContainers(ctx, svc)
	if err != nil {
		return err
	}
	for _, ct := range cts {
		if ct.State != container.StateRunning {
			continue
		}
		if err := c.dockerFor(svc.ServerID).Exec(ctx, ct.ID, docker.ExecOptions{Cmd: []string{"sh", "-c", svc.PreBackup}}); err != nil {
			return fmt.Errorf("pre-backup command: %w", err)
		}
		return nil
	}
	return nil
}

func (c *Core) removeHelper(ctx context.Context, dk *docker.Client, id string) {
	if err := dk.RemoveContainerAndVolumes(context.WithoutCancel(ctx), id); err != nil {
		c.log.Warn("cannot remove volume helper", "id", id[:12], "err", err)
	}
}

// restoreVolumeBackup checks and starts the restore of a volume backup into
// svc, which must have every volume of the archive.
func (c *Core) restoreVolumeBackup(ctx context.Context, b store.Backup, svc store.Service, confirm string) (store.Restore, error) {
	if _, err := backupKind(svc); err != nil || svc.Kind == store.ServiceKindPostgres {
		return store.Restore{}, fmt.Errorf("%w: %s has no volumes to restore into", ErrInvalid, svc.Name)
	}
	have := volumeNames(svc)
	for _, name := range b.Volumes {
		if !slices.Contains(have, name) {
			return store.Restore{}, fmt.Errorf("%w: %s has no volume %q", ErrInvalid, svc.Name, name)
		}
	}
	if confirm != svc.Name {
		return store.Restore{}, fmt.Errorf("%w: restoring overwrites the volumes of %s; confirm with its name", ErrInvalid, svc.Name)
	}
	target, err := c.store.GetBackupTarget(ctx, b.TargetID)
	if err != nil {
		return store.Restore{}, err
	}
	st, err := c.openStorage(target)
	if err != nil {
		return store.Restore{}, err
	}
	unlock, err := c.lockService(svc.ID)
	if err != nil {
		return store.Restore{}, err
	}
	r, err := c.store.CreateRestore(ctx, store.Restore{BackupID: b.ID, ServiceID: svc.ID, Status: store.OpRunning})
	if err != nil {
		unlock()
		return store.Restore{}, err
	}
	if err := c.goBackground(func() {
		defer unlock()
		ctx, cancel := context.WithTimeout(c.bg, restoreTimeout)
		defer cancel()
		swapped, err := c.restoreVolumes(ctx, svc, st, target, b, r)
		after := "The live volumes were left unchanged."
		if swapped {
			after = "The volumes may be partially restored: restore again."
		}
		c.finishRestore(ctx, svc, target, b, r, err, "Its volumes now hold", after)
	}); err != nil {
		unlock()
		_ = c.store.FinishRestore(ctx, r.ID, store.OpFailed, err.Error())
		return store.Restore{}, err
	}
	return r, nil
}

// restoreVolumes extracts the archive into a staging volume and checks it,
// then stops the service, replaces the content of each volume with the
// staged copy and starts the service again. It reports whether the live
// volumes were touched.
func (c *Core) restoreVolumes(ctx context.Context, svc store.Service, st storage.Storage, target store.BackupTarget, b store.Backup, r store.Restore) (bool, error) {
	rc, err := st.Get(ctx, b.ObjectKey)
	if err != nil {
		return false, fmt.Errorf("open backup: %w", err)
	}
	defer rc.Close()

	dk := c.dockerFor(svc.ServerID)
	stage := "kipitiny-stage-" + strings.ToLower(r.ID)
	if _, err := dk.VolumeCreate(ctx, client.VolumeCreateOptions{
		Name:   stage,
		Labels: map[string]string{docker.LabelManaged: "true", docker.LabelComponent: volumeStageComponent},
	}); err != nil {
		return false, fmt.Errorf("staging volume: %w", err)
	}
	defer func() {
		if err := dk.RemoveVolume(context.WithoutCancel(ctx), stage); err != nil {
			c.log.Warn("cannot remove staging volume", "volume", stage, "err", err)
		}
	}()
	mounts := append(helperMounts(svc, b.Volumes, helperVolumeRoot, false),
		mount.Mount{Type: mount.TypeVolume, Source: stage, Target: helperStageRoot})
	id, err := startVolumeHelper(ctx, dk, "kipitiny-restore-"+strings.ToLower(r.ID), mounts)
	if err != nil {
		return false, err
	}
	defer c.removeHelper(ctx, dk, id)

	err = fromStorage(rc, target, b, func(archive io.Reader) error {
		if err := dk.Exec(ctx, id, docker.ExecOptions{
			Cmd:   []string{"tar", "-xzf", "-", "--numeric-owner", "-C", helperStageRoot},
			Stdin: archive,
		}); err != nil {
			return fmt.Errorf("extract: %w", err)
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	check := `for n; do [ -d "` + helperStageRoot + `/$n" ] || { echo "volume $n is missing from the backup" >&2; exit 1; }; done`
	if err := dk.Exec(ctx, id, docker.ExecOptions{Cmd: append([]string{"sh", "-c", check, "sh"}, b.Volumes...)}); err != nil {
		return false, err
	}

	stopped, err := c.stopServiceContainers(ctx, svc)
	defer func() {
		for _, ct := range stopped {
			if _, err := dk.ContainerStart(context.WithoutCancel(ctx), ct, client.ContainerStartOptions{}); err != nil {
				c.log.Error("cannot restart container after restore", "container", ct, "err", err)
			}
		}
	}()
	if err != nil {
		return false, err
	}
	swap := `set -e
for n; do
  find "` + helperVolumeRoot + `/$n" -mindepth 1 -maxdepth 1 -exec rm -rf {} +
  cp -a "` + helperStageRoot + `/$n/." "` + helperVolumeRoot + `/$n/"
done`
	if err := dk.Exec(context.WithoutCancel(ctx), id, docker.ExecOptions{Cmd: append([]string{"sh", "-c", swap, "sh"}, b.Volumes...)}); err != nil {
		return true, fmt.Errorf("replace volume content: %w", err)
	}
	return true, nil
}

// stopServiceContainers stops the service's running containers and returns
// their IDs so they can be started again.
func (c *Core) stopServiceContainers(ctx context.Context, svc store.Service) ([]string, error) {
	cts, err := c.serviceContainers(ctx, svc)
	if err != nil {
		return nil, err
	}
	var stopped []string
	secs := int(stopTimeoutFor(svc).Seconds())
	for _, ct := range cts {
		if ct.State != container.StateRunning {
			continue
		}
		if _, err := c.dockerFor(svc.ServerID).ContainerStop(ctx, ct.ID, client.ContainerStopOptions{Timeout: &secs}); err != nil {
			return stopped, err
		}
		stopped = append(stopped, ct.ID)
	}
	return stopped, nil
}

// verifyVolumes reads the whole archive in a throwaway helper: it must
// decrypt, decompress and list cleanly, and match the recorded checksum.
func (c *Core) verifyVolumes(ctx context.Context, b store.Backup) (store.VerificationDetails, error) {
	var d store.VerificationDetails
	target, err := c.store.GetBackupTarget(ctx, b.TargetID)
	if err != nil {
		return d, err
	}
	st, err := c.openStorage(target)
	if err != nil {
		return d, err
	}
	serverID := store.LocalServerID
	if svc, err := c.store.GetService(ctx, b.ServiceID); err == nil {
		serverID = svc.ServerID
	}
	dk := c.dockerFor(serverID)
	id, err := startVolumeHelper(ctx, dk, "kipitiny-verify-"+strings.ToLower(b.ID), nil)
	if err != nil {
		return d, err
	}
	defer c.removeHelper(ctx, dk, id)

	rc, err := st.Get(ctx, b.ObjectKey)
	if err != nil {
		return d, fmt.Errorf("open backup: %w", err)
	}
	defer rc.Close()
	files := &lineCounter{}
	err = fromStorage(rc, target, b, func(archive io.Reader) error {
		if err := dk.Exec(ctx, id, docker.ExecOptions{Cmd: []string{"tar", "-tzf", "-"}, Stdin: archive, Stdout: files}); err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		return nil
	})
	if err != nil {
		return d, err
	}
	if files.n == 0 {
		return d, errors.New("the archive is empty")
	}
	d.Files = files.n
	return d, nil
}

type lineCounter struct{ n int64 }

func (l *lineCounter) Write(p []byte) (int, error) {
	l.n += int64(bytes.Count(p, []byte{'\n'}))
	return len(p), nil
}
