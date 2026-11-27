package projects

import (
	"context"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/kipit-labs/kipitiny/internal/usecase/data"
)

// scheduler runs the jobs; backup schedules run in data, the cleanup and
// the audit pruning in servers.
type scheduler struct {
	mu   sync.Mutex
	cron *cron.Cron
	jobs map[string]cron.EntryID // job ID -> entry
}

// StartScheduler loads every enabled job. Times are UTC unless the
// expression starts with CRON_TZ=<zone>.
func (c *Projects) StartScheduler(ctx context.Context) error {
	c.sched.mu.Lock()
	c.sched.cron = cron.New(cron.WithParser(data.CronParser), cron.WithLocation(time.UTC))
	c.sched.jobs = map[string]cron.EntryID{}
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
