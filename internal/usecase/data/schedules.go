package data

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

// CronParser reads the schedules of backups, jobs and the cleanup.
var CronParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

const (
	MaxKeep = 1000
	// A scheduled backup that finds its database busy (deploy, restore)
	// retries for a while instead of being skipped silently.
	busyRetryEvery = 30 * time.Second
	busyRetryFor   = 15 * time.Minute
	// Failed scheduled runs hold no data; their records are pruned after this.
	failedBackupTTL = 30 * 24 * time.Hour
)

// scheduler runs the backup schedules (jobs and the cleanup have their own,
// in core).
type scheduler struct {
	mu      sync.Mutex
	cron    *cron.Cron
	entries map[string]cron.EntryID // schedule ID -> entry
}

type ScheduleInput struct {
	TargetID string `json:"targetId"`
	// Database picks one of a PostgreSQL instance's databases; empty
	// follows the service's own.
	Database    string `json:"database"`
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
	domain.BackupSchedule
	NextRun *time.Time `json:"nextRun,omitempty"`
}

// StartSchedules loads every enabled backup schedule. Times are UTC unless
// the expression starts with CRON_TZ=<zone>.
func (c *Data) StartSchedules(ctx context.Context) error {
	c.sched.mu.Lock()
	c.sched.cron = cron.New(cron.WithParser(CronParser), cron.WithLocation(time.UTC))
	c.sched.entries = map[string]cron.EntryID{}
	c.sched.mu.Unlock()
	if err := c.seedManagerSchedule(ctx); err != nil {
		return err
	}
	if err := c.reloadSchedules(ctx); err != nil {
		return err
	}
	c.sched.cron.Start()
	return nil
}

// StopSchedules stops starting scheduled backups; running ones are awaited
// as operations.
func (c *Data) StopSchedules() {
	c.sched.mu.Lock()
	defer c.sched.mu.Unlock()
	if c.sched.cron != nil {
		c.sched.cron.Stop()
	}
}

// reloadSchedules replaces all cron entries from the store; cheap enough to
// run after every change.
func (c *Data) reloadSchedules(ctx context.Context) error {
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

func (c *Data) runScheduledBackup(scheduleID string) {
	ctx := c.bg
	log := c.log.With("schedule", scheduleID)

	deadline := time.Now().Add(busyRetryFor)
	for {
		sc, err := c.store.GetBackupSchedule(ctx, scheduleID)
		if err != nil || !sc.Enabled {
			return // deleted or disabled meanwhile
		}
		done := make(chan struct{})
		if sc.Kind == domain.BackupKindManager {
			_, err = c.startManagerBackup(ctx, sc.TargetID, sc.ID, done)
		} else {
			_, err = c.startBackup(ctx, sc.ServiceID, sc.TargetID, sc.Database, sc.ID, done)
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
func (c *Data) afterScheduledBackup(scheduleID, backupID string) {
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
func (c *Data) applyRetention(scheduleID string) {
	ctx := context.WithoutCancel(c.bg)
	log := c.log.With("schedule", scheduleID)
	sc, err := c.store.GetBackupSchedule(ctx, scheduleID)
	if err != nil {
		return
	}
	ok, err := c.store.ListBackups(ctx, domain.BackupFilter{ScheduleID: sc.ID, Status: domain.OpSucceeded})
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
	failed, err := c.store.ListBackups(ctx, domain.BackupFilter{ScheduleID: sc.ID, Status: domain.OpFailed})
	if err != nil {
		return
	}
	for _, b := range failed {
		if time.Since(b.CreatedAt) > failedBackupTTL {
			_ = c.store.DeleteBackup(ctx, b.ID)
		}
	}
}

func (c *Data) ListBackupSchedules(ctx context.Context, serviceID string) ([]ScheduleView, error) {
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

func (c *Data) scheduleView(sc domain.BackupSchedule) ScheduleView {
	v := ScheduleView{BackupSchedule: sc}
	c.sched.mu.Lock()
	defer c.sched.mu.Unlock()
	if entry, ok := c.sched.entries[sc.ID]; ok && c.sched.cron != nil {
		if next := c.sched.cron.Entry(entry).Next; !next.IsZero() {
			v.NextRun = &next
		}
	} else if sched, err := CronParser.Parse(sc.Cron); err == nil && sc.Enabled {
		next := sched.Next(time.Now().UTC())
		v.NextRun = &next
	}
	return v
}

func (c *Data) CreateBackupSchedule(ctx context.Context, serviceID string, in ScheduleInput) (ScheduleView, error) {
	svc, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return ScheduleView{}, err
	}
	kind, err := backupKind(svc)
	if err != nil {
		return ScheduleView{}, err
	}
	return c.createSchedule(ctx, domain.BackupSchedule{Kind: kind, ServiceID: serviceID}, in)
}

// ListManagerSchedules lists the schedules backing up the manager itself.
func (c *Data) ListManagerSchedules(ctx context.Context) ([]ScheduleView, error) {
	scs, err := c.store.ListBackupSchedules(ctx, "")
	if err != nil {
		return nil, err
	}
	views := []ScheduleView{}
	for _, sc := range scs {
		if sc.Kind == domain.BackupKindManager {
			views = append(views, c.scheduleView(sc))
		}
	}
	return views, nil
}

// CreateManagerSchedule schedules backups of the manager's own state.
func (c *Data) CreateManagerSchedule(ctx context.Context, in ScheduleInput) (ScheduleView, error) {
	return c.createSchedule(ctx, domain.BackupSchedule{Kind: domain.BackupKindManager}, in)
}

func (c *Data) createSchedule(ctx context.Context, sc domain.BackupSchedule, in ScheduleInput) (ScheduleView, error) {
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

func (c *Data) UpdateBackupSchedule(ctx context.Context, id string, in ScheduleInput) (ScheduleView, error) {
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

func (c *Data) DeleteBackupSchedule(ctx context.Context, id string) error {
	if err := c.store.DeleteBackupSchedule(ctx, id); err != nil {
		return err
	}
	return c.reloadSchedules(ctx)
}

func (c *Data) applyScheduleInput(ctx context.Context, sc *domain.BackupSchedule, in ScheduleInput) error {
	in.Cron = strings.TrimSpace(in.Cron)
	if _, err := CronParser.Parse(in.Cron); err != nil {
		return fmt.Errorf("%w: schedule: %v", ErrInvalid, err)
	}
	for _, n := range []int{in.KeepLast, in.KeepDaily, in.KeepWeekly, in.KeepMonthly} {
		if n < 0 || n > MaxKeep {
			return fmt.Errorf("%w: retention counts must be between 0 and %d", ErrInvalid, MaxKeep)
		}
	}
	if in.TargetID == "" {
		in.TargetID = domain.LocalTargetID
	}
	target, err := c.store.GetBackupTarget(ctx, in.TargetID)
	if errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("%w: unknown backup target", ErrInvalid)
	} else if err != nil {
		return err
	}
	if sc.Kind == domain.BackupKindManager {
		if err := managerTargetOK(target); err != nil {
			return err
		}
		in.Verify = false // the manager's own state has no restore test
	}
	if in.Database != "" {
		if err := c.checkScheduleDatabase(ctx, *sc, in.Database); err != nil {
			return err
		}
	}
	sc.TargetID, sc.Database, sc.Cron, sc.Enabled, sc.Verify = in.TargetID, in.Database, in.Cron, in.Enabled, in.Verify
	sc.KeepLast, sc.KeepDaily, sc.KeepWeekly, sc.KeepMonthly = in.KeepLast, in.KeepDaily, in.KeepWeekly, in.KeepMonthly
	return nil
}

// checkScheduleDatabase checks a schedule's database against the running
// instance. Its own database needs no check (nor a running service).
func (c *Data) checkScheduleDatabase(ctx context.Context, sc domain.BackupSchedule, name string) error {
	if sc.Kind != domain.BackupKindPostgres {
		return fmt.Errorf("%w: only a PostgreSQL service has databases to pick", ErrInvalid)
	}
	svc, err := c.store.GetService(ctx, sc.ServiceID)
	if err != nil {
		return err
	}
	if name == svc.Env[PGDatabase] {
		return nil
	}
	ct, err := c.runningContainer(ctx, svc)
	if err != nil {
		return fmt.Errorf("%w: start %s to pick one of its databases", ErrInvalid, svc.Name)
	}
	_, err = c.pgDatabaseOf(ctx, svc, ct, name)
	return err
}
