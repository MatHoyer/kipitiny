package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/ids"
	"github.com/MatHoyer/kipitiny/internal/storage"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	backupTimeout  = 6 * time.Hour
	restoreTimeout = 6 * time.Hour
)

// BackupDatabase starts a pg_dump of a postgres service to a target in the
// background. The dump runs inside the database container, so the client
// always matches the server version and the manager needs no Postgres tools.
func (c *Core) BackupDatabase(ctx context.Context, serviceID, targetID string) (store.Backup, error) {
	return c.startBackup(ctx, serviceID, targetID, "", nil)
}

// startBackup starts a backup; done, if set, is closed when it finishes.
// Backups made by a schedule trigger its retention on success.
func (c *Core) startBackup(ctx context.Context, serviceID, targetID, scheduleID string, done chan<- struct{}) (store.Backup, error) {
	if targetID == "" {
		targetID = store.LocalTargetID
	}
	svc, project, err := c.postgresService(ctx, serviceID)
	if err != nil {
		return store.Backup{}, err
	}
	target, err := c.store.GetBackupTarget(ctx, targetID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Backup{}, fmt.Errorf("%w: unknown backup target", ErrInvalid)
	}
	if err != nil {
		return store.Backup{}, err
	}
	st, err := storage.Open(target, c.cfg.DataDir)
	if err != nil {
		return store.Backup{}, err
	}
	unlock, err := c.lockService(svc.ID)
	if err != nil {
		return store.Backup{}, err
	}
	ct, err := c.runningContainer(ctx, svc)
	if err != nil {
		unlock()
		return store.Backup{}, err
	}

	created := time.Now().UTC()
	b, err := c.store.CreateBackup(ctx, store.Backup{
		ServiceID:   svc.ID,
		ProjectID:   project.ID,
		ServiceName: svc.Name,
		ProjectName: project.Name,
		TargetID:    target.ID,
		ScheduleID:  scheduleID,
		// The suffix keeps keys unique for backups started in the same second.
		ObjectKey: objectKey(fmt.Sprintf("%s/%s/", project.Name, svc.Name), created, ".dump", target),
		Status:    store.OpRunning,
	})
	if err != nil {
		unlock()
		return store.Backup{}, err
	}

	if err := c.goBackground(func() {
		if done != nil {
			defer close(done)
		}
		ok := c.runBackup(svc, ct, st, target, b)
		unlock()
		if ok && scheduleID != "" {
			c.applyRetention(scheduleID)
		}
	}); err != nil {
		unlock()
		b.Status, b.Error = store.OpFailed, err.Error()
		_ = c.store.FinishBackup(ctx, b)
		return store.Backup{}, err
	}
	return b, nil
}

// objectKey is dir/<timestamp>-<suffix><ext>[.age]. The suffix keeps keys
// unique for backups started in the same second.
func objectKey(dir string, created time.Time, ext string, target store.BackupTarget) string {
	key := dir + created.Format("20060102T150405Z") + "-" + strings.ToLower(ids.New()[16:]) + ext
	if target.Encrypted() {
		key += ".age"
	}
	return key
}

// BackupProject backs up every database of a project to one target.
// Failures to start are reported per database; the others still run.
func (c *Core) BackupProject(ctx context.Context, projectID, targetID string) ([]store.Backup, error) {
	svcs, err := c.store.ListServices(ctx, projectID)
	if err != nil {
		return nil, err
	}
	var started []store.Backup
	var errs []error
	for _, s := range svcs {
		if s.Kind != store.ServiceKindPostgres {
			continue
		}
		b, err := c.BackupDatabase(ctx, s.ID, targetID)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.Name, err))
			continue
		}
		started = append(started, b)
	}
	if len(started) == 0 && len(errs) == 0 {
		return nil, fmt.Errorf("%w: the project has no databases", ErrInvalid)
	}
	return started, errors.Join(errs...)
}

// runBackup performs the backup and records the outcome; it reports success.
func (c *Core) runBackup(svc store.Service, containerID string, st storage.Storage, target store.BackupTarget, b store.Backup) bool {
	ctx, cancel := context.WithTimeout(c.bg, backupTimeout)
	defer cancel()
	log := c.log.With("backup", b.ID, "service", svc.Name)
	start := time.Now()

	err := c.dump(ctx, svc, containerID, st, target, &b)
	b.DurationMS = time.Since(start).Milliseconds()
	b.Status = store.OpSucceeded
	if err != nil {
		b.Status, b.Error = store.OpFailed, err.Error()
		b.SizeBytes, b.SHA256 = 0, ""
		if errors.Is(ctx.Err(), context.Canceled) {
			b.Error = "interrupted by manager shutdown"
		}
		// Never leave a partial dump that looks like a backup.
		if derr := st.Delete(context.WithoutCancel(ctx), b.ObjectKey); derr != nil {
			log.Warn("cannot delete partial backup", "err", derr)
		}
		log.Warn("backup failed", "err", err)
	} else {
		log.Info("backup succeeded", "bytes", b.SizeBytes, "ms", b.DurationMS)
	}
	if err := c.store.FinishBackup(context.WithoutCancel(ctx), b); err != nil {
		log.Error("cannot record backup result", "err", err)
		return false
	}
	return err == nil && b.Status == store.OpSucceeded
}

// dump streams pg_dump output to storage, filling in size, checksum and
// server version on b.
func (c *Core) dump(ctx context.Context, svc store.Service, containerID string, st storage.Storage, target store.BackupTarget, b *store.Backup) error {
	version, err := c.serverVersion(ctx, svc, containerID)
	if err != nil {
		return fmt.Errorf("server version: %w", err)
	}
	b.PGVersion = version
	return c.upload(ctx, st, target, b, func(w io.Writer) error {
		err := c.docker.Exec(ctx, containerID, docker.ExecOptions{
			// Custom format: compressed, and pg_restore can restore selectively.
			Cmd:    []string{"pg_dump", "-U", svc.Env[pgUser], "-d", svc.Env[pgDatabase], "-Fc"},
			Stdout: w,
		})
		if err != nil {
			return fmt.Errorf("pg_dump: %w", err)
		}
		return nil
	})
}

// upload streams what produce writes to storage under b.ObjectKey, through
// age encryption when the target has a recipient, and records size and
// SHA-256 of the stored bytes on b. produce's error takes precedence: a
// producer can fail after data already flowed.
func (c *Core) upload(ctx context.Context, st storage.Storage, target store.BackupTarget, b *store.Backup,
	produce func(w io.Writer) error) error {

	pr, pw := io.Pipe()
	prodErr := make(chan error, 1)
	go func() {
		err := func() error {
			var w io.Writer = pw
			if target.Encrypted() {
				enc, err := encryptTo(pw, target.AgeRecipient)
				if err != nil {
					return err
				}
				if err := produce(enc); err != nil {
					return err
				}
				return enc.Close() // writes the final authenticated chunk
			}
			return produce(w)
		}()
		pw.CloseWithError(err) // nil → EOF for the uploader
		prodErr <- err
	}()

	sum := &hashCounter{h: sha256.New()}
	putErr := st.Put(ctx, b.ObjectKey, io.TeeReader(pr, sum))
	pr.CloseWithError(errors.New("upload stopped")) // unblock the producer if the upload failed
	err := <-prodErr

	// When the upload fails first, the producer only sees a broken pipe:
	// report the upload error then.
	var exitErr *docker.ExecError
	switch {
	case errors.As(err, &exitErr):
		return err
	case putErr != nil:
		return fmt.Errorf("upload: %w", putErr)
	case err != nil:
		return err
	}
	if sum.n == 0 {
		return errors.New("backup produced no output")
	}
	b.SizeBytes, b.SHA256, b.Encrypted = sum.n, hex.EncodeToString(sum.h.Sum(nil)), target.Encrypted()
	return nil
}

func (c *Core) serverVersion(ctx context.Context, svc store.Service, containerID string) (string, error) {
	var out strings.Builder
	err := c.docker.Exec(ctx, containerID, docker.ExecOptions{
		Cmd:    []string{"psql", "-U", svc.Env[pgUser], "-d", svc.Env[pgDatabase], "-tAc", "SHOW server_version"},
		Stdout: &out,
	})
	return strings.TrimSpace(out.String()), err
}

type hashCounter struct {
	h hash.Hash
	n int64
}

func (h *hashCounter) Write(p []byte) (int, error) {
	h.n += int64(len(p))
	return h.h.Write(p)
}

// RestoreBackup restores a backup into a postgres service (the backup's own
// database when targetServiceID is empty) in a single transaction. Apps
// linked to that database are stopped during the restore and started again
// afterwards.
// It overwrites data, so confirm must repeat the target service name.
func (c *Core) RestoreBackup(ctx context.Context, backupID, targetServiceID, confirm string) (store.Restore, error) {
	b, err := c.store.GetBackup(ctx, backupID)
	if err != nil {
		return store.Restore{}, err
	}
	if b.Status != store.OpSucceeded {
		return store.Restore{}, fmt.Errorf("%w: only successful backups can be restored", ErrInvalid)
	}
	if b.Kind != store.BackupKindPostgres {
		return store.Restore{}, fmt.Errorf("%w: manager backups are restored by replacing the data file (see README)", ErrInvalid)
	}
	if targetServiceID == "" {
		targetServiceID = b.ServiceID
	}
	svc, _, err := c.postgresService(ctx, targetServiceID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Restore{}, fmt.Errorf("%w: the backup's database no longer exists; pick another database", ErrInvalid)
	}
	if err != nil {
		return store.Restore{}, err
	}
	if confirm != svc.Name {
		return store.Restore{}, fmt.Errorf("%w: restoring overwrites %s; confirm with its name", ErrInvalid, svc.Name)
	}
	if major := postgresMajor(svc.Image); major != 0 && pgMajorFromVersion(b.PGVersion) > major {
		return store.Restore{}, fmt.Errorf("%w: backup is from PostgreSQL %s, database runs %d", ErrInvalid, b.PGVersion, major)
	}
	target, err := c.store.GetBackupTarget(ctx, b.TargetID)
	if err != nil {
		return store.Restore{}, err
	}
	st, err := storage.Open(target, c.cfg.DataDir)
	if err != nil {
		return store.Restore{}, err
	}
	unlock, err := c.lockService(svc.ID)
	if err != nil {
		return store.Restore{}, err
	}
	ct, err := c.runningContainer(ctx, svc)
	if err != nil {
		unlock()
		return store.Restore{}, err
	}
	r, err := c.store.CreateRestore(ctx, store.Restore{BackupID: b.ID, ServiceID: svc.ID, Status: store.OpRunning})
	if err != nil {
		unlock()
		return store.Restore{}, err
	}

	if err := c.goBackground(func() {
		defer unlock()
		c.runRestore(svc, ct, st, target, b, r)
	}); err != nil {
		unlock()
		_ = c.store.FinishRestore(ctx, r.ID, store.OpFailed, err.Error())
		return store.Restore{}, err
	}
	return r, nil
}

func (c *Core) runRestore(svc store.Service, containerID string, st storage.Storage, target store.BackupTarget, b store.Backup, r store.Restore) {
	ctx, cancel := context.WithTimeout(c.bg, restoreTimeout)
	defer cancel()
	log := c.log.With("restore", r.ID, "backup", b.ID, "service", svc.Name)

	stopped, err := c.stopLinkedApps(ctx, svc)
	if err == nil {
		err = c.restore(ctx, svc, containerID, st, target, b)
	}
	// Bring apps back even if the restore failed: the transaction rolled back.
	for _, id := range stopped {
		if _, serr := c.docker.ContainerStart(context.WithoutCancel(ctx), id, client.ContainerStartOptions{}); serr != nil {
			log.Error("cannot restart linked app", "container", id, "err", serr)
		}
	}

	status, msg := store.OpSucceeded, ""
	if err != nil {
		status, msg = store.OpFailed, err.Error()
		if errors.Is(ctx.Err(), context.Canceled) {
			msg = "interrupted by manager shutdown"
		}
		log.Warn("restore failed", "err", err)
	} else {
		log.Info("restore succeeded")
	}
	if err := c.store.FinishRestore(context.WithoutCancel(ctx), r.ID, status, msg); err != nil {
		log.Error("cannot record restore result", "err", err)
	}
}

// restore loads the dump into a scratch database and swaps it in by rename,
// so the result is exactly the backup (objects created since are gone) and
// a failure leaves the live database untouched.
func (c *Core) restore(ctx context.Context, svc store.Service, containerID string, st storage.Storage, target store.BackupTarget, b store.Backup) error {
	rc, err := st.Get(ctx, b.ObjectKey)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	defer rc.Close()

	user, db := svc.Env[pgUser], svc.Env[pgDatabase]
	scratch, old := db+"__restore", db+"__pre_restore"
	admin := func(sql string) error {
		// Maintenance database: the target can't be renamed while connected to it.
		return c.docker.Exec(ctx, containerID, docker.ExecOptions{
			Cmd: []string{"psql", "-U", user, "-d", "postgres", "-v", "ON_ERROR_STOP=1", "-qc", sql},
		})
	}
	if err := admin(fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, pgIdent(scratch))); err != nil {
		return fmt.Errorf("clean scratch database: %w", err)
	}
	if err := admin(fmt.Sprintf(`CREATE DATABASE %s OWNER %s`, pgIdent(scratch), pgIdent(user))); err != nil {
		return fmt.Errorf("create scratch database: %w", err)
	}
	dropScratch := func() {
		_ = admin(fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, pgIdent(scratch)))
	}

	// The checksum covers the stored (possibly encrypted) bytes.
	sum := &hashCounter{h: sha256.New()}
	stored := io.TeeReader(rc, sum)
	var dump io.Reader = stored
	if b.Encrypted {
		if dump, err = decryptFrom(stored, target.AgeIdentity); err != nil {
			dropScratch()
			return err
		}
	}
	err = c.docker.Exec(ctx, containerID, docker.ExecOptions{
		Cmd: []string{
			"pg_restore", "-U", user, "-d", scratch,
			"--no-owner", "--no-privileges", "--exit-on-error",
		},
		Stdin: dump,
	})
	if err != nil {
		dropScratch()
		return fmt.Errorf("pg_restore: %w", err)
	}
	if _, err := io.Copy(io.Discard, stored); err != nil { // finish the checksum
		dropScratch()
		return fmt.Errorf("read backup: %w", err)
	}
	if got := hex.EncodeToString(sum.h.Sum(nil)); b.SHA256 != "" && got != b.SHA256 {
		dropScratch()
		return fmt.Errorf("checksum mismatch: backup is corrupted (got %s, want %s)", got, b.SHA256)
	}

	// Swap. Each statement runs on its own: DROP DATABASE can't run in the
	// implicit transaction of a multi-statement query. Remaining connections
	// (linked apps are stopped already) are cut so the rename can proceed.
	if err := admin(fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, pgIdent(old))); err != nil {
		dropScratch()
		return fmt.Errorf("swap databases: %w", err)
	}
	terminate := fmt.Sprintf(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = %s AND pid <> pg_backend_pid()`,
		pgLiteral(db))
	if err := admin(terminate); err != nil {
		dropScratch()
		return fmt.Errorf("disconnect clients: %w", err)
	}
	if err := admin(fmt.Sprintf(`ALTER DATABASE %s RENAME TO %s`, pgIdent(db), pgIdent(old))); err != nil {
		dropScratch()
		return fmt.Errorf("swap databases: %w", err)
	}
	if err := admin(fmt.Sprintf(`ALTER DATABASE %s RENAME TO %s`, pgIdent(scratch), pgIdent(db))); err != nil {
		// Put the original back rather than leave no database at all.
		if rerr := admin(fmt.Sprintf(`ALTER DATABASE %s RENAME TO %s`, pgIdent(old), pgIdent(db))); rerr != nil {
			return fmt.Errorf("swap databases: %w (and restoring the original name failed: %v; it is %s)", err, rerr, old)
		}
		dropScratch()
		return fmt.Errorf("swap databases: %w", err)
	}
	if err := admin(fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, pgIdent(old))); err != nil {
		c.log.Warn("cannot drop pre-restore database", "service", svc.Name, "err", err)
	}
	return nil
}

func pgLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// pgIdent quotes an identifier. Names come from generated credentials, but
// quote anyway.
func pgIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// stopLinkedApps stops running containers of apps using db and returns
// their IDs so they can be started again.
func (c *Core) stopLinkedApps(ctx context.Context, db store.Service) ([]string, error) {
	svcs, err := c.store.ListServices(ctx, db.ProjectID)
	if err != nil {
		return nil, err
	}
	var stopped []string
	for _, s := range svcs {
		if s.DatabaseID != db.ID {
			continue
		}
		cts, err := c.serviceContainers(ctx, s)
		if err != nil {
			return stopped, err
		}
		secs := int(stopTimeout.Seconds())
		for _, ct := range cts {
			if ct.State != container.StateRunning {
				continue
			}
			if _, err := c.docker.ContainerStop(ctx, ct.ID, client.ContainerStopOptions{Timeout: &secs}); err != nil {
				return stopped, err
			}
			stopped = append(stopped, ct.ID)
		}
	}
	return stopped, nil
}

func (c *Core) postgresService(ctx context.Context, id string) (store.Service, store.Project, error) {
	svc, err := c.store.GetService(ctx, id)
	if err != nil {
		return store.Service{}, store.Project{}, err
	}
	if svc.Kind != store.ServiceKindPostgres {
		return store.Service{}, store.Project{}, fmt.Errorf("%w: not a postgres service", ErrInvalid)
	}
	p, err := c.store.GetProject(ctx, svc.ProjectID)
	return svc, p, err
}

// runningContainer returns the ID of the service's running container.
func (c *Core) runningContainer(ctx context.Context, svc store.Service) (string, error) {
	cts, err := c.serviceContainers(ctx, svc)
	if err != nil {
		return "", err
	}
	for _, ct := range cts {
		if ct.State == container.StateRunning {
			return ct.ID, nil
		}
	}
	return "", fmt.Errorf("%w: %s is not running", ErrInvalid, svc.Name)
}

func pgMajorFromVersion(v string) int {
	n := 0
	for _, r := range v {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func (c *Core) ListBackups(ctx context.Context, f store.BackupFilter) ([]store.Backup, error) {
	if f.Limit == 0 {
		f.Limit = 100
	}
	return c.store.ListBackups(ctx, f)
}

func (c *Core) ListRestores(ctx context.Context, serviceID string) ([]store.Restore, error) {
	return c.store.ListRestores(ctx, serviceID, 20)
}

// OpenBackup streams a backup's content (for download).
func (c *Core) OpenBackup(ctx context.Context, id string) (store.Backup, io.ReadCloser, error) {
	b, err := c.store.GetBackup(ctx, id)
	if err != nil {
		return store.Backup{}, nil, err
	}
	if b.Status != store.OpSucceeded {
		return store.Backup{}, nil, fmt.Errorf("%w: backup did not succeed", ErrInvalid)
	}
	st, err := c.storageFor(ctx, b.TargetID)
	if err != nil {
		return store.Backup{}, nil, err
	}
	rc, err := st.Get(ctx, b.ObjectKey)
	return b, rc, err
}

// DeleteBackup removes the object and its record.
func (c *Core) DeleteBackup(ctx context.Context, id string) error {
	b, err := c.store.GetBackup(ctx, id)
	if err != nil {
		return err
	}
	if b.Status == store.OpRunning {
		return ErrBusy
	}
	st, err := c.storageFor(ctx, b.TargetID)
	if err != nil {
		return err
	}
	if err := st.Delete(ctx, b.ObjectKey); err != nil {
		return fmt.Errorf("delete backup object: %w", err)
	}
	return c.store.DeleteBackup(ctx, id)
}

func (c *Core) storageFor(ctx context.Context, targetID string) (storage.Storage, error) {
	t, err := c.store.GetBackupTarget(ctx, targetID)
	if err != nil {
		return nil, err
	}
	return storage.Open(t, c.cfg.DataDir)
}
