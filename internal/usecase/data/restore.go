package data

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/moby/moby/client"

	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/notify"
	"github.com/kipit-labs/kipitiny/internal/storage"
)

// RestoreBackup restores a backup into a service (the backup's own when
// targetServiceID is empty). A database dump is loaded aside and swapped in,
// with the apps linked to the database stopped meanwhile; a volume archive is
// extracted aside, then copied over the volumes with the service stopped.
// It overwrites data, so confirm must repeat the target service name.
func (c *Data) RestoreBackup(ctx context.Context, backupID, targetServiceID, confirm string) (domain.Restore, error) {
	b, err := c.store.GetBackup(ctx, backupID)
	if err != nil {
		return domain.Restore{}, err
	}
	if b.Status != domain.OpSucceeded {
		return domain.Restore{}, fmt.Errorf("%w: only successful backups can be restored", ErrInvalid)
	}
	if b.Kind == domain.BackupKindManager {
		return domain.Restore{}, fmt.Errorf("%w: manager backups are restored by replacing the data file (see README)", ErrInvalid)
	}
	if targetServiceID == "" {
		targetServiceID = b.ServiceID
	}
	svc, err := c.store.GetService(ctx, targetServiceID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Restore{}, fmt.Errorf("%w: the backup's service no longer exists; pick another one", ErrInvalid)
	}
	if err != nil {
		return domain.Restore{}, err
	}
	// Restoring reads the backup's project and overwrites the target's.
	if err := identity.Check(ctx, b.ProjectID, identity.PermRestore); err != nil {
		return domain.Restore{}, err
	}
	if err := identity.Check(ctx, svc.ProjectID, identity.PermRestore); err != nil {
		return domain.Restore{}, err
	}
	if b.Kind == domain.BackupKindVolume {
		return c.restoreVolumeBackup(ctx, b, svc, confirm)
	}
	switch {
	case b.Kind == domain.BackupKindDump && svc.Kind != b.ServiceKind:
		return domain.Restore{}, fmt.Errorf("%w: a %s dump restores into a %s service", ErrInvalid, b.ServiceKind, b.ServiceKind)
	case b.Kind == domain.BackupKindPostgres && svc.Kind != domain.ServiceKindPostgres:
		return domain.Restore{}, fmt.Errorf("%w: a database dump restores into a PostgreSQL service", ErrInvalid)
	}
	if confirm != svc.Name {
		return domain.Restore{}, fmt.Errorf("%w: restoring overwrites %s; confirm with its name", ErrInvalid, svc.Name)
	}
	if major := postgresMajor(svc.Image); b.Kind == domain.BackupKindPostgres && major != 0 && pgMajorFromVersion(b.PGVersion) > major {
		return domain.Restore{}, fmt.Errorf("%w: backup is from PostgreSQL %s, database runs %d", ErrInvalid, b.PGVersion, major)
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
	ct, err := c.runningContainer(ctx, svc)
	if err != nil {
		unlock()
		return domain.Restore{}, err
	}
	if b.Kind == domain.BackupKindPostgres && len(b.Extensions) > 0 {
		// Checked up front: pg_restore would fail on the first CREATE EXTENSION.
		missing, err := c.missingExtensions(ctx, svc, ct, b.Extensions)
		if err == nil && len(missing) > 0 {
			err = fmt.Errorf("%w: the backup uses the %s extension, which %s's image (%s) doesn't have; switch it to an image that does",
				ErrInvalid, strings.Join(missing, ", "), svc.Name, svc.Image)
		}
		if err != nil {
			unlock()
			return domain.Restore{}, err
		}
	}
	r, err := c.store.CreateRestore(ctx, domain.Restore{BackupID: b.ID, ServiceID: svc.ID, Status: domain.OpRunning})
	if err != nil {
		unlock()
		return domain.Restore{}, err
	}

	if err := c.mgr.GoBackground(func() {
		defer unlock()
		c.runRestore(svc, ct, st, target, b, r)
	}); err != nil {
		unlock()
		_ = c.store.FinishRestore(ctx, r.ID, domain.OpFailed, err.Error())
		return domain.Restore{}, err
	}
	return r, nil
}

func (c *Data) runRestore(svc domain.Service, containerID string, st storage.Storage, target domain.BackupTarget, b domain.Backup, r domain.Restore) {
	ctx, cancel := context.WithTimeout(c.bg, RestoreTimeout)
	defer cancel()
	key := restoreKey(r.ID)
	defer c.steps.Done(key)

	c.steps.Set(key, RestoreStopApps)
	stopped, err := c.stopLinkedApps(ctx, svc)
	if err == nil && b.Kind == domain.BackupKindDump {
		err = c.restoreDump(ctx, svc, containerID, st, target, b, key)
	} else if err == nil {
		err = c.restore(ctx, svc, containerID, st, target, b, key)
	}
	// Bring apps back even if the restore failed: the transaction rolled back.
	if len(stopped) > 0 {
		c.steps.Set(key, RestoreStart)
	}
	for _, id := range stopped {
		if _, serr := c.mgr.DockerFor(svc.ServerID).ContainerStart(context.WithoutCancel(ctx), id, client.ContainerStartOptions{}); serr != nil {
			c.log.Error("cannot restart app using the database", "container", id, "err", serr)
		}
	}
	failed := "The live database was left unchanged."
	switch {
	case b.Kind == domain.BackupKindDump && svc.Kind == domain.ServiceKindMongoDB:
		failed = "Collections the backup holds may be partly restored."
	case b.Kind == domain.BackupKindDump:
		failed = "Unless the error says otherwise, the live database was left unchanged."
	}
	c.finishRestore(ctx, svc, target, b, r, err, "The database now holds", failed)
}

// finishRestore records and reports a restore's outcome. done starts the
// success message ("<done> the backup of <date>."); failed ends the failure one.
func (c *Data) finishRestore(ctx context.Context, svc domain.Service, target domain.BackupTarget, b domain.Backup, r domain.Restore,
	err error, done, failed string) {

	log := c.log.With("restore", r.ID, "backup", b.ID, "service", svc.Name)
	status, msg := domain.OpSucceeded, ""
	if err != nil {
		status, msg = domain.OpFailed, err.Error()
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

	e := notify.Event{
		Type:    domain.EventRestoreSucceeded,
		Level:   notify.Success,
		Title:   fmt.Sprintf("%s/%s restored", b.ProjectName, svc.Name),
		Message: fmt.Sprintf("%s the backup of %s.", done, b.CreatedAt.UTC().Format("2006-01-02 15:04 UTC")),
		Fields: []notify.Field{
			{Name: "Project", Value: b.ProjectName},
			{Name: "Service", Value: svc.Name},
			{Name: "Target", Value: target.Name},
		},
	}
	if status == domain.OpFailed {
		e.Type, e.Level, e.Title, e.Message = domain.EventRestoreFailed, notify.Error, fmt.Sprintf("%s/%s restore failed", b.ProjectName, svc.Name), msg+"\n"+failed
	}
	c.mgr.Notify(e, "/services/"+svc.ID, "")
}

// restore loads the dump into a scratch database and swaps it in by rename,
// so the result is exactly the backup (objects created since are gone) and
// a failure leaves the live database untouched.
// key names the restore, for its steps.
func (c *Data) restore(ctx context.Context, svc domain.Service, containerID string, st storage.Storage, target domain.BackupTarget, b domain.Backup, key string) error {
	c.steps.Set(key, RestoreLoad)
	rc, err := st.Get(ctx, b.ObjectKey)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	defer rc.Close()

	user, db := svc.Env[PGUser], backupDatabase(svc, b)
	scratch, old := db+"__restore", db+"__pre_restore"
	dk := c.mgr.DockerFor(svc.ServerID)
	// Run from a maintenance database: the target can't be renamed while
	// connected to it. That's postgres, unless postgres is the target.
	maintenance := "postgres"
	if db == maintenance {
		maintenance = "template1"
	}
	admin := func(sql string) error {
		return dk.Exec(ctx, containerID, docker.ExecOptions{
			Cmd: []string{"psql", "-U", user, "-d", maintenance, "-v", "ON_ERROR_STOP=1", "-qc", sql},
		})
	}
	if err := admin(fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, pgIdent(scratch))); err != nil {
		return fmt.Errorf("clean scratch database: %w", err)
	}
	// From template0: copying template1 fails while we're connected to it.
	if err := admin(fmt.Sprintf(`CREATE DATABASE %s OWNER %s TEMPLATE template0`, pgIdent(scratch), pgIdent(user))); err != nil {
		return fmt.Errorf("create scratch database: %w", err)
	}
	dropScratch := func() {
		_ = admin(fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, pgIdent(scratch)))
	}

	if err := c.pgRestore(ctx, dk, containerID, user, scratch, rc, target, b); err != nil {
		dropScratch()
		return err
	}

	// A database restored into an instance that lacks it is just renamed in.
	c.steps.Set(key, RestoreSwap)
	var exists strings.Builder
	err = dk.Exec(ctx, containerID, docker.ExecOptions{
		Cmd:    []string{"psql", "-U", user, "-d", maintenance, "-tAc", "SELECT 1 FROM pg_database WHERE datname = " + pgLiteral(db)},
		Stdout: &exists,
	})
	if err != nil {
		dropScratch()
		return fmt.Errorf("look up %s: %w", db, err)
	}
	if strings.TrimSpace(exists.String()) == "" {
		if err := admin(fmt.Sprintf(`ALTER DATABASE %s RENAME TO %s`, pgIdent(scratch), pgIdent(db))); err != nil {
			dropScratch()
			return fmt.Errorf("swap databases: %w", err)
		}
		return nil
	}

	// Swap. Each statement runs on its own: DROP DATABASE can't run in the
	// implicit transaction of a multi-statement query. Remaining connections
	// (apps using it are stopped already) are cut so the rename can proceed.
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
			return fmt.Errorf("swap databases: %w (and restoring the original name failed: %s; it is %s)", err, rerr.Error(), old)
		}
		dropScratch()
		return fmt.Errorf("swap databases: %w", err)
	}
	if err := admin(fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, pgIdent(old))); err != nil {
		c.log.Warn("cannot drop pre-restore database", "service", svc.Name, "err", err)
	}
	return nil
}

// backupDatabase is the database a backup holds; older backups predate the
// choice and hold the service's own.
func backupDatabase(svc domain.Service, b domain.Backup) string {
	if b.Database != "" {
		return b.Database
	}
	return svc.Env[PGDatabase]
}

// pgDatabaseOf checks that name is a database of svc's instance (its
// container ct) and returns it; empty is the service's own. Names come from
// callers: psql and pg_dump would take a connection string as well.
func (c *Data) pgDatabaseOf(ctx context.Context, svc domain.Service, ct, name string) (string, error) {
	main := svc.Env[PGDatabase]
	if name == "" || name == main {
		return main, nil
	}
	t := dataTarget{svc: svc, dk: c.mgr.DockerFor(svc.ServerID), container: ct, db: main}
	dbs, err := t.pgDatabases(ctx)
	if err != nil {
		return "", err
	}
	for _, d := range dbs {
		if d.Name == name {
			return name, nil
		}
	}
	return "", fmt.Errorf("%w: %s has no database %q", ErrInvalid, svc.Name, name)
}

func pgLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// pgRestore streams a stored backup (decrypting if needed) into pg_restore
// against database db, then verifies the checksum of the stored bytes.
func (c *Data) pgRestore(ctx context.Context, dk *docker.Client, containerID, user, db string, rc io.Reader, target domain.BackupTarget, b domain.Backup) error {
	return c.fromStorage(rc, target, b, func(dump io.Reader) error {
		err := dk.Exec(ctx, containerID, docker.ExecOptions{
			Cmd: []string{
				"pg_restore", "-U", user, "-d", db,
				"--no-owner", "--no-privileges", "--exit-on-error",
			},
			Stdin: dump,
		})
		if err != nil {
			return fmt.Errorf("pg_restore: %w", err)
		}
		return nil
	})
}

// fromStorage feeds a stored backup (decrypted if needed) to consume, then
// checks the checksum of the stored bytes.
func (c *Data) fromStorage(rc io.Reader, target domain.BackupTarget, b domain.Backup, consume func(io.Reader) error) error {
	sum := &hashCounter{h: sha256.New()}
	stored := io.TeeReader(rc, sum)
	content := stored
	if b.Encrypted {
		var err error
		if content, err = c.decryptFrom(stored, target.AgeIdentity); err != nil {
			return err
		}
	}
	if err := consume(content); err != nil {
		return err
	}
	if _, err := io.Copy(io.Discard, stored); err != nil { // finish the checksum
		return fmt.Errorf("read backup: %w", err)
	}
	if got := hex.EncodeToString(sum.h.Sum(nil)); b.SHA256 != "" && got != b.SHA256 {
		return fmt.Errorf("checksum mismatch: backup is corrupted (got %s, want %s)", got, b.SHA256)
	}
	return nil
}

// pgIdent quotes an identifier. Names come from generated credentials, but
// quote anyway.
func pgIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// stopLinkedApps stops running containers of apps whose env references db
// and returns their IDs so they can be started again.
func (c *Data) stopLinkedApps(ctx context.Context, db domain.Service) ([]string, error) {
	svcs, err := c.store.ListServices(ctx, db.ProjectID)
	if err != nil {
		return nil, err
	}
	var stopped []string
	for _, s := range svcs {
		if s.ID == db.ID || !domain.UsesDatabase(s.Env, db.Name) {
			continue
		}
		cts, err := c.mgr.ServiceContainers(ctx, s)
		if err != nil {
			return stopped, err
		}
		secs := int(StopTimeoutFor(s).Seconds())
		for _, ct := range cts {
			if ct.State != domain.StateRunning {
				continue
			}
			if _, err := c.mgr.DockerFor(db.ServerID).ContainerStop(ctx, ct.ID, client.ContainerStopOptions{Timeout: &secs}); err != nil {
				return stopped, err
			}
			stopped = append(stopped, ct.ID)
		}
	}
	return stopped, nil
}

// runningContainer returns the ID of the service's running container.
// A sleeping project wakes first.
func (c *Data) runningContainer(ctx context.Context, svc domain.Service) (string, error) {
	if err := c.mgr.WakeFor(ctx, svc); err != nil {
		return "", err
	}
	cts, err := c.mgr.ServiceContainers(ctx, svc)
	if err != nil {
		return "", err
	}
	for _, ct := range cts {
		if ct.State == domain.StateRunning {
			return ct.ID, nil
		}
	}
	return "", fmt.Errorf("%w: %s is not running", ErrInvalid, svc.Name)
}
