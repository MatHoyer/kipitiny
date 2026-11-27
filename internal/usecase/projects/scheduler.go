package projects

import (
	"context"
	"sync"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

// scheduler runs the jobs; backup schedules run in data, the cleanup and
// the audit pruning in servers.
type scheduler struct {
	mu   sync.Mutex
	cron domain.Scheduler
	jobs map[string]domain.CronEntry // job ID -> entry
}

// StartScheduler loads every enabled job. Times are UTC unless the
// expression starts with CRON_TZ=<zone>.
func (c *Projects) StartScheduler(ctx context.Context) error {
	c.sched.mu.Lock()
	c.sched.cron = c.cron.NewScheduler()
	c.sched.jobs = map[string]domain.CronEntry{}
	c.sched.mu.Unlock()
	if err := c.ReloadJobs(ctx); err != nil {
		return err
	}
	c.sched.cron.Start()
	return nil
}

// StopScheduler stops scheduling; running jobs are awaited with the
// manager's background work.
func (c *Projects) StopScheduler() {
	c.sched.mu.Lock()
	defer c.sched.mu.Unlock()
	if c.sched.cron != nil {
		c.sched.cron.Stop()
	}
}
