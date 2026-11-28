package app

import "context"

// StartScheduler starts every schedule: backups (data), jobs (projects), the
// cleanup and the audit pruning (servers).
func (c *App) StartScheduler(ctx context.Context) error {
	if err := c.StartSchedules(ctx); err != nil {
		return err
	}
	if err := c.Projects.StartScheduler(ctx); err != nil {
		return err
	}
	return c.Servers.StartScheduler(ctx)
}

func (c *App) stopScheduler() {
	c.StopSchedules()
	c.Projects.StopScheduler()
	c.Servers.StopScheduler()
}
