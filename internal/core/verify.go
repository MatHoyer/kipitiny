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
	"github.com/MatHoyer/kipitiny/internal/storage"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	verifyComponent = "restore-test"
	verifyTimeout   = 6 * time.Hour
	verifyMemoryMB  = 512
)

// VerifyBackup restores a backup into a throwaway, network-less Postgres
// container and checks the result, proving the backup can actually be
// restored. The outcome is recorded on the backup.
func (c *Core) VerifyBackup(ctx context.Context, backupID string) (store.Backup, error) {
	b, err := c.store.GetBackup(ctx, backupID)
	if err != nil {
		return store.Backup{}, err
	}
	if b.Kind != store.BackupKindPostgres || b.Status != store.OpSucceeded {
		return store.Backup{}, fmt.Errorf("%w: only successful database backups can be verified", ErrInvalid)
	}
	if b.VerifyStatus == store.OpRunning {
		return store.Backup{}, ErrBusy
	}
	b.Verification = store.Verification{VerifyStatus: store.OpRunning}
	if err := c.store.SetBackupVerification(ctx, b.ID, b.Verification); err != nil {
		return store.Backup{}, err
	}
	if err := c.goBackground(func() { c.runVerify(b) }); err != nil {
		now := time.Now()
		_ = c.store.SetBackupVerification(ctx, b.ID, store.Verification{VerifyStatus: store.OpFailed, VerifyError: err.Error(), VerifiedAt: &now})
		return store.Backup{}, err
	}
	return b, nil
}

func (c *Core) runVerify(b store.Backup) {
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

	details, err := c.verify(ctx, b)
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
		log.Info("restore test passed", "tables", details.Tables, "rows", details.Rows)
	}
	if err := c.store.SetBackupVerification(context.WithoutCancel(ctx), b.ID, v); err != nil && !errors.Is(err, store.ErrNotFound) {
		log.Error("cannot record restore test", "err", err)
	}
}

func (c *Core) verify(ctx context.Context, b store.Backup) (store.VerificationDetails, error) {
	var d store.VerificationDetails
	target, err := c.store.GetBackupTarget(ctx, b.TargetID)
	if err != nil {
		return d, err
	}
	st, err := storage.Open(target, c.cfg.DataDir)
	if err != nil {
		return d, err
	}
	image := c.verifyImage(ctx, b)
	if err := c.docker.EnsureImage(ctx, image); err != nil {
		return d, fmt.Errorf("pull %s: %w", image, err)
	}

	const user, db = "app", "app"
	id, err := c.docker.Run(ctx, client.ContainerCreateOptions{
		Name: "kipitiny-verify-" + strings.ToLower(b.ID),
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
		return d, err
	}
	defer func() {
		// The data directory is an anonymous volume: remove it too.
		if err := c.docker.RemoveContainerAndVolumes(context.WithoutCancel(ctx), id); err != nil {
			c.log.Warn("cannot remove restore test container", "id", id[:12], "err", err)
		}
	}()
	if err := c.docker.WaitHealthy(ctx, id, postgresReadyTimeout); err != nil {
		return d, fmt.Errorf("throwaway server: %w", err)
	}

	rc, err := st.Get(ctx, b.ObjectKey)
	if err != nil {
		return d, fmt.Errorf("open backup: %w", err)
	}
	defer rc.Close()
	if err := c.pgRestore(ctx, id, user, db, rc, target, b); err != nil {
		return d, err
	}

	// Sanity check: statistics on what came back.
	query := `ANALYZE;
SELECT (SELECT count(*) FROM pg_stat_user_tables),
       (SELECT coalesce(sum(n_live_tup), 0) FROM pg_stat_user_tables),
       pg_database_size(current_database());`
	var out strings.Builder
	if err := c.docker.Exec(ctx, id, docker.ExecOptions{
		Cmd:    []string{"psql", "-U", user, "-d", db, "-v", "ON_ERROR_STOP=1", "-tAq", "-c", query},
		Stdout: &out,
	}); err != nil {
		return d, fmt.Errorf("sanity query: %w", err)
	}
	return parseVerifyOutput(out.String())
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

// verifyImage uses the database's own image when it still exists and runs
// the backup's major version, otherwise the official image of that major.
func (c *Core) verifyImage(ctx context.Context, b store.Backup) string {
	major := pgMajorFromVersion(b.PGVersion)
	if svc, err := c.store.GetService(ctx, b.ServiceID); err == nil && postgresMajor(svc.Image) == major {
		return svc.Image
	}
	if major == 0 {
		return DefaultPostgresImage
	}
	return fmt.Sprintf("postgres:%d-alpine", major)
}

// cleanupRestoreTests removes throwaway containers left by a crash.
func (c *Core) cleanupRestoreTests(ctx context.Context) error {
	cts, err := c.docker.ListContainers(ctx, map[string]string{docker.LabelComponent: verifyComponent})
	if err != nil {
		return err
	}
	for _, ct := range cts {
		if err := c.docker.RemoveContainerAndVolumes(ctx, ct.ID); err != nil {
			return err
		}
	}
	return nil
}
