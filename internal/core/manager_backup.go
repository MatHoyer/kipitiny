package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/MatHoyer/kipitiny/internal/storage"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// managerLockID serializes manager backups like a service.
const managerLockID = "manager"

// BackupManager snapshots the manager's own state (VACUUM INTO) and uploads
// it to a target in the background.
func (c *Core) BackupManager(ctx context.Context, targetID string) (store.Backup, error) {
	return c.startManagerBackup(ctx, targetID, nil)
}

func (c *Core) startManagerBackup(ctx context.Context, targetID string, done chan<- struct{}) (store.Backup, error) {
	if targetID == "" {
		targetID = store.LocalTargetID
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
	unlock, err := c.lockService(managerLockID)
	if err != nil {
		return store.Backup{}, err
	}
	b, err := c.store.CreateBackup(ctx, store.Backup{
		Kind:        store.BackupKindManager,
		ServiceName: "manager",
		TargetID:    target.ID,
		ObjectKey:   objectKey("_manager/", time.Now().UTC(), ".db", target),
		Status:      store.OpRunning,
	})
	if err != nil {
		unlock()
		return store.Backup{}, err
	}
	if err := c.goBackground(func() {
		defer unlock()
		if done != nil {
			defer close(done)
		}
		c.runManagerBackup(st, target, b)
	}); err != nil {
		unlock()
		b.Status, b.Error = store.OpFailed, err.Error()
		_ = c.store.FinishBackup(ctx, b)
		return store.Backup{}, err
	}
	return b, nil
}

func (c *Core) runManagerBackup(st storage.Storage, target store.BackupTarget, b store.Backup) {
	ctx, cancel := context.WithTimeout(c.bg, time.Hour)
	defer cancel()
	start := time.Now()

	err := c.snapshotTo(ctx, st, target, &b)
	b.DurationMS = time.Since(start).Milliseconds()
	b.Status = store.OpSucceeded
	if err != nil {
		b.Status, b.Error, b.SizeBytes, b.SHA256 = store.OpFailed, err.Error(), 0, ""
		_ = st.Delete(context.WithoutCancel(ctx), b.ObjectKey)
		c.log.Warn("manager backup failed", "err", err)
	}
	if err := c.store.FinishBackup(context.WithoutCancel(ctx), b); err != nil {
		c.log.Error("cannot record manager backup", "err", err)
	}
	if b.Status == store.OpSucceeded {
		c.pruneManagerBackups(ctx, target.ID)
	}
}

func (c *Core) snapshotTo(ctx context.Context, st storage.Storage, target store.BackupTarget, b *store.Backup) error {
	dir := filepath.Join(c.cfg.DataDir, "tmp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, "snapshot-"+b.ID+".db")
	defer os.Remove(path)
	if err := c.store.Snapshot(ctx, path); err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	return c.upload(ctx, st, target, b, func(w io.Writer) error {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(w, f)
		return err
	})
}

// pruneManagerBackups keeps the configured number of manager backups per target.
func (c *Core) pruneManagerBackups(ctx context.Context, targetID string) {
	keep := c.cfg.ManagerBackup.Keep
	if keep <= 0 {
		return
	}
	bs, err := c.store.ListBackups(ctx, store.BackupFilter{Kind: store.BackupKindManager, Status: store.OpSucceeded})
	if err != nil {
		return
	}
	n := 0
	for _, b := range bs {
		if b.TargetID != targetID {
			continue
		}
		if n++; n > keep {
			if err := c.DeleteBackup(ctx, b.ID); err != nil {
				c.log.Warn("cannot prune manager backup", "backup", b.ID, "err", err)
			}
		}
	}
}

// scheduleManagerBackup registers the manager's own backup, if configured.
func (c *Core) scheduleManagerBackup() error {
	cfg := c.cfg.ManagerBackup
	if cfg.Cron == "" {
		return nil
	}
	job := cron.NewChain(cron.SkipIfStillRunning(cron.DiscardLogger)).Then(cron.FuncJob(func() {
		done := make(chan struct{})
		if _, err := c.startManagerBackup(c.bg, cfg.TargetID, done); err != nil {
			c.log.Warn("scheduled manager backup did not start", "err", err)
			return
		}
		<-done
	}))
	if _, err := c.sched.cron.AddJob(cfg.Cron, job); err != nil {
		return fmt.Errorf("KIPITINY_MANAGER_BACKUP_CRON: %w", err)
	}
	return nil
}
