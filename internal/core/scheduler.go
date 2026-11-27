package core

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

// StartScheduler loads every enabled schedule. Times are UTC unless the
// expression starts with CRON_TZ=<zone>.
func (c *Core) StartScheduler(ctx context.Context) error {
	if err := c.StartSchedules(ctx); err != nil {
		return err
	}
	c.sched.mu.Lock()
	c.sched.cron = cron.New(cron.WithParser(data.CronParser), cron.WithLocation(time.UTC))
	c.sched.jobs = map[string]cron.EntryID{}
	c.sched.mu.Unlock()
	if err := c.reloadJobs(ctx); err != nil {
		return err
	}
	if err := c.Servers.StartScheduler(ctx); err != nil {
		return err
	}
	c.sched.cron.Start()
	return nil
}

func (c *Core) stopScheduler() {
	c.StopSchedules()
	c.StopScheduler()
	c.sched.mu.Lock()
	defer c.sched.mu.Unlock()
	if c.sched.cron != nil {
		c.sched.cron.Stop() // running jobs are awaited through c.wg
	}
}
