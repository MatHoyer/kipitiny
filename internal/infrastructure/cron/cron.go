// Package cron is domain.Cron with robfig/cron.
package cron

import (
	"time"

	"github.com/robfig/cron/v3"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

var parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// Cron implements domain.Cron.
type Cron struct{}

func (Cron) Next(spec string, t time.Time) (time.Time, error) {
	s, err := parser.Parse(spec)
	if err != nil {
		return time.Time{}, err
	}
	return s.Next(t), nil
}

func (Cron) NewScheduler() domain.Scheduler {
	return scheduler{cron.New(cron.WithParser(parser), cron.WithLocation(time.UTC))}
}

type scheduler struct{ c *cron.Cron }

func (s scheduler) Add(spec string, fn func(), skipBusy bool) (domain.CronEntry, error) {
	var job cron.Job = cron.FuncJob(fn)
	if skipBusy {
		job = cron.NewChain(cron.SkipIfStillRunning(cron.DiscardLogger)).Then(job)
	}
	id, err := s.c.AddJob(spec, job)
	return domain.CronEntry(id), err
}

func (s scheduler) Remove(e domain.CronEntry) { s.c.Remove(cron.EntryID(e)) }

func (s scheduler) Next(e domain.CronEntry) time.Time { return s.c.Entry(cron.EntryID(e)).Next }

func (s scheduler) Start() { s.c.Start() }

func (s scheduler) Stop() { s.c.Stop() }
