package servers

import (
	"context"
	"sync"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

// scheduler runs the cleanup and the audit pruning; backup schedules run in
// data, jobs in core.
type scheduler struct {
	mu      sync.Mutex
	cron    domain.Scheduler
	cleanup domain.CronEntry // 0 when cleanup is off
}

// StartScheduler schedules the cleanup and the daily audit pruning. Times
// are UTC unless the expression starts with CRON_TZ=<zone>.
func (c *Servers) StartScheduler(ctx context.Context) error {
	c.sched.mu.Lock()
	c.sched.cron = c.cron.NewScheduler()
	c.sched.mu.Unlock()
	if err := c.scheduleCleanup(ctx); err != nil {
		return err
	}
	if _, err := c.sched.cron.Add("@daily", func() { c.mgr.PruneAudit(c.bg) }, false); err != nil {
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
