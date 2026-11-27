package domain

import "time"

// Cron reads cron expressions (five fields or a @descriptor, times in UTC
// unless the expression starts with CRON_TZ=<zone>) and runs functions on
// them.
type Cron interface {
	// Next is when spec fires next after t, an error when spec isn't valid.
	Next(spec string, t time.Time) (time.Time, error)
	// NewScheduler returns a scheduler, stopped.
	NewScheduler() Scheduler
}

// CronEntry is a function a Scheduler runs; 0 is none.
type CronEntry int

// Scheduler runs functions on cron expressions once started.
type Scheduler interface {
	// Add runs fn on spec. With skipBusy, a run due while the previous one
	// still runs is skipped.
	Add(spec string, fn func(), skipBusy bool) (CronEntry, error)
	Remove(e CronEntry)
	// Next is when e runs next, zero before the scheduler starts.
	Next(e CronEntry) time.Time
	Start()
	// Stop stops scheduling; it doesn't wait for running functions.
	Stop()
}
