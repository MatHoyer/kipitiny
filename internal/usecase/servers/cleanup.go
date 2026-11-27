package servers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/notify"
	"github.com/kipit-labs/kipitiny/internal/usecase/data"
)

const (
	cleanupSetting    = "cleanup"
	cleanupRunSetting = "cleanup_last_run"
	// minKeepDeployments keeps enough history for rollbacks.
	minKeepDeployments = 10
)

// Image and volume cleanup modes.
const (
	CleanOff       = "off"
	CleanDangling  = "dangling"  // images: untagged layers only
	CleanAnonymous = "anonymous" // volumes: unnamed only
	CleanUnused    = "unused"    // anything no container uses
)

// CleanupSettings is what the admin enables. Everything the manager relies
// on is protected whatever the settings: database volumes, the images of
// services and of their recent deployments (rollback), and containers and
// networks labelled kipitiny.managed.
type CleanupSettings struct {
	Enabled bool `json:"enabled"`
	// Cron is when it runs (UTC unless CRON_TZ=<zone> is given).
	Cron string `json:"cron"`
	// MinAgeHours leaves anything younger alone, so a deploy in progress
	// never loses its freshly built image.
	MinAgeHours int    `json:"minAgeHours"`
	Images      string `json:"images"`  // off, dangling, unused
	Volumes     string `json:"volumes"` // off, anonymous, unused
	BuildCache  bool   `json:"buildCache"`
	// Containers removes stopped containers the manager does not own.
	Containers bool `json:"containers"`
	// Networks removes unused networks the manager does not own.
	Networks bool `json:"networks"`
	// KeepDeployments trims each service's deployment history and logs to
	// this many entries; 0 keeps everything.
	KeepDeployments int `json:"keepDeployments"`
}

func defaultCleanupSettings() CleanupSettings {
	return CleanupSettings{
		Cron:        "0 4 * * *",
		MinAgeHours: 24,
		Images:      CleanDangling,
		Volumes:     CleanAnonymous,
		BuildCache:  true,
		Networks:    true,
	}
}

// CleanupRun is the outcome of one cleanup.
type CleanupRun struct {
	Trigger    string          `json:"trigger"` // schedule, manual
	StartedAt  time.Time       `json:"startedAt"`
	FinishedAt *time.Time      `json:"finishedAt,omitempty"`
	Servers    []CleanupResult `json:"servers"`
	// Deployments is how many old deployment records (and logs) went away.
	Deployments int    `json:"deployments"`
	Error       string `json:"error,omitempty"`
}

// problems lists the run's errors, prefixed by their server.
func (r CleanupRun) problems() []string {
	var out []string
	if r.Error != "" {
		out = append(out, r.Error)
	}
	for _, s := range r.Servers {
		for _, e := range s.Errors {
			out = append(out, s.Server+": "+e)
		}
	}
	return out
}

// CleanupResult counts what was removed on one server.
type CleanupResult struct {
	Server     string `json:"server"`
	Containers int    `json:"containers"`
	Images     int    `json:"images"`
	Volumes    int    `json:"volumes"`
	Networks   int    `json:"networks"`
	BuildCache int    `json:"buildCache"`
	// Reclaimed is approximate: Docker shares layers between images.
	Reclaimed uint64   `json:"reclaimed"`
	Errors    []string `json:"errors,omitempty"`
}

type CleanupView struct {
	Settings CleanupSettings `json:"settings"`
	Running  bool            `json:"running"`
	// Step is where a running cleanup is at, on StepServer.
	Step       string      `json:"step,omitempty"`
	StepServer string      `json:"stepServer,omitempty"`
	NextRun    *time.Time  `json:"nextRun,omitempty"`
	LastRun    *CleanupRun `json:"lastRun,omitempty"`
}

func (c *Servers) cleanupSettings(ctx context.Context) CleanupSettings {
	s := defaultCleanupSettings()
	if v, err := c.store.GetSetting(ctx, cleanupSetting); err == nil && v != "" {
		_ = json.Unmarshal([]byte(v), &s)
	}
	return s
}

func (c *Servers) Cleanup(ctx context.Context) CleanupView {
	v := CleanupView{Settings: c.cleanupSettings(ctx), Running: c.cleaning.Load()}
	if v.Running {
		v.Step, _ = c.steps.Get(cleanupKey)
		v.StepServer = c.steps.DetailOf(cleanupKey)
	}
	c.sched.mu.Lock()
	if c.sched.cron != nil && c.sched.cleanup != 0 {
		if next := c.sched.cron.Entry(c.sched.cleanup).Next; !next.IsZero() {
			v.NextRun = &next
		}
	}
	c.sched.mu.Unlock()
	if raw, err := c.store.GetSetting(ctx, cleanupRunSetting); err == nil && raw != "" {
		var run CleanupRun
		if json.Unmarshal([]byte(raw), &run) == nil {
			v.LastRun = &run
		}
	}
	return v
}

func (c *Servers) SetCleanup(ctx context.Context, s CleanupSettings) (CleanupView, error) {
	s.Cron = strings.TrimSpace(s.Cron)
	if _, err := data.CronParser.Parse(s.Cron); err != nil {
		return CleanupView{}, fmt.Errorf("%w: schedule: %s", ErrInvalid, err.Error())
	}
	if s.MinAgeHours < 1 || s.MinAgeHours > 24*365 {
		return CleanupView{}, fmt.Errorf("%w: minimum age must be between 1 hour and a year", ErrInvalid)
	}
	if !oneOf(s.Images, CleanOff, CleanDangling, CleanUnused) {
		return CleanupView{}, fmt.Errorf("%w: images must be off, dangling or unused", ErrInvalid)
	}
	if !oneOf(s.Volumes, CleanOff, CleanAnonymous, CleanUnused) {
		return CleanupView{}, fmt.Errorf("%w: volumes must be off, anonymous or unused", ErrInvalid)
	}
	if s.KeepDeployments != 0 && (s.KeepDeployments < minKeepDeployments || s.KeepDeployments > data.MaxKeep) {
		return CleanupView{}, fmt.Errorf("%w: keep 0 (all) or %d to %d deployments", ErrInvalid, minKeepDeployments, data.MaxKeep)
	}
	b, err := json.Marshal(s)
	if err != nil {
		return CleanupView{}, err
	}
	if err := c.store.SetSetting(ctx, cleanupSetting, string(b)); err != nil {
		return CleanupView{}, err
	}
	if err := c.scheduleCleanup(ctx); err != nil {
		return CleanupView{}, err
	}
	return c.Cleanup(ctx), nil
}

func oneOf(s string, options ...string) bool {
	for _, o := range options {
		if s == o {
			return true
		}
	}
	return false
}

// scheduleCleanup (re)registers the cleanup job from the stored settings.
func (c *Servers) scheduleCleanup(ctx context.Context) error {
	s := c.cleanupSettings(ctx)
	c.sched.mu.Lock()
	defer c.sched.mu.Unlock()
	if c.sched.cron == nil {
		return nil // scheduler not started (e.g. tests)
	}
	if c.sched.cleanup != 0 {
		c.sched.cron.Remove(c.sched.cleanup)
		c.sched.cleanup = 0
	}
	if !s.Enabled {
		return nil
	}
	job := cron.FuncJob(func() {
		done := make(chan struct{})
		if err := c.startCleanup(c.bg, "schedule", done); err != nil {
			c.log.Warn("scheduled cleanup did not start", "err", err)
			return
		}
		<-done
	})
	entry, err := c.sched.cron.AddJob(s.Cron, job)
	if err != nil {
		c.log.Error("invalid cleanup schedule", "cron", s.Cron, "err", err)
		return nil
	}
	c.sched.cleanup = entry
	return nil
}

// RunCleanup starts a cleanup with the saved settings now, enabled or not.
func (c *Servers) RunCleanup(ctx context.Context) (CleanupView, error) {
	if err := c.startCleanup(ctx, "manual", nil); err != nil {
		return CleanupView{}, err
	}
	return c.Cleanup(ctx), nil
}

func (c *Servers) startCleanup(ctx context.Context, trigger string, done chan<- struct{}) error {
	if !c.cleaning.CompareAndSwap(false, true) {
		return ErrBusy
	}
	s := c.cleanupSettings(ctx)
	err := c.mgr.GoBackground(func() {
		defer c.cleaning.Store(false)
		if done != nil {
			defer close(done)
		}
		c.runCleanup(s, trigger)
	})
	if err != nil {
		c.cleaning.Store(false)
	}
	return err
}

func (c *Servers) runCleanup(s CleanupSettings, trigger string) {
	ctx, cancel := context.WithTimeout(c.bg, time.Hour)
	defer cancel()
	defer c.steps.Done(cleanupKey)
	run := CleanupRun{Trigger: trigger, StartedAt: time.Now().UTC(), Servers: []CleanupResult{}}

	servers, err := c.store.ListServers(ctx)
	svcs, serr := c.store.ListAllServices(ctx)
	if err = errors.Join(err, serr); err != nil {
		run.Error = err.Error()
	} else {
		for _, sv := range servers {
			run.Servers = append(run.Servers, c.cleanServer(ctx, sv, svcs, s))
		}
		if s.KeepDeployments > 0 {
			c.steps.Set(cleanupKey, CleanupDeployments)
			run.Deployments = c.trimDeployments(ctx, svcs, s.KeepDeployments)
		}
	}
	finished := time.Now().UTC()
	run.FinishedAt = &finished
	if problems := run.problems(); len(problems) > 0 {
		c.mgr.Notify(notify.Event{
			Type:    domain.EventCleanupFailed,
			Level:   notify.Warning,
			Title:   "Docker cleanup had errors",
			Message: strings.Join(problems, "\n"),
		}, "/cleanup", "")
	}
	c.log.Info("cleanup finished", "trigger", trigger, "servers", len(run.Servers), "deployments", run.Deployments)
	if b, err := json.Marshal(run); err == nil {
		if err := c.store.SetSetting(context.WithoutCancel(ctx), cleanupRunSetting, string(b)); err != nil {
			c.log.Warn("cannot record cleanup", "err", err)
		}
	}
}
