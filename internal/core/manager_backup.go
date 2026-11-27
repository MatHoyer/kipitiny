package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/storage"
)

// managerLockID serializes manager backups like a service.
const managerLockID = "manager"

// BackupManager snapshots the manager's own state (VACUUM INTO) and uploads
// it to a target in the background.
func (c *Core) BackupManager(ctx context.Context, targetID string) (domain.Backup, error) {
	return c.startManagerBackup(ctx, targetID, "", nil)
}

// managerTargetOK refuses unencrypted remote targets for manager backups:
// the snapshot holds every credential kipitiny knows (SSH key, registry and
// cloud tokens, TOTP secret, other targets' age identities) in plaintext.
func managerTargetOK(t domain.BackupTarget) error {
	if t.Kind != domain.BackupTargetLocal && t.AgeRecipient == "" {
		return fmt.Errorf("%w: manager backups hold every credential; use local disk or a target with encryption", ErrInvalid)
	}
	return nil
}

// startManagerBackup starts a manager backup; done, if set, is closed when it
// finishes. Backups made by a schedule trigger its retention on success.
func (c *Core) startManagerBackup(ctx context.Context, targetID, scheduleID string, done chan<- struct{}) (domain.Backup, error) {
	if targetID == "" {
		targetID = domain.LocalTargetID
	}
	target, err := c.store.GetBackupTarget(ctx, targetID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Backup{}, fmt.Errorf("%w: unknown backup target", ErrInvalid)
	}
	if err != nil {
		return domain.Backup{}, err
	}
	if err := managerTargetOK(target); err != nil {
		return domain.Backup{}, err
	}
	st, err := c.openStorage(target)
	if err != nil {
		return domain.Backup{}, err
	}
	unlock, err := c.lockService(managerLockID)
	if err != nil {
		return domain.Backup{}, err
	}
	b, err := c.store.CreateBackup(ctx, domain.Backup{
		Kind:        domain.BackupKindManager,
		ServiceName: "manager",
		TargetID:    target.ID,
		ScheduleID:  scheduleID,
		ObjectKey:   objectKey("_manager/", time.Now().UTC(), ".db", target),
		Status:      domain.OpRunning,
	})
	if err != nil {
		unlock()
		return domain.Backup{}, err
	}
	if err := c.goBackground(func() {
		defer unlock()
		if done != nil {
			defer close(done)
		}
		if c.runManagerBackup(st, target, b) && scheduleID != "" {
			c.afterScheduledBackup(scheduleID, b.ID)
		}
	}); err != nil {
		unlock()
		b.Status, b.Error = domain.OpFailed, err.Error()
		_ = c.store.FinishBackup(ctx, b)
		return domain.Backup{}, err
	}
	return b, nil
}

func (c *Core) runManagerBackup(st storage.Storage, target domain.BackupTarget, b domain.Backup) bool {
	ctx, cancel := context.WithTimeout(c.bg, time.Hour)
	defer cancel()
	defer c.steps.done(backupKey(b.ID))
	start := time.Now()

	err := c.snapshotTo(ctx, st, target, &b)
	b.DurationMS = time.Since(start).Milliseconds()
	b.Status = domain.OpSucceeded
	if err != nil {
		b.Status, b.Error, b.SizeBytes, b.SHA256 = domain.OpFailed, err.Error(), 0, ""
		_ = st.Delete(context.WithoutCancel(ctx), b.ObjectKey)
		c.log.Warn("manager backup failed", "err", err)
	}
	if err := c.store.FinishBackup(context.WithoutCancel(ctx), b); err != nil {
		c.log.Error("cannot record manager backup", "err", err)
	}
	c.notifyBackup(b, target, "")
	return b.Status == domain.OpSucceeded
}

func (c *Core) snapshotTo(ctx context.Context, st storage.Storage, target domain.BackupTarget, b *domain.Backup) error {
	dir := filepath.Join(c.cfg.DataDir, "tmp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, "snapshot-"+b.ID+".db")
	defer os.Remove(path)
	c.steps.set(backupKey(b.ID), BackupSnapshot)
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

// managerScheduleSeeded marks that the KIPITINY_MANAGER_BACKUP_* defaults
// became a schedule; from then on it is edited in the UI.
const managerScheduleSeeded = "manager_schedule_seeded"

// seedManagerSchedule turns the KIPITINY_MANAGER_BACKUP_* settings into a
// manager backup schedule, once.
func (c *Core) seedManagerSchedule(ctx context.Context) error {
	if done, _ := c.store.GetSetting(ctx, managerScheduleSeeded); done != "" {
		return nil
	}
	if cfg := c.cfg.ManagerBackup; cfg.Cron != "" {
		in := ScheduleInput{TargetID: cfg.TargetID, Cron: cfg.Cron, KeepLast: cfg.Keep, Enabled: true}
		if t, err := c.store.GetBackupTarget(ctx, in.TargetID); err != nil {
			c.log.Warn("KIPITINY_MANAGER_BACKUP_TARGET not found, using local disk", "target", in.TargetID)
			in.TargetID = domain.LocalTargetID
		} else if err := managerTargetOK(t); err != nil {
			c.log.Warn("KIPITINY_MANAGER_BACKUP_TARGET is not encrypted, using local disk", "target", in.TargetID)
			in.TargetID = domain.LocalTargetID
		}
		sc := domain.BackupSchedule{Kind: domain.BackupKindManager}
		if err := c.applyScheduleInput(ctx, &sc, in); err != nil {
			return fmt.Errorf("KIPITINY_MANAGER_BACKUP_CRON: %w", err)
		}
		if _, err := c.store.CreateBackupSchedule(ctx, sc); err != nil {
			return err
		}
	}
	return c.store.SetSetting(ctx, managerScheduleSeeded, "1")
}
