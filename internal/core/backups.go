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
	"github.com/MatHoyer/kipitiny/internal/notify"
	"github.com/MatHoyer/kipitiny/internal/storage"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	backupTimeout  = 6 * time.Hour
	restoreTimeout = 6 * time.Hour
)

// BackupService starts a backup of a service to a target in the background:
// a pg_dump for PostgreSQL, the database's own dump tool for MySQL, MariaDB
// and MongoDB, an archive of its volumes for Redis and apps with volumes.
// Dumps run inside the database container, so the client always matches the
// server version and the manager needs no database tools.
//
// database picks which of a PostgreSQL instance's databases to dump; empty
// is the service's own.
func (c *Core) BackupService(ctx context.Context, serviceID, targetID, database string) (store.Backup, error) {
	return c.startBackup(ctx, serviceID, targetID, database, "", nil)
}

// startBackup starts a backup; done, if set, is closed when it finishes.
// Backups made by a schedule trigger its retention on success.
func (c *Core) startBackup(ctx context.Context, serviceID, targetID, database, scheduleID string, done chan<- struct{}) (store.Backup, error) {
	if targetID == "" {
		targetID = store.LocalTargetID
	}
	svc, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return store.Backup{}, err
	}
	kind, err := backupKind(svc)
	if err != nil {
		return store.Backup{}, err
	}
	project, err := c.store.GetProject(ctx, svc.ProjectID)
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
	st, err := c.openStorage(target)
	if err != nil {
		return store.Backup{}, err
	}
	unlock, err := c.lockService(svc.ID)
	if err != nil {
		return store.Backup{}, err
	}
	// Dumps run in the database; volumes are read by a helper, but must
	// exist (a never-deployed service has none yet).
	var ct string
	ext, dir := ".tar.gz", fmt.Sprintf("%s/%s/", project.Name, svc.Name)
	switch {
	case kind == store.BackupKindPostgres:
		ext = ".dump"
		if ct, err = c.runningContainer(ctx, svc); err == nil {
			database, err = c.pgDatabaseOf(ctx, svc, ct, database)
		}
		if database != svc.Env[pgDatabase] {
			dir += database + "/"
		}
	case database != "":
		err = fmt.Errorf("%w: only a PostgreSQL service has databases to pick", ErrInvalid)
	case kind == store.BackupKindDump:
		ext = dumpExt(svc.Kind)
		ct, err = c.runningContainer(ctx, svc)
	case svc.CurrentDeploymentID == "":
		err = fmt.Errorf("%w: %s has not been deployed yet", ErrInvalid, svc.Name)
	}
	if err != nil {
		unlock()
		return store.Backup{}, err
	}

	created := time.Now().UTC()
	b, err := c.store.CreateBackup(ctx, store.Backup{
		Kind:        kind,
		Volumes:     volumeNames(svc),
		ServiceID:   svc.ID,
		ProjectID:   project.ID,
		ServiceName: svc.Name,
		ServiceKind: svc.Kind,
		ServiceIcon: IconHint(svc),
		ProjectName: project.Name,
		TargetID:    target.ID,
		ScheduleID:  scheduleID,
		Database:    database,
		// The suffix keeps keys unique for backups started in the same second.
		ObjectKey: objectKey(dir, created, ext, target),
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
			c.afterScheduledBackup(scheduleID, b.ID)
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

// BackupProject backs up every database and app with volumes of a project
// to one target. Failures to start are reported per service; the others
// still run.
func (c *Core) BackupProject(ctx context.Context, projectID, targetID string) ([]store.Backup, error) {
	svcs, err := c.store.ListServices(ctx, projectID)
	if err != nil {
		return nil, err
	}
	var started []store.Backup
	var errs []error
	for _, s := range svcs {
		if _, err := backupKind(s); err != nil {
			continue
		}
		b, err := c.BackupService(ctx, s.ID, targetID, "")
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.Name, err))
			continue
		}
		started = append(started, b)
	}
	if len(started) == 0 && len(errs) == 0 {
		return nil, fmt.Errorf("%w: the project has no databases or volumes", ErrInvalid)
	}
	return started, errors.Join(errs...)
}

// runBackup performs the backup and records the outcome; it reports success.
func (c *Core) runBackup(svc store.Service, containerID string, st storage.Storage, target store.BackupTarget, b store.Backup) bool {
	ctx, cancel := context.WithTimeout(c.bg, backupTimeout)
	defer cancel()
	log := c.log.With("backup", b.ID, "service", svc.Name)
	start := time.Now()

	var err error
	switch b.Kind {
	case store.BackupKindVolume:
		err = c.dumpVolumes(ctx, svc, st, target, &b)
	case store.BackupKindDump:
		err = c.dumpDatabase(ctx, svc, containerID, st, target, &b)
	default:
		err = c.dump(ctx, svc, containerID, st, target, &b)
	}
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
	c.notifyBackup(b, target, "/services/"+svc.ID)
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
		err := c.dockerFor(svc.ServerID).Exec(ctx, containerID, docker.ExecOptions{
			// Custom format: compressed, and pg_restore can restore selectively.
			Cmd:    []string{"pg_dump", "-U", svc.Env[pgUser], "-d", backupDatabase(svc, *b), "-Fc"},
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
	err := c.dockerFor(svc.ServerID).Exec(ctx, containerID, docker.ExecOptions{
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

// RestoreBackup restores a backup into a service (the backup's own when
// targetServiceID is empty). A database dump is loaded aside and swapped in,
// with the apps linked to the database stopped meanwhile; a volume archive is
// extracted aside, then copied over the volumes with the service stopped.
// It overwrites data, so confirm must repeat the target service name.
func (c *Core) RestoreBackup(ctx context.Context, backupID, targetServiceID, confirm string) (store.Restore, error) {
	b, err := c.store.GetBackup(ctx, backupID)
	if err != nil {
		return store.Restore{}, err
	}
	if b.Status != store.OpSucceeded {
		return store.Restore{}, fmt.Errorf("%w: only successful backups can be restored", ErrInvalid)
	}
	if b.Kind == store.BackupKindManager {
		return store.Restore{}, fmt.Errorf("%w: manager backups are restored by replacing the data file (see README)", ErrInvalid)
	}
	if targetServiceID == "" {
		targetServiceID = b.ServiceID
	}
	svc, err := c.store.GetService(ctx, targetServiceID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Restore{}, fmt.Errorf("%w: the backup's service no longer exists; pick another one", ErrInvalid)
	}
	if err != nil {
		return store.Restore{}, err
	}
	if b.Kind == store.BackupKindVolume {
		return c.restoreVolumeBackup(ctx, b, svc, confirm)
	}
	switch {
	case b.Kind == store.BackupKindDump && svc.Kind != b.ServiceKind:
		return store.Restore{}, fmt.Errorf("%w: a %s dump restores into a %s service", ErrInvalid, b.ServiceKind, b.ServiceKind)
	case b.Kind == store.BackupKindPostgres && svc.Kind != store.ServiceKindPostgres:
		return store.Restore{}, fmt.Errorf("%w: a database dump restores into a PostgreSQL service", ErrInvalid)
	}
	if confirm != svc.Name {
		return store.Restore{}, fmt.Errorf("%w: restoring overwrites %s; confirm with its name", ErrInvalid, svc.Name)
	}
	if major := postgresMajor(svc.Image); b.Kind == store.BackupKindPostgres && major != 0 && pgMajorFromVersion(b.PGVersion) > major {
		return store.Restore{}, fmt.Errorf("%w: backup is from PostgreSQL %s, database runs %d", ErrInvalid, b.PGVersion, major)
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

	stopped, err := c.stopLinkedApps(ctx, svc)
	if err == nil && b.Kind == store.BackupKindDump {
		err = c.restoreDump(ctx, svc, containerID, st, target, b)
	} else if err == nil {
		err = c.restore(ctx, svc, containerID, st, target, b)
	}
	// Bring apps back even if the restore failed: the transaction rolled back.
	for _, id := range stopped {
		if _, serr := c.dockerFor(svc.ServerID).ContainerStart(context.WithoutCancel(ctx), id, client.ContainerStartOptions{}); serr != nil {
			c.log.Error("cannot restart app using the database", "container", id, "err", serr)
		}
	}
	failed := "The live database was left unchanged."
	switch {
	case b.Kind == store.BackupKindDump && svc.Kind == store.ServiceKindMongoDB:
		failed = "Collections the backup holds may be partly restored."
	case b.Kind == store.BackupKindDump:
		failed = "Unless the error says otherwise, the live database was left unchanged."
	}
	c.finishRestore(ctx, svc, target, b, r, err, "The database now holds", failed)
}

// finishRestore records and reports a restore's outcome. done starts the
// success message ("<done> the backup of <date>."); failed ends the failure one.
func (c *Core) finishRestore(ctx context.Context, svc store.Service, target store.BackupTarget, b store.Backup, r store.Restore,
	err error, done, failed string) {

	log := c.log.With("restore", r.ID, "backup", b.ID, "service", svc.Name)
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

	e := notify.Event{
		Type:    EventRestoreSucceeded,
		Level:   notify.Success,
		Title:   fmt.Sprintf("%s/%s restored", b.ProjectName, svc.Name),
		Message: fmt.Sprintf("%s the backup of %s.", done, b.CreatedAt.UTC().Format("2006-01-02 15:04 UTC")),
		Fields: []notify.Field{
			{Name: "Project", Value: b.ProjectName},
			{Name: "Service", Value: svc.Name},
			{Name: "Target", Value: target.Name},
		},
	}
	if status == store.OpFailed {
		e.Type, e.Level, e.Title, e.Message = EventRestoreFailed, notify.Error, fmt.Sprintf("%s/%s restore failed", b.ProjectName, svc.Name), msg+"\n"+failed
	}
	c.notify(e, "/services/"+svc.ID, "")
}

// notifyBackup reports a finished database or manager backup.
func (c *Core) notifyBackup(b store.Backup, target store.BackupTarget, path string) {
	subject := b.ProjectName + "/" + b.ServiceName
	if b.Kind == store.BackupKindManager {
		subject, path = "Manager state", "/backups"
	}
	e := notify.Event{
		Type:  EventBackupSucceeded,
		Level: notify.Success,
		Title: subject + " backed up",
		Fields: []notify.Field{
			{Name: "Target", Value: target.Name},
			{Name: "Size", Value: humanSize(b.SizeBytes)},
			{Name: "Duration", Value: (time.Duration(b.DurationMS) * time.Millisecond).Round(time.Second).String()},
		},
	}
	if b.Status != store.OpSucceeded {
		e.Type, e.Level, e.Title, e.Message = EventBackupFailed, notify.Error, subject+" backup failed", b.Error
		e.Fields = e.Fields[:1]
	}
	if b.Database != "" {
		e.Fields = append([]notify.Field{{Name: "Database", Value: b.Database}}, e.Fields...)
	}
	c.notify(e, path, "")
}

// humanSize formats a byte count with binary units.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
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

	user, db := svc.Env[pgUser], backupDatabase(svc, b)
	scratch, old := db+"__restore", db+"__pre_restore"
	dk := c.dockerFor(svc.ServerID)
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

// backupDatabase is the database a backup holds; older backups predate the
// choice and hold the service's own.
func backupDatabase(svc store.Service, b store.Backup) string {
	if b.Database != "" {
		return b.Database
	}
	return svc.Env[pgDatabase]
}

// pgDatabaseOf checks that name is a database of svc's instance (its
// container ct) and returns it; empty is the service's own. Names come from
// callers: psql and pg_dump would take a connection string as well.
func (c *Core) pgDatabaseOf(ctx context.Context, svc store.Service, ct, name string) (string, error) {
	main := svc.Env[pgDatabase]
	if name == "" || name == main {
		return main, nil
	}
	t := dataTarget{svc: svc, dk: c.dockerFor(svc.ServerID), container: ct, db: main}
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
func (c *Core) pgRestore(ctx context.Context, dk *docker.Client, containerID, user, db string, rc io.Reader, target store.BackupTarget, b store.Backup) error {
	return fromStorage(rc, target, b, func(dump io.Reader) error {
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
func fromStorage(rc io.Reader, target store.BackupTarget, b store.Backup, consume func(io.Reader) error) error {
	sum := &hashCounter{h: sha256.New()}
	stored := io.TeeReader(rc, sum)
	var content io.Reader = stored
	if b.Encrypted {
		var err error
		if content, err = decryptFrom(stored, target.AgeIdentity); err != nil {
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
func (c *Core) stopLinkedApps(ctx context.Context, db store.Service) ([]string, error) {
	svcs, err := c.store.ListServices(ctx, db.ProjectID)
	if err != nil {
		return nil, err
	}
	var stopped []string
	for _, s := range svcs {
		if s.ID == db.ID || !usesDatabase(s.Env, db.Name) {
			continue
		}
		cts, err := c.serviceContainers(ctx, s)
		if err != nil {
			return stopped, err
		}
		secs := int(stopTimeoutFor(s).Seconds())
		for _, ct := range cts {
			if ct.State != container.StateRunning {
				continue
			}
			if _, err := c.dockerFor(db.ServerID).ContainerStop(ctx, ct.ID, client.ContainerStopOptions{Timeout: &secs}); err != nil {
				return stopped, err
			}
			stopped = append(stopped, ct.ID)
		}
	}
	return stopped, nil
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

func (c *Core) GetBackup(ctx context.Context, id string) (store.Backup, error) {
	return c.store.GetBackup(ctx, id)
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
	return c.openStorage(t)
}
