package core

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	// sleepEvery is how often request counters are read: idle times are
	// known to about that.
	sleepEvery = time.Minute
	// metricsAddr is Traefik's Prometheus entrypoint, on loopback only: the
	// manager reads it with an exec in the container, so it works the same on
	// remote servers and apps on the proxy network can't read it.
	metricsAddr = "127.0.0.1:8082"
	// routerRequests is the counter of requests per router.
	routerRequests = "traefik_router_requests_total{"
)

// sleepState knows when each sleep-enabled project last got a request.
// Memory only: after a restart every project counts as just active, so
// nothing falls asleep at once.
type sleepState struct {
	mu         sync.Mutex
	lastActive map[string]time.Time   // project ID ->
	requests   map[string]float64     // project ID -> last counter sum
	waking     map[string]time.Time   // project ID -> when a request woke it
	projects   map[string]*sync.Mutex // project ID -> held to fall asleep or wake
}

// lock serializes falling asleep and waking for a project.
func (s *sleepState) lock(projectID string) (unlock func()) {
	s.mu.Lock()
	if s.projects == nil {
		s.projects = map[string]*sync.Mutex{}
	}
	m, ok := s.projects[projectID]
	if !ok {
		m = &sync.Mutex{}
		s.projects[projectID] = m
	}
	s.mu.Unlock()
	m.Lock()
	return m.Unlock
}

// markActive records activity now (a request, a deploy, a start).
func (c *Core) markActive(projectID string) {
	c.sleep.mu.Lock()
	defer c.sleep.mu.Unlock()
	if c.sleep.lastActive == nil {
		c.sleep.lastActive = map[string]time.Time{}
	}
	c.sleep.lastActive[projectID] = time.Now()
}

// setRunning records that a deployed service runs: neither stopped nor
// asleep, and its project active as of now.
func (c *Core) setRunning(ctx context.Context, svc store.Service) error {
	if err := c.store.SetServiceStopped(ctx, svc.ID, false); err != nil {
		return err
	}
	c.markActive(svc.ProjectID)
	return nil
}

// observe takes a project's request counter: any change (Traefik restarted
// counts too) is activity. It returns when the project was last active.
func (s *sleepState) observe(id string, requests float64, now time.Time) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Each map on its own: markActive may have made lastActive alone.
	if s.lastActive == nil {
		s.lastActive = map[string]time.Time{}
	}
	if s.requests == nil {
		s.requests = map[string]float64{}
	}
	prev, seen := s.requests[id]
	s.requests[id] = requests
	last, ok := s.lastActive[id]
	if !ok || (seen && prev != requests) {
		last = now
		s.lastActive[id] = now
	}
	return last
}

// forget drops projects that no longer sleep.
func (s *sleepState) forget(keep map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.lastActive {
		if !keep[id] {
			delete(s.lastActive, id)
			delete(s.requests, id)
		}
	}
}

// sleepBlocker says why a project's services keep it from sleeping: only a
// request through Traefik wakes it, so it needs an app with a domain, and
// no traffic may go around Traefik.
func sleepBlocker(svcs []store.Service) error {
	routed := false
	for _, s := range svcs {
		switch {
		case len(s.PublishedPorts) > 0:
			return fmt.Errorf("%w: %s has published ports: their traffic can't wake the project", ErrInvalid, s.Name)
		case s.HostNetwork:
			return fmt.Errorf("%w: %s uses the host network: its traffic can't wake the project", ErrInvalid, s.Name)
		case httpRouted(s):
			routed = true
		}
	}
	if !routed {
		return fmt.Errorf("%w: only a project with an app on a domain can sleep: a request to it wakes the project", ErrInvalid)
	}
	return nil
}

// checkSleepServer refuses sleep mode where nothing can wake the project: a
// remote server reaches the manager's pages through KIPITINY_DOMAIN.
func (c *Core) checkSleepServer(ctx context.Context, project store.Project) error {
	sv, err := c.store.GetServer(ctx, project.ServerID)
	if err != nil {
		return err
	}
	if sv.Kind != store.ServerLocal && (c.cfg.Domain == "" || isLocalDomain(c.cfg.Domain)) {
		return fmt.Errorf("%w: on a remote server, sleep mode needs the manager's domain (KIPITINY_DOMAIN): requests wake the project through its pages", ErrInvalid)
	}
	return nil
}

// checkProjectSleep checks that a project may sleep with these services.
func (c *Core) checkProjectSleep(ctx context.Context, project store.Project, svcs []store.Service) error {
	if s := project.SleepAfterSeconds; s < minSleepAfterSeconds || s > maxSleepAfterSeconds {
		return fmt.Errorf("%w: sleep after must be between %d minutes and %d days", ErrInvalid, minSleepAfterSeconds/60, maxSleepAfterSeconds/86400)
	}
	if err := sleepBlocker(svcs); err != nil {
		return err
	}
	return c.checkSleepServer(ctx, project)
}

// SetProjectSleep sets a project's sleep mode: after sleepAfterSeconds
// without a request to its apps, every service stops, databases too, until
// the next request. 0 turns it off, waking the project.
func (c *Core) SetProjectSleep(ctx context.Context, projectID string, sleepAfterSeconds int) (store.Project, error) {
	p, err := c.store.GetProject(ctx, projectID)
	if err != nil {
		return store.Project{}, err
	}
	p.SleepAfterSeconds = sleepAfterSeconds
	if sleepAfterSeconds != 0 {
		svcs, err := c.store.ListServices(ctx, projectID)
		if err != nil {
			return store.Project{}, err
		}
		if err := c.checkProjectSleep(ctx, p, svcs); err != nil {
			return store.Project{}, err
		}
	}
	if err := c.store.SetProjectSleep(ctx, projectID, sleepAfterSeconds); err != nil {
		return store.Project{}, err
	}
	c.markActive(projectID) // the idle time counts from now
	if sleepAfterSeconds == 0 {
		if err := c.wakeProject(ctx, projectID); err != nil {
			return store.Project{}, err
		}
	}
	return c.GetProject(ctx, projectID)
}

// StartSleep puts projects with sleep mode to sleep once their apps got no
// request for their idle time.
func (c *Core) StartSleep() error {
	return c.goLoop(func() {
		tick := time.NewTicker(sleepEvery)
		defer tick.Stop()
		for {
			select {
			case <-c.bg.Done():
				return
			case <-tick.C:
			}
			if err := c.sleepIdle(c.bg); err != nil && c.bg.Err() == nil {
				c.log.Warn("sleep check failed", "err", err)
			}
		}
	})
}

// sleepIdle reads each server's request counters once and puts idle
// projects to sleep. A server whose counters can't be read keeps its
// projects awake.
func (c *Core) sleepIdle(ctx context.Context) error {
	projects, err := c.store.ListProjects(ctx)
	if err != nil {
		return err
	}
	all, err := c.store.ListAllServices(ctx)
	if err != nil {
		return err
	}
	svcs := map[string][]store.Service{}
	for _, s := range all {
		svcs[s.ProjectID] = append(svcs[s.ProjectID], s)
	}
	byServer := map[string][]store.Project{}
	keep := map[string]bool{}
	for _, p := range projects {
		if p.SleepAfterSeconds == 0 || sleepBlocker(svcs[p.ID]) != nil {
			continue
		}
		keep[p.ID] = true
		awake, asleep := false, false
		for _, s := range svcs[p.ID] {
			asleep = asleep || s.Sleeping
			awake = awake || (!s.Sleeping && !s.Stopped && s.CurrentDeploymentID != "")
		}
		if awake && !asleep {
			byServer[p.ServerID] = append(byServer[p.ServerID], p)
		}
	}
	c.sleep.forget(keep)
	var errs []error
	for serverID, here := range byServer {
		counts, err := c.routerRequests(ctx, serverID)
		if err != nil {
			errs = append(errs, fmt.Errorf("server %s: %w", serverID, err))
			continue
		}
		now := time.Now()
		for _, p := range here {
			var requests float64
			for _, s := range svcs[p.ID] {
				requests += counts[strings.ToLower(s.ID)]
			}
			last := c.sleep.observe(p.ID, requests, now)
			if now.Sub(last) < time.Duration(p.SleepAfterSeconds)*time.Second {
				continue
			}
			if err := c.putToSleep(ctx, p.ID); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", p.Name, err))
			}
		}
	}
	return errors.Join(errs...)
}

// putToSleep marks the project's running services sleeping, then stops
// them, apps first so none loses its database while answering (containers
// are kept, so waking is a plain start). A project with a service being
// deployed or backed up is left for next time.
func (c *Core) putToSleep(ctx context.Context, projectID string) error {
	defer c.sleep.lock(projectID)()
	if c.jobRunningIn(projectID) { // it may need the project's databases
		return nil
	}
	svcs, err := c.store.ListServices(ctx, projectID)
	if err != nil {
		return err
	}
	for _, s := range svcs {
		unlock, err := c.lockService(s.ID)
		if errors.Is(err, ErrBusy) {
			return nil
		}
		if err != nil {
			return err
		}
		defer unlock()
	}
	p, err := c.store.GetProject(ctx, projectID)
	if err != nil || p.SleepAfterSeconds == 0 {
		return err
	}
	// Read again under the locks: a deploy may have just finished.
	if svcs, err = c.store.ListServices(ctx, projectID); err != nil || sleepBlocker(svcs) != nil {
		return err
	}
	// Record the desired state first: the reconciler must not undo it.
	if err := c.store.SetProjectSleeping(ctx, projectID, true); err != nil {
		return err
	}
	slices.SortStableFunc(svcs, func(a, b store.Service) int { return cmpBool(a.Kind.IsDatabase(), b.Kind.IsDatabase()) })
	for _, svc := range svcs {
		if svc.Stopped || svc.CurrentDeploymentID == "" {
			continue
		}
		cts, err := c.activeContainers(ctx, svc)
		if err != nil {
			return err
		}
		secs := int(stopTimeoutFor(svc).Seconds())
		dk := c.dockerFor(svc.ServerID)
		for _, ct := range cts {
			if ct.State != container.StateRunning {
				continue
			}
			if _, err := dk.ContainerStop(ctx, ct.ID, client.ContainerStopOptions{Timeout: &secs}); err != nil {
				return fmt.Errorf("stop %s: %w", ct.Names[0], err)
			}
		}
	}
	c.log.Info("project asleep", "project", p.Name, "idle", time.Duration(p.SleepAfterSeconds)*time.Second)
	return nil
}

// cmpBool orders false before true.
func cmpBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}

// coldStart is how long the starting page waits for a woken app before it
// gives up and shows the usual "unavailable" page.
const coldStart = 2 * time.Minute

// starting reports whether svc's project is asleep or was woken less than
// coldStart ago, waking it in the background when it sleeps: a visitor gets
// the starting page. Maintenance mode wins: nothing wakes.
func (c *Core) starting(svc store.Service, maintenance bool) bool {
	if svc.Stopped || maintenance {
		return false
	}
	now := time.Now()
	c.sleep.mu.Lock()
	woken, ok := c.sleep.waking[svc.ProjectID]
	if svc.Sleeping && (!ok || now.Sub(woken) >= coldStart) {
		if c.sleep.waking == nil {
			c.sleep.waking = map[string]time.Time{}
		}
		woken, ok = now, true
		c.sleep.waking[svc.ProjectID] = now
		_ = c.goBackground(func() {
			if err := c.wakeProject(c.bg, svc.ProjectID); err != nil {
				c.log.Warn("wake failed", "service", svc.Name, "err", err)
			}
		})
	}
	c.sleep.mu.Unlock()
	return ok && now.Sub(woken) < coldStart
}

// wakeProject starts a sleeping project's services again, databases first,
// then apps after those they depend on, so they find them; the reconciler takes
// it from there. Waking an awake project does nothing. It takes no service
// lock: a deploy or a backup holding one wakes the project it needs.
func (c *Core) wakeProject(ctx context.Context, projectID string) error {
	defer c.sleep.lock(projectID)()
	svcs, err := c.store.ListServices(ctx, projectID)
	if err != nil {
		return err
	}
	svcs = slices.DeleteFunc(svcs, func(s store.Service) bool { return !s.Sleeping })
	if len(svcs) == 0 {
		return nil
	}
	c.markActive(projectID)
	if err := c.store.SetProjectSleeping(ctx, projectID, false); err != nil {
		return err
	}
	// In dependency order, each service awaited as the others need it.
	// shortcut: one service at a time, even those that don't depend on each
	// other; start them in parallel if waking gets slow.
	await := awaited(svcs)
	for _, svc := range startOrder(svcs) {
		cts, err := c.activeContainers(ctx, svc)
		if err != nil {
			return err
		}
		dk := c.dockerFor(svc.ServerID)
		for _, ct := range cts {
			if ct.State == container.StateRunning || (isJob(svc) && completed(ct)) {
				continue // a job that already did its work stays done
			}
			if _, err := dk.ContainerStart(ctx, ct.ID, client.ContainerStartOptions{}); err != nil {
				c.kick() // the reconciler recreates what can't start
				c.log.Warn("wake: start failed", "container", ct.Names[0], "err", err)
			}
		}
		if cond := await[svc.Name]; cond != "" {
			for _, ct := range cts {
				if err := c.waitCondition(ctx, dk, ct.ID, svc, cond); err != nil {
					c.log.Warn("wake: dependency not ready, starting the next services anyway", "service", svc.Name, "err", err)
				}
			}
		}
	}
	c.kick()
	c.log.Info("project woken", "project", projectID)
	return nil
}

// wakeFor wakes svc's project when svc sleeps, so its containers run.
func (c *Core) wakeFor(ctx context.Context, svc store.Service) error {
	cur, err := c.store.GetService(ctx, svc.ID)
	if err != nil || !cur.Sleeping {
		return err
	}
	return c.wakeProject(ctx, svc.ProjectID)
}

// routerRequests sums Traefik's request counters per service (lower-case
// ID), over the routers of its replicas (one per configuration hash).
func (c *Core) routerRequests(ctx context.Context, serverID string) (map[string]float64, error) {
	dk := c.dockerFor(serverID)
	cts, err := dk.ListContainers(ctx, map[string]string{docker.LabelComponent: traefikComponent})
	if err != nil {
		return nil, err
	}
	if len(cts) != 1 || cts[0].State != container.StateRunning {
		return nil, errors.New("traefik is not running")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	pr, pw := io.Pipe()
	var counts map[string]float64
	done := make(chan error, 1)
	go func() {
		var err error
		counts, err = parseRouterRequests(pr)
		pr.CloseWithError(err)
		done <- err
	}()
	err = dk.Exec(ctx, cts[0].ID, docker.ExecOptions{
		Cmd:    []string{"wget", "-qO-", "http://" + metricsAddr + "/metrics"},
		Stdout: pw,
	})
	pw.CloseWithError(err)
	if perr := <-done; err == nil {
		err = perr
	}
	if err != nil {
		return nil, fmt.Errorf("read traefik metrics: %w", err)
	}
	return counts, nil
}

// parseRouterRequests reads Prometheus text, keeping only the request
// counters of the routers kipitiny names (kipitiny-<id>-<hash>@docker).
func parseRouterRequests(r io.Reader) (map[string]float64, error) {
	out := map[string]float64{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, routerRequests) {
			continue
		}
		_, rest, ok := strings.Cut(line, `router="kipitiny-`)
		if !ok {
			continue
		}
		router, rest, ok := strings.Cut(rest, `"`)
		if !ok || !strings.HasSuffix(router, "@docker") {
			continue // the pages' routers (@http) are not the app's traffic
		}
		id, _, ok := strings.Cut(router, "-")
		if !ok {
			continue
		}
		_, value, ok := strings.Cut(rest, "} ")
		if !ok {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) == 0 {
			continue
		}
		v, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			continue
		}
		out[id] += v
	}
	return out, sc.Err()
}
