package core

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
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

// sleepState knows when each sleep-enabled app last got a request. Memory
// only: after a restart every app counts as just active, so nothing falls
// asleep at once.
type sleepState struct {
	mu         sync.Mutex
	lastActive map[string]time.Time // service ID ->
	requests   map[string]float64   // service ID -> last counter sum
	waking     map[string]time.Time // service ID -> when a request woke it
}

// markActive records activity now (a request, a deploy, a start).
func (c *Core) markActive(id string) {
	c.sleep.mu.Lock()
	defer c.sleep.mu.Unlock()
	if c.sleep.lastActive == nil {
		c.sleep.lastActive = map[string]time.Time{}
	}
	c.sleep.lastActive[id] = time.Now()
}

// setRunning records that a deployed service runs: neither stopped nor
// asleep, and active as of now.
func (c *Core) setRunning(ctx context.Context, id string) error {
	if err := c.store.SetServiceStopped(ctx, id, false); err != nil {
		return err
	}
	c.markActive(id)
	return nil
}

// observe takes a service's request counter: any change (Traefik restarted
// counts too) is activity. It returns when the service was last active.
func (s *sleepState) observe(id string, requests float64, now time.Time) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastActive == nil {
		s.lastActive, s.requests = map[string]time.Time{}, map[string]float64{}
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

// forget drops services that no longer sleep.
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

// validateSleep checks an app's sleep mode: only an app Traefik routes can
// be woken by a request.
func validateSleep(s store.Service) error {
	if s.SleepAfterSeconds == 0 {
		return nil
	}
	switch {
	case s.SleepAfterSeconds < minSleepAfterSeconds || s.SleepAfterSeconds > maxSleepAfterSeconds:
		return fmt.Errorf("%w: sleep after must be between %d minutes and %d days", ErrInvalid, minSleepAfterSeconds/60, maxSleepAfterSeconds/86400)
	case !httpRouted(s):
		return fmt.Errorf("%w: only an app with a domain and a container port can sleep: a request wakes it", ErrInvalid)
	case len(s.PublishedPorts) > 0:
		return fmt.Errorf("%w: an app with published ports can't sleep: their traffic can't wake it", ErrInvalid)
	}
	return nil
}

// checkSleepServer refuses sleep mode where nothing can wake the app: a
// remote server reaches the manager's pages through KIPITINY_DOMAIN.
func (c *Core) checkSleepServer(ctx context.Context, s store.Service, project store.Project) error {
	if s.SleepAfterSeconds == 0 {
		return nil
	}
	sv, err := c.store.GetServer(ctx, project.ServerID)
	if err != nil {
		return err
	}
	if sv.Kind != store.ServerLocal && (c.cfg.Domain == "" || isLocalDomain(c.cfg.Domain)) {
		return fmt.Errorf("%w: on a remote server, sleep mode needs the manager's domain (KIPITINY_DOMAIN): requests wake the app through its pages", ErrInvalid)
	}
	return nil
}

// StartSleep puts apps with sleep mode to sleep once they got no request for
// their idle time.
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

// sleepIdle reads each server's request counters once and puts idle apps to
// sleep. A server whose counters can't be read keeps its apps awake.
func (c *Core) sleepIdle(ctx context.Context) error {
	svcs, err := c.store.ListAllServices(ctx)
	if err != nil {
		return err
	}
	byServer := map[string][]store.Service{}
	keep := map[string]bool{}
	for _, s := range svcs {
		if s.SleepAfterSeconds > 0 && !s.Stopped && s.CurrentDeploymentID != "" {
			keep[s.ID] = true
			if !s.Sleeping {
				byServer[s.ServerID] = append(byServer[s.ServerID], s)
			}
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
		for _, s := range here {
			last := c.sleep.observe(s.ID, counts[strings.ToLower(s.ID)], now)
			if now.Sub(last) < time.Duration(s.SleepAfterSeconds)*time.Second {
				continue
			}
			if err := c.putToSleep(ctx, s.ID); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", s.Name, err))
			}
		}
	}
	return errors.Join(errs...)
}

// putToSleep marks the service sleeping, then stops its replicas (kept, so
// waking is a plain start). A service being deployed is left for next time.
func (c *Core) putToSleep(ctx context.Context, id string) error {
	unlock, err := c.lockService(id)
	if errors.Is(err, ErrBusy) {
		return nil
	}
	if err != nil {
		return err
	}
	defer unlock()
	svc, err := c.store.GetService(ctx, id)
	if err != nil || svc.Stopped || svc.Sleeping || svc.SleepAfterSeconds == 0 {
		return err
	}
	cts, err := c.activeContainers(ctx, svc)
	if err != nil {
		return err
	}
	// Record the desired state first: the reconciler must not undo it.
	if err := c.store.SetServiceSleeping(ctx, svc.ID, true); err != nil {
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
	c.log.Info("service asleep", "service", svc.Name, "idle", time.Duration(svc.SleepAfterSeconds)*time.Second)
	return nil
}

// coldStart is how long the starting page waits for a woken app before it
// gives up and shows the usual "unavailable" page.
const coldStart = 2 * time.Minute

// starting reports whether svc is asleep or was woken less than coldStart
// ago, waking it in the background when it sleeps: a visitor gets the
// starting page.
func (c *Core) starting(svc store.Service) bool {
	if svc.Stopped || svc.Maintenance.Enabled {
		return false
	}
	now := time.Now()
	c.sleep.mu.Lock()
	woken, ok := c.sleep.waking[svc.ID]
	if svc.Sleeping && (!ok || now.Sub(woken) >= coldStart) {
		if c.sleep.waking == nil {
			c.sleep.waking = map[string]time.Time{}
		}
		woken, ok = now, true
		c.sleep.waking[svc.ID] = now
		_ = c.goBackground(func() {
			if err := c.Wake(c.bg, svc.ID); err != nil {
				c.log.Warn("wake failed", "service", svc.Name, "err", err)
			}
		})
	}
	c.sleep.mu.Unlock()
	return ok && now.Sub(woken) < coldStart
}

// Wake starts a sleeping service's replicas again; the reconciler takes it
// from there. Waking an awake service does nothing.
func (c *Core) Wake(ctx context.Context, id string) error {
	svc, err := c.store.GetService(ctx, id)
	if err != nil || !svc.Sleeping {
		return err
	}
	unlock, err := c.lockService(id)
	if errors.Is(err, ErrBusy) {
		return nil // being deployed or woken already
	}
	if err != nil {
		return err
	}
	defer unlock()
	if svc, err = c.store.GetService(ctx, id); err != nil || !svc.Sleeping {
		return err
	}
	c.markActive(svc.ID)
	if err := c.store.SetServiceSleeping(ctx, svc.ID, false); err != nil {
		return err
	}
	cts, err := c.activeContainers(ctx, svc)
	if err != nil {
		return err
	}
	dk := c.dockerFor(svc.ServerID)
	for _, ct := range cts {
		if ct.State == container.StateRunning {
			continue
		}
		if _, err := dk.ContainerStart(ctx, ct.ID, client.ContainerStartOptions{}); err != nil {
			c.kick() // the reconciler recreates what can't start
			c.log.Warn("wake: start failed", "container", ct.Names[0], "err", err)
		}
	}
	c.kick()
	c.log.Info("service woken", "service", svc.Name)
	return nil
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
