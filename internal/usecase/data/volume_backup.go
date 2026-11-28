package data

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

// HelperImage is the small image helper containers run in, mounting
// volumes to copy files in or out.
const HelperImage = "busybox:stable"

// Volume backups archive a service's volumes (gzipped tar, one top-level
// directory per volume) through a short-lived, network-less helper container
// that mounts them, so neither the service image nor the manager needs tar.
const (
	volumeHelperComponent = "volume-helper"
	volumeStageComponent  = "volume-stage"
	volumeHelperImage     = HelperImage
	// dataVolumeName names a database's data volume inside an archive.
	dataVolumeName   = "data"
	helperVolumeRoot = "/v"
	helperStageRoot  = "/stage"
	preBackupTimeout = 10 * time.Minute
)

// volumeNames lists what a volume backup of svc archives: Redis's data
// volume (the other databases are dumped by their own tool), or an app's
// volumes.
func volumeNames(svc domain.Service) []string {
	switch {
	case svc.Kind == domain.ServiceKindRedis:
		return []string{dataVolumeName}
	case svc.Kind.IsDatabase():
		return nil
	}
	names := make([]string, len(svc.Volumes))
	for i, v := range svc.Volumes {
		names[i] = v.Name
	}
	return names
}

// backupKind is how a service is backed up.
func backupKind(svc domain.Service) (domain.BackupKind, error) {
	switch {
	case svc.Kind == domain.ServiceKindPostgres:
		return domain.BackupKindPostgres, nil
	case svc.Kind.IsMySQL(), svc.Kind == domain.ServiceKindMongoDB:
		return domain.BackupKindDump, nil
	case len(volumeNames(svc)) > 0:
		return domain.BackupKindVolume, nil
	}
	return "", fmt.Errorf("%w: %s has no volumes to back up", ErrInvalid, svc.Name)
}

// helperMounts mounts the named volumes of svc under helperVolumeRoot/<name>, read-only
// if readOnly or if the app mounts the volume read-only.
func helperMounts(svc domain.Service, names []string, readOnly bool) []domain.Mount {
	mounts := make([]domain.Mount, 0, len(names))
	for _, name := range names {
		var m domain.Mount
		if svc.Kind.IsDatabase() {
			m = domain.Mount{Type: domain.MountVolume, Source: DataVolume(svc)}
		} else {
			m = AppVolumeMount(svc, name, "")
		}
		m.Target, m.ReadOnly = helperVolumeRoot+"/"+name, readOnly || m.ReadOnly
		mounts = append(mounts, m)
	}
	return mounts
}

// startVolumeHelper runs an idle helper with mounts; commands run in it
// over exec. The caller removes it.
func startVolumeHelper(ctx context.Context, dk domain.ContainerRuntime, name string, mounts []domain.Mount) (string, error) {
	// A fixed public image: no registry credential.
	if err := dk.EnsureImage(ctx, volumeHelperImage, "", ""); err != nil {
		return "", fmt.Errorf("pull %s: %w", volumeHelperImage, err)
	}
	return dk.Run(ctx, domain.ContainerSpec{
		Name:        name,
		Image:       volumeHelperImage,
		Cmd:         []string{"sleep", "2147483647"},
		Labels:      map[string]string{domain.LabelManaged: "true", domain.LabelComponent: volumeHelperComponent},
		NetworkMode: "none",
		Mounts:      mounts,
		Log:         true,
	})
}

// dumpVolumes streams an archive of the backup's volumes to storage. The
// service keeps running: files are copied as they are (run a pre-backup
// command to flush them first).
func (c *Data) dumpVolumes(ctx context.Context, svc domain.Service, st Storage, target domain.BackupTarget, b *domain.Backup) error {
	if svc.PreBackup != "" {
		c.steps.Set(backupKey(b.ID), BackupPreBackup)
		if err := c.preBackup(ctx, svc); err != nil {
			return err
		}
	}
	dk := c.mgr.DockerFor(svc.ServerID)
	c.steps.Set(backupKey(b.ID), BackupHelper)
	id, err := startVolumeHelper(ctx, dk, "kipitiny-backup-"+strings.ToLower(b.ID), helperMounts(svc, b.Volumes, true))
	if err != nil {
		return err
	}
	defer c.removeHelper(ctx, dk, id)
	return c.upload(ctx, st, target, b, func(w io.Writer) error {
		err := dk.Exec(ctx, id, domain.ExecOptions{
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
func (c *Data) preBackup(ctx context.Context, svc domain.Service) error {
	ctx, cancel := context.WithTimeout(ctx, preBackupTimeout)
	defer cancel()
	cts, err := c.mgr.ActiveContainers(ctx, svc)
	if err != nil {
		return err
	}
	for _, ct := range cts {
		if ct.State != domain.StateRunning {
			continue
		}
		if err := c.mgr.DockerFor(svc.ServerID).Exec(ctx, ct.ID, domain.ExecOptions{Cmd: []string{"sh", "-c", svc.PreBackup}}); err != nil {
			return fmt.Errorf("pre-backup command: %w", err)
		}
		return nil
	}
	return nil
}

func (c *Data) removeHelper(ctx context.Context, dk domain.ContainerRuntime, id string) {
	if err := dk.RemoveContainerAndVolumes(context.WithoutCancel(ctx), id); err != nil {
		c.log.Warn("cannot remove volume helper", "id", id[:12], "err", err)
	}
}

// restoreVolumeBackup checks and starts the restore of a volume backup into
// svc, which must have every volume of the archive.
func (c *Data) restoreVolumeBackup(ctx context.Context, b domain.Backup, svc domain.Service, confirm string) (domain.Restore, error) {
	if kind, err := backupKind(svc); err != nil || kind != domain.BackupKindVolume {
		return domain.Restore{}, fmt.Errorf("%w: %s has no volumes to restore into", ErrInvalid, svc.Name)
	}
	have := volumeNames(svc)
	for _, name := range b.Volumes {
		if !slices.Contains(have, name) {
			return domain.Restore{}, fmt.Errorf("%w: %s has no volume %q", ErrInvalid, svc.Name, name)
		}
	}
	// An admin attaches host data read-only on purpose: only an admin overwrites it.
	for _, v := range svc.Volumes {
		if v.ReadOnly && v.HostAccess() && slices.Contains(b.Volumes, v.Name) {
			if err := identity.RequireAdmin(ctx); err != nil {
				return domain.Restore{}, fmt.Errorf("%w: volume %s is attached read-only; only an admin restores it", identity.ErrForbidden, v.Name)
			}
		}
	}
	if confirm != svc.Name {
		return domain.Restore{}, fmt.Errorf("%w: restoring overwrites the volumes of %s; confirm with its name", ErrInvalid, svc.Name)
	}
	target, err := c.store.GetBackupTarget(ctx, b.TargetID)
	if err != nil {
		return domain.Restore{}, err
	}
	st, err := c.openStorage(target)
	if err != nil {
		return domain.Restore{}, err
	}
	unlock, err := c.mgr.LockService(svc.ID)
	if err != nil {
		return domain.Restore{}, err
	}
	r, err := c.store.CreateRestore(ctx, domain.Restore{BackupID: b.ID, ServiceID: svc.ID, Status: domain.OpRunning})
	if err != nil {
		unlock()
		return domain.Restore{}, err
	}
	if err := c.mgr.GoBackground(func() {
		defer unlock()
		ctx, cancel := context.WithTimeout(c.bg, RestoreTimeout)
		defer cancel()
		defer c.steps.Done(restoreKey(r.ID))
		swapped, err := c.restoreVolumes(ctx, svc, st, target, b, r)
		after := "The live volumes were left unchanged."
		if swapped {
			after = "The volumes may be partially restored: restore again."
		}
		c.finishRestore(ctx, svc, target, b, r, err, "Its volumes now hold", after)
	}); err != nil {
		unlock()
		_ = c.store.FinishRestore(ctx, r.ID, domain.OpFailed, err.Error())
		return domain.Restore{}, err
	}
	return r, nil
}

// restoreVolumes extracts the archive into a staging volume and checks it,
// then stops the service, replaces the content of each volume with the
// staged copy and starts the service again. It reports whether the live
// volumes were touched.
func (c *Data) restoreVolumes(ctx context.Context, svc domain.Service, st Storage, target domain.BackupTarget, b domain.Backup, r domain.Restore) (bool, error) {
	key := restoreKey(r.ID)
	c.steps.Set(key, RestoreExtract)
	rc, err := st.Get(ctx, b.ObjectKey)
	if err != nil {
		return false, fmt.Errorf("open backup: %w", err)
	}
	defer rc.Close()

	dk := c.mgr.DockerFor(svc.ServerID)
	stage := "kipitiny-stage-" + strings.ToLower(r.ID)
	if err := dk.CreateVolume(ctx, stage, map[string]string{domain.LabelManaged: "true", domain.LabelComponent: volumeStageComponent}); err != nil {
		return false, fmt.Errorf("staging volume: %w", err)
	}
	defer func() {
		if err := dk.RemoveVolume(context.WithoutCancel(ctx), stage); err != nil {
			c.log.Warn("cannot remove staging volume", "volume", stage, "err", err)
		}
	}()
	mounts := helperMounts(svc, b.Volumes, false)
	for i := range mounts {
		mounts[i].ReadOnly = false // a restore writes read-only volumes too (restoreVolumeBackup checked who may)
	}
	mounts = append(mounts, domain.Mount{Type: domain.MountVolume, Source: stage, Target: helperStageRoot})
	id, err := startVolumeHelper(ctx, dk, "kipitiny-restore-"+strings.ToLower(r.ID), mounts)
	if err != nil {
		return false, err
	}
	defer c.removeHelper(ctx, dk, id)

	err = c.fromStorage(rc, target, b, func(archive io.Reader) error {
		if err := dk.Exec(ctx, id, domain.ExecOptions{
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
	if err := dk.Exec(ctx, id, domain.ExecOptions{Cmd: append([]string{"sh", "-c", check, "sh"}, b.Volumes...)}); err != nil {
		return false, err
	}

	c.steps.Set(key, RestoreStopService)
	stopped, err := c.stopServiceContainers(ctx, svc)
	defer func() {
		if len(stopped) > 0 {
			c.steps.Set(key, RestoreStart)
		}
		for _, ct := range stopped {
			if err := dk.StartContainer(context.WithoutCancel(ctx), ct); err != nil {
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
	c.steps.Set(key, RestoreReplace)
	if err := dk.Exec(context.WithoutCancel(ctx), id, domain.ExecOptions{Cmd: append([]string{"sh", "-c", swap, "sh"}, b.Volumes...)}); err != nil {
		return true, fmt.Errorf("replace volume content: %w", err)
	}
	return true, nil
}

// stopServiceContainers stops the service's running containers and returns
// their IDs so they can be started again.
func (c *Data) stopServiceContainers(ctx context.Context, svc domain.Service) ([]string, error) {
	cts, err := c.mgr.ServiceContainers(ctx, svc)
	if err != nil {
		return nil, err
	}
	var stopped []string
	secs := int(StopTimeoutFor(svc).Seconds())
	for _, ct := range cts {
		if ct.State != domain.StateRunning {
			continue
		}
		if err := c.mgr.DockerFor(svc.ServerID).StopContainer(ctx, ct.ID, &secs); err != nil {
			return stopped, err
		}
		stopped = append(stopped, ct.ID)
	}
	return stopped, nil
}

// verifyVolumes reads the whole archive in a throwaway helper: it must
// decrypt, decompress and list cleanly, and match the recorded checksum.
func (c *Data) verifyVolumes(ctx context.Context, b domain.Backup) (domain.VerificationDetails, error) {
	var d domain.VerificationDetails
	target, err := c.store.GetBackupTarget(ctx, b.TargetID)
	if err != nil {
		return d, err
	}
	st, err := c.openStorage(target)
	if err != nil {
		return d, err
	}
	serverID := domain.LocalServerID
	if svc, err := c.store.GetService(ctx, b.ServiceID); err == nil {
		serverID = svc.ServerID
	}
	dk := c.mgr.DockerFor(serverID)
	c.steps.Set(verifyKey(b.ID), VerifyServer)
	id, err := startVolumeHelper(ctx, dk, "kipitiny-verify-"+strings.ToLower(b.ID), nil)
	if err != nil {
		return d, err
	}
	defer c.removeHelper(ctx, dk, id)

	c.steps.Set(verifyKey(b.ID), VerifyRead)
	rc, err := st.Get(ctx, b.ObjectKey)
	if err != nil {
		return d, fmt.Errorf("open backup: %w", err)
	}
	defer rc.Close()
	files := &lineCounter{}
	err = c.fromStorage(rc, target, b, func(archive io.Reader) error {
		if err := dk.Exec(ctx, id, domain.ExecOptions{Cmd: []string{"tar", "-tzf", "-"}, Stdin: archive, Stdout: files}); err != nil {
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
