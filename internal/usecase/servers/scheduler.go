package servers

import (
	"context"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/kipit-labs/kipitiny/internal/usecase/data"
)

// scheduler runs the cleanup and the audit pruning; backup schedules run in
// data, jobs in core.
type scheduler struct {
	mu      sync.Mutex
	cron    *cron.Cron
	cleanup cron.EntryID // 0 when cleanup is off
}

// StartScheduler schedules the cleanup and the daily audit pruning. Times
// are UTC unless the expression starts with CRON_TZ=<zone>.
func (c *Servers) StartScheduler(ctx context.Context) error {
	c.sched.mu.Lock()
	c.sched.cron = cron.New(cron.WithParser(data.CronParser), cron.WithLocation(time.UTC))
	c.sched.mu.Unlock()
	if err := c.scheduleCleanup(ctx); err != nil {
		return err
	}
	if _, err := c.sched.cron.AddFunc("@daily", func() { c.mgr.PruneAudit(c.bg) }); err != nil {
		return err
	}
	c.sched.cron.Start()
	return nil
}

// StopScheduler stops scheduling; a running cleanup is awaited with the
// manager's background work.
func (c *Servers) StopScheduler() {
	c.sched.mu.Lock()
	defer c.sched.mu.Unlock()
	if c.sched.cron != nil {
		c.sched.cron.Stop()
	}
}
