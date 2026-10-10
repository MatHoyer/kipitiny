package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/MatHoyer/kipitiny/internal/notify"
	"github.com/MatHoyer/kipitiny/internal/probe"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	uptimeTick = 5 * time.Second
	// A service is down after uptimeDownAfter failed checks in a row and up
	// again after uptimeUpAfter successful ones, so a single slow answer or
	// a flapping service doesn't flood the channels.
	uptimeDownAfter = 3
	uptimeUpAfter   = 2
	// uptimeRecent is how many results each check keeps in memory.
	uptimeRecent = 60
	// uptimeKeep is how long hourly counts are kept.
	uptimeKeep = 30 * 24 * time.Hour

	defaultUptimeInterval = 60
	defaultUptimeTimeout  = 10
)

// UptimeResult is the outcome of one check.
type UptimeResult struct {
	At time.Time `json:"at"`
	OK bool      `json:"ok"`
	// Status is 0 when no response arrived.
	Status    int    `json:"status,omitempty"`
	LatencyMS int64  `json:"latencyMs"`
	Error     string `json:"error,omitempty"`
}

// uptimeState tracks the checks between runs, in memory.
type uptimeState struct {
	mu     sync.Mutex
	runs   map[string]*uptimeRun // service ID ->
	kick   chan struct{}
	pruned time.Time
}

type uptimeRun struct {
	next    time.Time
	running bool
	// down is the state last notified; fails and oks count the results in a
	// row that disagree with it.
	down      bool
	fails     int
	oks       int
	lastError string
	recent    []UptimeResult
}

// observe records a result and reports whether the service changed state.
func (r *uptimeRun) observe(res UptimeResult) bool {
	r.recent = append(r.recent, res)
	if len(r.recent) > uptimeRecent {
		r.recent = append(r.recent[:0], r.recent[len(r.recent)-uptimeRecent:]...)
	}
	if res.OK {
		r.fails, r.oks = 0, r.oks+1
	} else {
		r.fails, r.oks, r.lastError = r.fails+1, 0, res.Error
	}
	switch {
	case !r.down && r.fails >= uptimeDownAfter:
		r.down = true
	case r.down && r.oks >= uptimeUpAfter:
		r.down = false
	default:
		return false
	}
	r.fails, r.oks = 0, 0
	return true
}

// StartUptime runs the uptime checks, each on its own interval.
func (c *Core) StartUptime() error {
	c.uptime.mu.Lock()
	c.uptime.kick = make(chan struct{}, 1)
	c.uptime.mu.Unlock()
	return c.goLoop(func() {
		tick := time.NewTicker(uptimeTick)
		defer tick.Stop()
		for {
			c.scheduleUptime(c.bg)
			select {
			case <-c.bg.Done():
				return
			case <-tick.C:
			case <-c.uptime.kick:
			}
		}
	})
}

func (c *Core) kickUptime() {
	c.uptime.mu.Lock()
	kick := c.uptime.kick
	c.uptime.mu.Unlock()
	if kick == nil {
		return // loop not started (tests)
	}
	select {
	case kick <- struct{}{}:
	default:
	}
}

// scheduleUptime starts the checks that are due and forgets removed ones.
func (c *Core) scheduleUptime(ctx context.Context) {
	checks, err := c.store.ListUptimeChecks(ctx)
	if err != nil {
		return
	}
	now := time.Now()
	if now.Sub(c.uptime.pruned) > time.Hour {
		if err := c.store.PruneUptime(ctx, now.Add(-uptimeKeep)); err != nil {
			c.log.Warn("prune uptime results", "err", err)
		}
		c.uptime.pruned = now
	}
	c.uptime.mu.Lock()
	defer c.uptime.mu.Unlock()
	if c.uptime.runs == nil {
		c.uptime.runs = map[string]*uptimeRun{}
	}
	seen := map[string]bool{}
	for _, chk := range checks {
		seen[chk.ServiceID] = true
		r := c.uptime.runs[chk.ServiceID]
		if r == nil {
			r = &uptimeRun{down: chk.Down}
			c.uptime.runs[chk.ServiceID] = r
		}
		if !chk.Enabled || r.running || now.Before(r.next) {
			continue
		}
		r.running = true
		r.next = now.Add(time.Duration(chk.IntervalSec) * time.Second)
		err := c.goLoop(func() {
			defer func() {
				c.uptime.mu.Lock()
				r.running = false
				c.uptime.mu.Unlock()
			}()
			c.runUptime(c.bg, chk, r)
		})
		if err != nil {
			r.running = false
		}
	}
	for id := range c.uptime.runs {
		if !seen[id] {
			delete(c.uptime.runs, id)
		}
	}
}

// uptimeTarget returns the URL a check requests, or why it can't run now.
func uptimeTarget(svc store.Service, chk store.UptimeCheck) (url, problem string) {
	switch {
	case !httpRouted(svc):
		return "", "the service has no public domain"
	case svc.Stopped:
		return "", "the service is stopped"
	case svc.CurrentDeploymentID == "":
		return "", "the service is not deployed yet"
	}
	return "https://" + svc.Domain + chk.Path, ""
}

func (c *Core) runUptime(ctx context.Context, chk store.UptimeCheck, r *uptimeRun) {
	svc, err := c.store.GetService(ctx, chk.ServiceID)
	if err != nil {
		return
	}
	url, problem := uptimeTarget(svc, chk)
	if problem != "" {
		return // not a failure: nothing is expected to answer
	}
	hc := c.uptimeHTTP
	if hc == nil {
		hc = probe.NewClient()
	}
	rctx, cancel := context.WithTimeout(ctx, time.Duration(chk.TimeoutSec)*time.Second)
	res, err := probe.HTTP(rctx, hc, url, chk.ExpectedStatus)
	cancel()
	if ctx.Err() != nil {
		return // shutting down: not the service's fault
	}
	out := UptimeResult{At: time.Now(), OK: err == nil, Status: res.Status, LatencyMS: res.Latency.Milliseconds()}
	if err != nil {
		out.Error = uptimeError(err, chk.TimeoutSec)
	}
	if err := c.store.AddUptimeResult(ctx, chk.ServiceID, out.At, out.OK, res.Latency); err != nil {
		c.log.Warn("record uptime result", "service", svc.Name, "err", err)
	}

	c.uptime.mu.Lock()
	changed := r.observe(out)
	down, lastError := r.down, r.lastError
	c.uptime.mu.Unlock()
	if !changed {
		return
	}
	since := chk.ChangedAt
	if err := c.store.SetUptimeState(ctx, chk.ServiceID, down, out.At); err != nil && !errors.Is(err, store.ErrNotFound) {
		c.log.Warn("save uptime state", "service", svc.Name, "err", err)
	}
	c.notifyUptime(ctx, svc, url, down, lastError, since, out.At)
}

// uptimeError makes a request error short enough for a notification.
func uptimeError(err error, timeoutSec int) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Sprintf("no answer within %d s", timeoutSec)
	}
	msg := err.Error()
	// net/http prefixes errors with the method and the full URL.
	if i := strings.LastIndex(msg, "\": "); i >= 0 {
		msg = msg[i+3:]
	}
	return msg
}

func (c *Core) notifyUptime(ctx context.Context, svc store.Service, url string, down bool, lastError string, since *time.Time, at time.Time) {
	project, err := c.store.GetProject(ctx, svc.ProjectID)
	if err != nil {
		return
	}
	name := project.Name + "/" + svc.Name
	fields := []notify.Field{{Name: "Project", Value: project.Name}, {Name: "Service", Value: svc.Name}, {Name: "URL", Value: url}}
	e := notify.Event{Type: EventUptimeUp, Level: notify.Success, Title: name + " is back up", Fields: fields}
	if down {
		e.Type, e.Level, e.Title = EventUptimeDown, notify.Error, name+" is down"
		e.Message = fmt.Sprintf("%d checks in a row failed: %s", uptimeDownAfter, lastError)
	} else if since != nil {
		e.Message = "Down for " + at.Sub(*since).Round(time.Second).String() + "."
	}
	c.notify(e, "/services/"+svc.ID, "")
}

type UptimeInput struct {
	Path           string `json:"path"`
	IntervalSec    int    `json:"intervalSec"`
	TimeoutSec     int    `json:"timeoutSec"`
	ExpectedStatus int    `json:"expectedStatus"`
	Enabled        bool   `json:"enabled"`
}

// UptimeView is a service's check with what it has seen.
type UptimeView struct {
	// Check is nil when the service has none.
	Check *store.UptimeCheck `json:"check"`
	URL   string             `json:"url,omitempty"`
	// Problem says why the check isn't running.
	Problem string         `json:"problem,omitempty"`
	Recent  []UptimeResult `json:"recent"`
	// Uptime percentages; nil without checks in the period.
	Uptime24h *float64 `json:"uptime24h"`
	Uptime7d  *float64 `json:"uptime7d"`
	Uptime30d *float64 `json:"uptime30d"`
	// AvgLatencyMS is over the successful checks of the last 24 hours.
	AvgLatencyMS *int64 `json:"avgLatencyMs"`
}

// Uptime returns a service's check and its results.
func (c *Core) Uptime(ctx context.Context, serviceID string) (UptimeView, error) {
	svc, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return UptimeView{}, err
	}
	v := UptimeView{Recent: []UptimeResult{}}
	chk, err := c.store.GetUptimeCheck(ctx, serviceID)
	if errors.Is(err, store.ErrNotFound) {
		return v, nil
	} else if err != nil {
		return UptimeView{}, err
	}
	v.Check = &chk
	v.URL, v.Problem = uptimeTarget(svc, chk)
	if v.URL == "" && svc.Domain != "" {
		v.URL = "https://" + svc.Domain + chk.Path
	}
	if !chk.Enabled {
		v.Problem = "paused"
	}
	c.uptime.mu.Lock()
	if r := c.uptime.runs[serviceID]; r != nil {
		v.Recent = append(v.Recent, r.recent...)
	}
	c.uptime.mu.Unlock()

	now := time.Now()
	hours, err := c.store.ListUptimeHours(ctx, serviceID, now.Add(-uptimeKeep))
	if err != nil {
		return UptimeView{}, err
	}
	v.Uptime24h, v.AvgLatencyMS = uptimeOver(hours, now.Add(-24*time.Hour))
	v.Uptime7d, _ = uptimeOver(hours, now.Add(-7*24*time.Hour))
	v.Uptime30d, _ = uptimeOver(hours, now.Add(-uptimeKeep))
	return v, nil
}

// uptimeOver sums the hours from the one holding since: the percentage of
// successful checks and their average response time.
func uptimeOver(hours []store.UptimeHour, since time.Time) (pct *float64, latency *int64) {
	from := since.UTC().Truncate(time.Hour)
	var checks, failures int
	var ms int64
	for _, h := range hours {
		if h.Hour.Before(from) {
			continue
		}
		checks, failures, ms = checks+h.Checks, failures+h.Failures, ms+h.LatencyMS
	}
	if checks == 0 {
		return nil, nil
	}
	p := float64(checks-failures) / float64(checks) * 100
	if ok := int64(checks - failures); ok > 0 {
		avg := ms / ok
		latency = &avg
	}
	return &p, latency
}

// SetUptime creates or replaces a service's check. It runs right away. A
// git project's checks are in its compose file.
func (c *Core) SetUptime(ctx context.Context, serviceID string, in UptimeInput) (UptimeView, error) {
	svc, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return UptimeView{}, err
	}
	if err := c.checkGitOwned(ctx, svc.ProjectID); err != nil {
		return UptimeView{}, err
	}
	chk, err := uptimeCheck(svc, in)
	if err != nil {
		return UptimeView{}, err
	}
	if err := c.saveUptime(ctx, chk); err != nil {
		return UptimeView{}, err
	}
	return c.Uptime(ctx, serviceID)
}

// uptimeCheck validates svc's check settings, with the defaults applied.
func uptimeCheck(svc store.Service, in UptimeInput) (store.UptimeCheck, error) {
	if svc.Kind != store.ServiceKindApp || !httpRouted(svc) {
		return store.UptimeCheck{}, fmt.Errorf("%w: only an app with a public domain can have an uptime check", ErrInvalid)
	}
	chk := store.UptimeCheck{
		ServiceID:      svc.ID,
		Path:           strings.TrimSpace(in.Path),
		IntervalSec:    in.IntervalSec,
		TimeoutSec:     in.TimeoutSec,
		ExpectedStatus: in.ExpectedStatus,
		Enabled:        in.Enabled,
	}
	if chk.Path == "" {
		chk.Path = "/"
	}
	if chk.IntervalSec == 0 {
		chk.IntervalSec = defaultUptimeInterval
	}
	if chk.TimeoutSec == 0 {
		chk.TimeoutSec = defaultUptimeTimeout
	}
	switch {
	case !strings.HasPrefix(chk.Path, "/") || strings.ContainsAny(chk.Path, " #\t\r\n"):
		return store.UptimeCheck{}, fmt.Errorf("%w: path must start with / (e.g. /health)", ErrInvalid)
	case chk.IntervalSec < 30 || chk.IntervalSec > 3600:
		return store.UptimeCheck{}, fmt.Errorf("%w: interval must be between 30 and 3600 seconds", ErrInvalid)
	case chk.TimeoutSec < 1 || chk.TimeoutSec > 60 || chk.TimeoutSec >= chk.IntervalSec:
		return store.UptimeCheck{}, fmt.Errorf("%w: timeout must be between 1 and 60 seconds, and shorter than the interval", ErrInvalid)
	case chk.ExpectedStatus != 0 && (chk.ExpectedStatus < 100 || chk.ExpectedStatus > 599):
		return store.UptimeCheck{}, fmt.Errorf("%w: expected status must be an HTTP status (100-599), or 0 for any below 400", ErrInvalid)
	}
	return chk, nil
}

// sameUptime reports whether two checks have the same settings.
func sameUptime(a, b store.UptimeCheck) bool {
	return a.Path == b.Path && a.IntervalSec == b.IntervalSec && a.TimeoutSec == b.TimeoutSec &&
		a.ExpectedStatus == b.ExpectedStatus && a.Enabled == b.Enabled
}

// saveUptime stores a check and runs it now with the new settings; the
// notified state is kept.
func (c *Core) saveUptime(ctx context.Context, chk store.UptimeCheck) error {
	if _, err := c.store.SaveUptimeCheck(ctx, chk); err != nil {
		return err
	}
	c.uptime.mu.Lock()
	if r := c.uptime.runs[chk.ServiceID]; r != nil {
		r.next = time.Time{}
	}
	c.uptime.mu.Unlock()
	c.kickUptime()
	return nil
}

// DeleteUptime removes a service's check and its results.
func (c *Core) DeleteUptime(ctx context.Context, serviceID string) error {
	svc, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return err
	}
	if err := c.checkGitOwned(ctx, svc.ProjectID); err != nil {
		return err
	}
	return c.deleteUptime(ctx, serviceID)
}

func (c *Core) deleteUptime(ctx context.Context, serviceID string) error {
	if err := c.store.DeleteUptimeCheck(ctx, serviceID); err != nil {
		return err
	}
	c.uptime.mu.Lock()
	delete(c.uptime.runs, serviceID)
	c.uptime.mu.Unlock()
	return nil
}
