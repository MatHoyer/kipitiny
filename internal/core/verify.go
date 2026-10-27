package core

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/notify"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	verifyComponent = "restore-test"
	verifyTimeout   = 6 * time.Hour
	verifyMemoryMB  = 512
)

// VerifyBackup proves a backup can actually be restored: a database dump is
// restored into a throwaway, network-less server of its kind and checked; a
// volume archive is read through entirely. The outcome is recorded on the
// backup.
func (c *Core) VerifyBackup(ctx context.Context, backupID string) (store.Backup, error) {
	b, err := c.store.GetBackup(ctx, backupID)
	if err != nil {
		return store.Backup{}, err
	}
	if b.Kind == store.BackupKindManager || b.Status != store.OpSucceeded {
		return store.Backup{}, fmt.Errorf("%w: only successful service backups can be verified", ErrInvalid)
	}
	if b.VerifyStatus == store.OpRunning {
		return store.Backup{}, ErrBusy
	}
	b.Verification = store.Verification{VerifyStatus: store.OpRunning}
	if err := c.store.SetBackupVerification(ctx, b.ID, b.Verification); err != nil {
		return store.Backup{}, err
	}
	c.steps.set(verifyKey(b.ID), VerifyQueued)
	if err := c.goBackground(func() { c.runVerify(b) }); err != nil {
		c.steps.done(verifyKey(b.ID))
		now := time.Now()
		_ = c.store.SetBackupVerification(ctx, b.ID, store.Verification{VerifyStatus: store.OpFailed, VerifyError: err.Error(), VerifiedAt: &now})
		return store.Backup{}, err
	}
	return b, nil
}

// Steps of a restore test, as Backup.VerifyStep reports them.
const (
	// VerifyQueued: waiting for another restore test to finish.
	VerifyQueued = "queued"
	// VerifyImage: getting the database server's image.
	VerifyImage = "image"
	// VerifyServer: starting the throwaway server (or helper) and waiting
	// until it's ready.
	VerifyServer = "server"
	// VerifyRestore: downloading the backup and loading it.
	VerifyRestore = "restore"
	// VerifyRead: downloading a volume archive and reading it through.
	VerifyRead = "read"
	// VerifyCheck: measuring what came back.
	VerifyCheck = "check"
)

func (c *Core) runVerify(b store.Backup) {
	defer c.steps.done(verifyKey(b.ID))
	// One restore test at a time: they can be memory- and disk-hungry.
	select {
	case c.verifySem <- struct{}{}:
		defer func() { <-c.verifySem }()
	case <-c.bg.Done():
		return
	}
	ctx, cancel := context.WithTimeout(c.bg, verifyTimeout)
	defer cancel()
	log := c.log.With("backup", b.ID, "service", b.ServiceName)
	start := time.Now()

	var details store.VerificationDetails
	var err error
	switch b.Kind {
	case store.BackupKindVolume:
		details, err = c.verifyVolumes(ctx, b)
	case store.BackupKindDump:
		details, err = c.verifyDump(ctx, b)
	default:
		details, err = c.verify(ctx, b)
	}
	details.DurationMS = time.Since(start).Milliseconds()
	now := time.Now()
	v := store.Verification{VerifyStatus: store.OpSucceeded, VerifyDetails: details, VerifiedAt: &now}
	if err != nil {
		v.VerifyStatus, v.VerifyError = store.OpFailed, err.Error()
		if errors.Is(ctx.Err(), context.Canceled) {
			v.VerifyError = "interrupted by manager shutdown"
		}
		log.Warn("restore test failed", "err", err)
	} else {
		log.Info("restore test passed", "tables", details.Tables, "rows", details.Rows, "files", details.Files)
	}
	if err := c.store.SetBackupVerification(context.WithoutCancel(ctx), b.ID, v); err != nil && !errors.Is(err, store.ErrNotFound) {
		log.Error("cannot record restore test", "err", err)
	}

	subject := b.ProjectName + "/" + b.ServiceName
	e := notify.Event{
		Type:    EventVerifySucceeded,
		Level:   notify.Success,
		Title:   "Restore test passed for " + subject,
		Message: fmt.Sprintf("The backup of %s restores cleanly.", b.CreatedAt.UTC().Format("2006-01-02 15:04 UTC")),
		Fields: []notify.Field{
			{Name: "Tables", Value: fmt.Sprint(details.Tables)},
			{Name: "Rows", Value: fmt.Sprint(details.Rows)},
		},
	}
	if b.Kind == store.BackupKindVolume {
		e.Fields = []notify.Field{{Name: "Files", Value: fmt.Sprint(details.Files)}}
	}
	if err != nil {
		e.Type, e.Level, e.Title, e.Message, e.Fields = EventVerifyFailed, notify.Error, "Restore test failed for "+subject,
			fmt.Sprintf("The backup of %s could not be restored: %s", b.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"), v.VerifyError), nil
	}
	c.notify(e, "/services/"+b.ServiceID, "")
}

func (c *Core) verify(ctx context.Context, b store.Backup) (store.VerificationDetails, error) {
	var d store.VerificationDetails
	target, err := c.store.GetBackupTarget(ctx, b.TargetID)
	if err != nil {
		return d, err
	}
	st, err := c.openStorage(target)
	if err != nil {
		return d, err
	}
	const user, db = "app", "app"
	dk, id, err := c.scratchPostgres(ctx, b, "kipitiny-verify-"+strings.ToLower(b.ID), user, db, func(s string) { c.steps.set(verifyKey(b.ID), s) })
	if err != nil {
		return d, err
	}
	defer c.removeScratch(ctx, dk, id)
	// The image's init scripts may have filled db (PostGIS creates its
	// extensions there): restore into an empty database, as a restore does.
	const restored = "restore_test"
	if err := dk.Exec(ctx, id, docker.ExecOptions{
		Cmd: []string{"psql", "-U", user, "-d", db, "-v", "ON_ERROR_STOP=1", "-qc", "CREATE DATABASE " + restored + " TEMPLATE template0"},
	}); err != nil {
		return d, fmt.Errorf("create database: %w", err)
	}

	c.steps.set(verifyKey(b.ID), VerifyRestore)
	rc, err := st.Get(ctx, b.ObjectKey)
	if err != nil {
		return d, fmt.Errorf("open backup: %w", err)
	}
	defer rc.Close()
	if err := c.pgRestore(ctx, dk, id, user, restored, rc, target, b); err != nil {
		return d, err
	}

	// Sanity check: statistics on what came back.
	c.steps.set(verifyKey(b.ID), VerifyCheck)
	query := `ANALYZE;
SELECT (SELECT count(*) FROM pg_stat_user_tables),
       (SELECT coalesce(sum(n_live_tup), 0) FROM pg_stat_user_tables),
       pg_database_size(current_database());`
	var out strings.Builder
	if err := dk.Exec(ctx, id, docker.ExecOptions{
		Cmd:    []string{"psql", "-U", user, "-d", restored, "-v", "ON_ERROR_STOP=1", "-tAq", "-c", query},
		Stdout: &out,
	}); err != nil {
		return d, fmt.Errorf("sanity query: %w", err)
	}
	return parseVerifyOutput(out.String())
}

// scratchPostgres starts a throwaway, network-less server able to restore
// b, with the superuser user and the database db, and waits until it's
// ready; step reports where it's at (VerifyImage, VerifyServer). The caller
// removes it with removeScratch.
func (c *Core) scratchPostgres(ctx context.Context, b store.Backup, name, user, db string, step func(string)) (*docker.Client, string, error) {
	image, serverID := c.verifyImage(ctx, b)
	dk := c.dockerFor(serverID)
	step(VerifyImage)
	if err := dk.EnsureImage(ctx, image, c.registryAuth(ctx, image)); err != nil {
		return nil, "", fmt.Errorf("pull %s: %w", image, err)
	}
	step(VerifyServer)
	id, err := dk.Run(ctx, client.ContainerCreateOptions{
		Name: name,
		Config: &container.Config{
			Image: image,
			Env:   []string{"POSTGRES_USER=" + user, "POSTGRES_DB=" + db, "POSTGRES_PASSWORD=" + randomToken(16)},
			Labels: map[string]string{
				docker.LabelManaged:   "true",
				docker.LabelComponent: verifyComponent,
			},
			Healthcheck: postgresHealthcheck(user, db),
		},
		HostConfig: &container.HostConfig{
			// Isolated: the restore arrives over exec, nothing needs a network.
			NetworkMode: "none",
			Resources:   container.Resources{Memory: verifyMemoryMB << 20},
			ShmSize:     128 << 20,
			LogConfig:   docker.DefaultLogConfig(),
		},
	})
	if err != nil {
		return nil, "", err
	}
	if err := dk.WaitHealthy(ctx, id, postgresReadyTimeout); err != nil {
		c.removeScratch(ctx, dk, id)
		return nil, "", fmt.Errorf("throwaway server: %w", err)
	}
	return dk, id, nil
}

// removeScratch removes a throwaway server and its data, an anonymous
// volume.
func (c *Core) removeScratch(ctx context.Context, dk *docker.Client, id string) {
	if err := dk.RemoveContainerAndVolumes(context.WithoutCancel(ctx), id); err != nil {
		c.log.Warn("cannot remove throwaway server", "id", id[:12], "err", err)
	}
}

// parseVerifyOutput reads "tables|rows|bytes" from psql -tA output.
func parseVerifyOutput(s string) (store.VerificationDetails, error) {
	var d store.VerificationDetails
	fields := strings.Split(strings.TrimSpace(s), "|")
	if len(fields) != 3 {
		return d, fmt.Errorf("sanity query: unexpected output %q", s)
	}
	var err error
	if d.Tables, err = strconv.Atoi(fields[0]); err != nil {
		return d, fmt.Errorf("sanity query: %w", err)
	}
	if d.Rows, err = strconv.ParseInt(fields[1], 10, 64); err != nil {
		return d, fmt.Errorf("sanity query: %w", err)
	}
	if d.DBBytes, err = strconv.ParseInt(fields[2], 10, 64); err != nil {
		return d, fmt.Errorf("sanity query: %w", err)
	}
	return d, nil
}

// verifyImage picks where and with what a restore test runs: on the
// database's server with its own image when it still exists and runs the
// backup's major version, otherwise with the image that made the backup, or
// the official image of that major for backups that don't record it.
func (c *Core) verifyImage(ctx context.Context, b store.Backup) (image, serverID string) {
	major := pgMajorFromVersion(b.PGVersion)
	svc, err := c.store.GetService(ctx, b.ServiceID)
	if err == nil && postgresMajor(svc.Image) == major {
		return svc.Image, svc.ServerID
	}
	serverID = store.LocalServerID
	if err == nil {
		serverID = svc.ServerID
	}
	// The image that made it (PostGIS…), when the service is gone or moved on.
	if b.Image != "" && postgresMajor(b.Image) == major {
		return b.Image, serverID
	}
	if major == 0 {
		return DefaultPostgresImage, serverID
	}
	return fmt.Sprintf("postgres:%d-alpine", major), serverID
}

// cleanupRestoreTests removes throwaway containers (restore tests, volume
// helpers) and staging volumes left by a crash.
func (c *Core) cleanupRestoreTests(ctx context.Context, dk *docker.Client) error {
	for _, component := range []string{verifyComponent, volumeHelperComponent} {
		cts, err := dk.ListContainers(ctx, map[string]string{docker.LabelComponent: component})
		if err != nil {
			return err
		}
		for _, ct := range cts {
			if err := dk.RemoveContainerAndVolumes(ctx, ct.ID); err != nil {
				return err
			}
		}
	}
	f := make(client.Filters).Add("label", docker.LabelComponent+"="+volumeStageComponent)
	vols, err := dk.VolumeList(ctx, client.VolumeListOptions{Filters: f})
	if err != nil {
		return err
	}
	for _, v := range vols.Items {
		if err := dk.RemoveVolume(ctx, v.Name); err != nil {
			return err
		}
	}
	return nil
}
