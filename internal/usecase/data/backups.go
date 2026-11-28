package data

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

	"github.com/distribution/reference"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/ids"
	"github.com/kipit-labs/kipitiny/internal/notify"
)

const (
	backupTimeout  = 6 * time.Hour
	RestoreTimeout = 6 * time.Hour
)

// BackupService starts a backup of a service to a target in the background:
// a pg_dump for PostgreSQL, the database's own dump tool for MySQL, MariaDB
// and MongoDB, an archive of its volumes for Redis and apps with volumes.
// Dumps run inside the database container, so the client always matches the
// server version and the manager needs no database tools.
//
// database picks which of a PostgreSQL instance's databases to dump; empty
// is the service's own.
func (c *Data) BackupService(ctx context.Context, serviceID, targetID, database string) (domain.Backup, error) {
	return c.startBackup(ctx, serviceID, targetID, database, "", nil)
}

// startBackup starts a backup; done, if set, is closed when it finishes.
// Backups made by a schedule trigger its retention on success.
func (c *Data) startBackup(ctx context.Context, serviceID, targetID, database, scheduleID string, done chan<- struct{}) (domain.Backup, error) {
	if targetID == "" {
		targetID = domain.LocalTargetID
	}
	svc, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return domain.Backup{}, err
	}
	kind, err := backupKind(svc)
	if err != nil {
		return domain.Backup{}, err
	}
	project, err := c.store.GetProject(ctx, svc.ProjectID)
	if err != nil {
		return domain.Backup{}, err
	}
	target, err := c.store.GetBackupTarget(ctx, targetID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Backup{}, fmt.Errorf("%w: unknown backup target", ErrInvalid)
	}
	if err != nil {
		return domain.Backup{}, err
	}
	st, err := c.openStorage(target)
	if err != nil {
		return domain.Backup{}, err
	}
	unlock, err := c.mgr.LockService(svc.ID)
	if err != nil {
		return domain.Backup{}, err
	}
	// Dumps run in the database; volumes are read by a helper, but must
	// exist (a never-deployed service has none yet).
	var ct string
	ext, dir := ".tar.gz", fmt.Sprintf("%s/%s/", project.Name, svc.Name)
	switch {
	case kind == domain.BackupKindPostgres:
		ext = ".dump"
		if ct, err = c.runningContainer(ctx, svc); err == nil {
			database, err = c.pgDatabaseOf(ctx, svc, ct, database)
		}
		if database != svc.Env[PGDatabase] {
			dir += database + "/"
		}
	case database != "":
		err = fmt.Errorf("%w: only a PostgreSQL service has databases to pick", ErrInvalid)
	case kind == domain.BackupKindDump:
		ext = DumpExt(svc.Kind)
		ct, err = c.runningContainer(ctx, svc)
	case svc.CurrentDeploymentID == "":
		err = fmt.Errorf("%w: %s has not been deployed yet", ErrInvalid, svc.Name)
	}
	if err != nil {
		unlock()
		return domain.Backup{}, err
	}

	created := time.Now().UTC()
	b, err := c.store.CreateBackup(ctx, domain.Backup{
		Kind:        kind,
		Volumes:     volumeNames(svc),
		Image:       svc.Image,
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
		Status:    domain.OpRunning,
	})
	if err != nil {
		unlock()
		return domain.Backup{}, err
	}

	if err := c.mgr.GoBackground(func() {
		if done != nil {
			defer close(done)
		}
		ok := c.runBackup(svc, ct, st, target, b)
		unlock()
		if ok && scheduleID != "" {
			c.afterScheduledBackup(scheduleID, b.ID)
		}
		if ok && seedable(b) && svc.Anonymize != nil {
			if err := c.RefreshAnonymized(c.bg, svc.ProjectID, false); err != nil {
				c.log.Warn("anonymized copy", "service", svc.Name, "err", err)
			}
		}
	}); err != nil {
		unlock()
		b.Status, b.Error = domain.OpFailed, err.Error()
		_ = c.store.FinishBackup(ctx, b)
		return domain.Backup{}, err
	}
	return b, nil
}

// objectKey is dir/<timestamp>-<suffix><ext>[.age]. The suffix keeps keys
// unique for backups started in the same second.
func objectKey(dir string, created time.Time, ext string, target domain.BackupTarget) string {
	key := dir + created.Format("20060102T150405Z") + "-" + strings.ToLower(ids.New()[16:]) + ext
	if target.Encrypted() {
		key += ".age"
	}
	return key
}

// BackupProject backs up every database and app with volumes of a project
// to one target. Failures to start are reported per service; the others
// still run.
func (c *Data) BackupProject(ctx context.Context, projectID, targetID string) ([]domain.Backup, error) {
	svcs, err := c.store.ListServices(ctx, projectID)
	if err != nil {
		return nil, err
	}
	var started []domain.Backup
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
func (c *Data) runBackup(svc domain.Service, containerID string, st Storage, target domain.BackupTarget, b domain.Backup) bool {
	ctx, cancel := context.WithTimeout(c.bg, backupTimeout)
	defer cancel()
	defer c.steps.Done(backupKey(b.ID))
	log := c.log.With("backup", b.ID, "service", svc.Name)
	start := time.Now()

	var err error
	switch b.Kind {
	case domain.BackupKindVolume:
		err = c.dumpVolumes(ctx, svc, st, target, &b)
	case domain.BackupKindDump:
		err = c.dumpDatabase(ctx, svc, containerID, st, target, &b)
	default:
		err = c.dump(ctx, svc, containerID, st, target, &b)
	}
	b.DurationMS = time.Since(start).Milliseconds()
	b.Status = domain.OpSucceeded
	if err != nil {
		b.Status, b.Error = domain.OpFailed, err.Error()
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
		// Without it the backup is fine, only not adoptable by another manager.
		if err := c.writeSidecar(ctx, st, target, b); err != nil {
			log.Warn("cannot write backup sidecar", "err", err)
		}
	}
	if err := c.store.FinishBackup(context.WithoutCancel(ctx), b); err != nil {
		log.Error("cannot record backup result", "err", err)
		return false
	}
	c.notifyBackup(b, target, "/services/"+svc.ID)
	return err == nil && b.Status == domain.OpSucceeded
}

// dump streams pg_dump output to storage, filling in size, checksum and
// server version on b.
func (c *Data) dump(ctx context.Context, svc domain.Service, containerID string, st Storage, target domain.BackupTarget, b *domain.Backup) error {
	c.steps.Set(backupKey(b.ID), BackupPrepare)
	version, err := c.serverVersion(ctx, svc, containerID)
	if err != nil {
		return fmt.Errorf("server version: %w", err)
	}
	b.PGVersion = version
	if b.Extensions, err = c.pgExtensions(ctx, svc, containerID, backupDatabase(svc, *b)); err != nil {
		return fmt.Errorf("extensions: %w", err)
	}
	return c.upload(ctx, st, target, b, func(w io.Writer) error {
		err := c.mgr.DockerFor(svc.ServerID).Exec(ctx, containerID, domain.ExecOptions{
			// Custom format: compressed, and pg_restore can restore selectively.
			Cmd:    []string{"pg_dump", "-U", svc.Env[PGUser], "-d", backupDatabase(svc, *b), "-Fc"},
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
func (c *Data) upload(ctx context.Context, st Storage, target domain.BackupTarget, b *domain.Backup,
	produce func(w io.Writer) error) error {

	pr, pw := io.Pipe()
	prodErr := make(chan error, 1)
	go func() {
		err := func() error {
			var w io.Writer = pw
			if target.Encrypted() {
				enc, err := c.encryptTo(pw, target.AgeRecipient)
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
	step := c.steps.Set(backupKey(b.ID), BackupUpload)
	putErr := st.Put(ctx, b.ObjectKey, io.TeeReader(pr, io.MultiWriter(sum, step)))
	pr.CloseWithError(errors.New("upload stopped")) // unblock the producer if the upload failed
	err := <-prodErr

	// When the upload fails first, the producer only sees a broken pipe:
	// report the upload error then.
	var exitErr *domain.ExecError
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

type hashCounter struct {
	h hash.Hash
	n int64
}

func (h *hashCounter) Write(p []byte) (int, error) {
	h.n += int64(len(p))
	return h.h.Write(p)
}

// notifyBackup reports a finished database or manager backup.
func (c *Data) notifyBackup(b domain.Backup, target domain.BackupTarget, path string) {
	subject := b.ProjectName + "/" + b.ServiceName
	if b.Kind == domain.BackupKindManager {
		subject, path = "Manager state", "/backups"
	}
	e := notify.Event{
		Type:  domain.EventBackupSucceeded,
		Level: notify.Success,
		Title: subject + " backed up",
		Fields: []notify.Field{
			{Name: "Target", Value: target.Name},
			{Name: "Size", Value: humanSize(b.SizeBytes)},
			{Name: "Duration", Value: (time.Duration(b.DurationMS) * time.Millisecond).Round(time.Second).String()},
		},
	}
	if b.Status != domain.OpSucceeded {
		e.Type, e.Level, e.Title, e.Message = domain.EventBackupFailed, notify.Error, subject+" backup failed", b.Error
		e.Fields = e.Fields[:1]
	}
	if b.Database != "" {
		e.Fields = append([]notify.Field{{Name: "Database", Value: b.Database}}, e.Fields...)
	}
	c.mgr.Notify(e, path, "")
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

func (c *Data) ListBackups(ctx context.Context, f domain.BackupFilter) ([]domain.Backup, error) {
	if f.Limit == 0 {
		f.Limit = 100
	}
	if f.ProjectID != "" && !identity.Visible(ctx, f.ProjectID) {
		return []domain.Backup{}, nil
	}
	f.ProjectIn = identity.VisibleProjects(ctx)
	bs, err := c.store.ListBackups(ctx, f)
	for i := range bs {
		c.fillSteps(&bs[i])
	}
	return bs, err
}

func (c *Data) GetBackup(ctx context.Context, id string) (domain.Backup, error) {
	b, err := c.store.GetBackup(ctx, id)
	if err == nil {
		c.fillSteps(&b)
	}
	return b, err
}

func (c *Data) ListRestores(ctx context.Context, serviceID string) ([]domain.Restore, error) {
	rs, err := c.store.ListRestores(ctx, serviceID, 20)
	for i := range rs {
		if rs[i].Status == domain.OpRunning {
			rs[i].Step, _ = c.steps.Get(restoreKey(rs[i].ID))
		}
	}
	return rs, err
}

// fillSteps adds where b's running backup or restore test is at.
func (c *Data) fillSteps(b *domain.Backup) {
	if b.Status == domain.OpRunning {
		b.Step, b.StepBytes = c.steps.Get(backupKey(b.ID))
	}
	if b.VerifyStatus == domain.OpRunning {
		b.VerifyStep, _ = c.steps.Get(verifyKey(b.ID))
	}
}

// OpenBackup streams a backup's content (for download).
func (c *Data) OpenBackup(ctx context.Context, id string) (domain.Backup, io.ReadCloser, error) {
	b, err := c.store.GetBackup(ctx, id)
	if err != nil {
		return domain.Backup{}, nil, err
	}
	if b.Status != domain.OpSucceeded {
		return domain.Backup{}, nil, fmt.Errorf("%w: backup did not succeed", ErrInvalid)
	}
	st, err := c.storageFor(ctx, b.TargetID)
	if err != nil {
		return domain.Backup{}, nil, err
	}
	rc, err := st.Get(ctx, b.ObjectKey)
	return b, rc, err
}

// DeleteBackup removes the object and its record, with its anonymized
// copies.
func (c *Data) DeleteBackup(ctx context.Context, id string) error {
	b, err := c.store.GetBackup(ctx, id)
	if err != nil {
		return err
	}
	if b.Status == domain.OpRunning {
		return ErrBusy
	}
	copies, err := c.store.ListBackups(ctx, domain.BackupFilter{Anonymized: true, SourceID: id})
	if err != nil {
		return err
	}
	for _, cp := range copies {
		if err := c.DeleteBackup(ctx, cp.ID); err != nil {
			return fmt.Errorf("anonymized copy: %w", err)
		}
	}
	st, err := c.storageFor(ctx, b.TargetID)
	if err != nil {
		return err
	}
	if err := st.Delete(ctx, b.ObjectKey); err != nil {
		return fmt.Errorf("delete backup object: %w", err)
	}
	if b.Kind != domain.BackupKindManager {
		if err := st.Delete(ctx, b.ObjectKey+sidecarExt); err != nil {
			return fmt.Errorf("delete backup sidecar: %w", err)
		}
	}
	return c.store.DeleteBackup(ctx, id)
}

func (c *Data) storageFor(ctx context.Context, targetID string) (Storage, error) {
	t, err := c.store.GetBackupTarget(ctx, targetID)
	if err != nil {
		return nil, err
	}
	return c.openStorage(t)
}

// IconHint names the logo for svc: its icon, else its image's base name
// (ghcr.io/n8n-io/n8n:1 → n8n). The UI shows the logo if it knows the name,
// else one for the kind. Backups keep it, as they outlive the service.
func IconHint(svc domain.Service) string {
	if svc.Icon != "" {
		return svc.Icon
	}
	named, err := reference.ParseNormalizedNamed(svc.Image)
	if err != nil {
		return ""
	}
	p := reference.Path(named)
	return p[strings.LastIndex(p, "/")+1:]
}
