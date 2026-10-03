package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/MatHoyer/kipitiny/internal/store"
)

var cronParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

const (
	maxKeep = 1000
	// A scheduled backup that finds its database busy (deploy, restore)
	// retries for a while instead of being skipped silently.
	busyRetryEvery = 30 * time.Second
	busyRetryFor   = 15 * time.Minute
	// Failed scheduled runs hold no data; their records are pruned after this.
	failedBackupTTL = 30 * 24 * time.Hour
)

type scheduler struct {
	mu      sync.Mutex
	cron    *cron.Cron
	entries map[string]cron.EntryID // schedule ID -> entry
	cleanup cron.EntryID            // 0 when cleanup is off
}

type ScheduleInput struct {
	TargetID    string `json:"targetId"`
	Cron        string `json:"cron"`
	KeepLast    int    `json:"keepLast"`
	KeepDaily   int    `json:"keepDaily"`
	KeepWeekly  int    `json:"keepWeekly"`
	KeepMonthly int    `json:"keepMonthly"`
	Enabled     bool   `json:"enabled"`
	// Verify restore-tests each backup the schedule makes.
	Verify bool `json:"verify"`
}

type ScheduleView struct {
	store.BackupSchedule
	NextRun *time.Time `json:"nextRun,omitempty"`
}

// StartScheduler loads every enabled schedule. Times are UTC unless the
// expression starts with CRON_TZ=<zone>.
func (c *Core) StartScheduler(ctx context.Context) error {
	c.sched.mu.Lock()
	c.sched.cron = cron.New(cron.WithParser(cronParser), cron.WithLocation(time.UTC))
	c.sched.entries = map[string]cron.EntryID{}
	c.sched.mu.Unlock()
	if err := c.seedManagerSchedule(ctx); err != nil {
		return err
	}
	if err := c.reloadSchedules(ctx); err != nil {
		return err
	}
	if err := c.scheduleCleanup(ctx); err != nil {
		return err
	}
	if _, err := c.sched.cron.AddFunc("@daily", func() { c.pruneAudit(c.bg) }); err != nil {
		return err
	}
	c.sched.cron.Start()
	return nil
}

func (c *Core) stopScheduler() {
	c.sched.mu.Lock()
	defer c.sched.mu.Unlock()
	if c.sched.cron != nil {
		c.sched.cron.Stop() // running jobs are awaited through c.wg
	}
}

// reloadSchedules replaces all cron entries from the store; cheap enough to
// run after every change.
func (c *Core) reloadSchedules(ctx context.Context) error {
	scs, err := c.store.ListBackupSchedules(ctx, "")
	if err != nil {
		return err
	}
	c.sched.mu.Lock()
	defer c.sched.mu.Unlock()
	if c.sched.cron == nil {
		return nil // scheduler not started (e.g. tests, CLI)
	}
	for id, entry := range c.sched.entries {
		c.sched.cron.Remove(entry)
		delete(c.sched.entries, id)
	}
	for _, sc := range scs {
		if !sc.Enabled {
			continue
		}
		job := cron.NewChain(cron.SkipIfStillRunning(cron.DiscardLogger)).Then(cron.FuncJob(func() {
			c.runScheduledBackup(sc.ID)
		}))
		entry, err := c.sched.cron.AddJob(sc.Cron, job)
		if err != nil {
			c.log.Error("invalid backup schedule", "schedule", sc.ID, "cron", sc.Cron, "err", err)
			continue
		}
		c.sched.entries[sc.ID] = entry
	}
	return nil
}

func (c *Core) runScheduledBackup(scheduleID string) {
	ctx := c.bg
	log := c.log.With("schedule", scheduleID)

	deadline := time.Now().Add(busyRetryFor)
	for {
		sc, err := c.store.GetBackupSchedule(ctx, scheduleID)
		if err != nil || !sc.Enabled {
			return // deleted or disabled meanwhile
		}
		done := make(chan struct{})
		if sc.Kind == store.BackupKindManager {
			_, err = c.startManagerBackup(ctx, sc.TargetID, sc.ID, done)
		} else {
			_, err = c.startBackup(ctx, sc.ServiceID, sc.TargetID, sc.ID, done)
		}
		if err == nil {
			select {
			case <-done:
			case <-ctx.Done():
			}
			return
		}
		if !errors.Is(err, ErrBusy) || time.Now().After(deadline) {
			log.Warn("scheduled backup did not start", "err", err)
			return
		}
		select {
		case <-time.After(busyRetryEvery):
		case <-ctx.Done():
			return
		}
	}
}

// afterScheduledBackup prunes old backups, then restore-tests the new one if
// the schedule asks for it.
func (c *Core) afterScheduledBackup(scheduleID, backupID string) {
	c.applyRetention(scheduleID)
	sc, err := c.store.GetBackupSchedule(c.bg, scheduleID)
	if err != nil || !sc.Verify {
		return
	}
	if _, err := c.VerifyBackup(c.bg, backupID); err != nil {
		c.log.Warn("cannot start restore test", "backup", backupID, "err", err)
	}
}

// applyRetention deletes the schedule's backups that no rule keeps.
func (c *Core) applyRetention(scheduleID string) {
	ctx := context.WithoutCancel(c.bg)
	log := c.log.With("schedule", scheduleID)
	sc, err := c.store.GetBackupSchedule(ctx, scheduleID)
	if err != nil {
		return
	}
	ok, err := c.store.ListBackups(ctx, store.BackupFilter{ScheduleID: sc.ID, Status: store.OpSucceeded})
	if err != nil {
		log.Error("retention: list backups", "err", err)
		return
	}
	keep := retain(ok, sc)
	for _, b := range ok {
		if keep[b.ID] {
			continue
		}
		if err := c.DeleteBackup(ctx, b.ID); err != nil {
			log.Warn("retention: cannot delete backup", "backup", b.ID, "err", err)
		} else {
			log.Info("retention: pruned backup", "backup", b.ID, "created", b.CreatedAt)
		}
	}
	failed, err := c.store.ListBackups(ctx, store.BackupFilter{ScheduleID: sc.ID, Status: store.OpFailed})
	if err != nil {
		return
	}
	for _, b := range failed {
		if time.Since(b.CreatedAt) > failedBackupTTL {
			_ = c.store.DeleteBackup(ctx, b.ID)
		}
	}
}

func (c *Core) ListBackupSchedules(ctx context.Context, serviceID string) ([]ScheduleView, error) {
	scs, err := c.store.ListBackupSchedules(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	views := make([]ScheduleView, len(scs))
	for i, sc := range scs {
		views[i] = c.scheduleView(sc)
	}
	return views, nil
}

func (c *Core) scheduleView(sc store.BackupSchedule) ScheduleView {
	v := ScheduleView{BackupSchedule: sc}
	c.sched.mu.Lock()
	defer c.sched.mu.Unlock()
	if entry, ok := c.sched.entries[sc.ID]; ok && c.sched.cron != nil {
		if next := c.sched.cron.Entry(entry).Next; !next.IsZero() {
			v.NextRun = &next
		}
	} else if sched, err := cronParser.Parse(sc.Cron); err == nil && sc.Enabled {
		next := sched.Next(time.Now().UTC())
		v.NextRun = &next
	}
	return v
}

func (c *Core) CreateBackupSchedule(ctx context.Context, serviceID string, in ScheduleInput) (ScheduleView, error) {
	if _, _, err := c.postgresService(ctx, serviceID); err != nil {
		return ScheduleView{}, err
	}
	return c.createSchedule(ctx, store.BackupSchedule{Kind: store.BackupKindPostgres, ServiceID: serviceID}, in)
}

// ListManagerSchedules lists the schedules backing up the manager itself.
func (c *Core) ListManagerSchedules(ctx context.Context) ([]ScheduleView, error) {
	scs, err := c.store.ListBackupSchedules(ctx, "")
	if err != nil {
		return nil, err
	}
	views := []ScheduleView{}
	for _, sc := range scs {
		if sc.Kind == store.BackupKindManager {
			views = append(views, c.scheduleView(sc))
		}
	}
	return views, nil
}

// CreateManagerSchedule schedules backups of the manager's own state.
func (c *Core) CreateManagerSchedule(ctx context.Context, in ScheduleInput) (ScheduleView, error) {
	return c.createSchedule(ctx, store.BackupSchedule{Kind: store.BackupKindManager}, in)
}

func (c *Core) createSchedule(ctx context.Context, sc store.BackupSchedule, in ScheduleInput) (ScheduleView, error) {
	if err := c.applyScheduleInput(ctx, &sc, in); err != nil {
		return ScheduleView{}, err
	}
	sc, err := c.store.CreateBackupSchedule(ctx, sc)
	if err != nil {
		return ScheduleView{}, err
	}
	if err := c.reloadSchedules(ctx); err != nil {
		return ScheduleView{}, err
	}
	return c.scheduleView(sc), nil
}

func (c *Core) UpdateBackupSchedule(ctx context.Context, id string, in ScheduleInput) (ScheduleView, error) {
	sc, err := c.store.GetBackupSchedule(ctx, id)
	if err != nil {
		return ScheduleView{}, err
	}
	if err := c.applyScheduleInput(ctx, &sc, in); err != nil {
		return ScheduleView{}, err
	}
	if sc, err = c.store.UpdateBackupSchedule(ctx, sc); err != nil {
		return ScheduleView{}, err
	}
	if err := c.reloadSchedules(ctx); err != nil {
		return ScheduleView{}, err
	}
	return c.scheduleView(sc), nil
}

func (c *Core) DeleteBackupSchedule(ctx context.Context, id string) error {
	if err := c.store.DeleteBackupSchedule(ctx, id); err != nil {
		return err
	}
	return c.reloadSchedules(ctx)
}

func (c *Core) applyScheduleInput(ctx context.Context, sc *store.BackupSchedule, in ScheduleInput) error {
	in.Cron = strings.TrimSpace(in.Cron)
	if _, err := cronParser.Parse(in.Cron); err != nil {
		return fmt.Errorf("%w: schedule: %v", ErrInvalid, err)
	}
	for _, n := range []int{in.KeepLast, in.KeepDaily, in.KeepWeekly, in.KeepMonthly} {
		if n < 0 || n > maxKeep {
			return fmt.Errorf("%w: retention counts must be between 0 and %d", ErrInvalid, maxKeep)
		}
	}
	if in.TargetID == "" {
		in.TargetID = store.LocalTargetID
	}
	if _, err := c.store.GetBackupTarget(ctx, in.TargetID); errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("%w: unknown backup target", ErrInvalid)
	} else if err != nil {
		return err
	}
	if sc.Kind == store.BackupKindManager {
		in.Verify = false // restore tests start a PostgreSQL container
	}
	sc.TargetID, sc.Cron, sc.Enabled, sc.Verify = in.TargetID, in.Cron, in.Enabled, in.Verify
	sc.KeepLast, sc.KeepDaily, sc.KeepWeekly, sc.KeepMonthly = in.KeepLast, in.KeepDaily, in.KeepWeekly, in.KeepMonthly
	return nil
}
