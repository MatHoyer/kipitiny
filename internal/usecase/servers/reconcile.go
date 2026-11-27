package servers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"

	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

const (
	reconcileEvery    = 30 * time.Second
	reconcileDebounce = 2 * time.Second
)

// StartReconciler keeps Docker in line with the store on every server:
// missing or stopped replicas of deployed services come back, replicas above
// the desired count go away, and containers of deleted services are removed.
// It runs every 30 s, after changes, and when a managed container dies or
// disappears.
func (c *Servers) StartReconciler(_ context.Context) error {
	return c.mgr.GoLoop(func() {
		watchers := map[string]context.CancelFunc{}
		defer func() {
			for _, stop := range watchers {
				stop()
			}
		}()
		timer := time.NewTimer(0) // once at startup
		for {
			select {
			case <-c.bg.Done():
				timer.Stop()
				return
			case <-c.reconcileKick:
				// Let bursts (a rollout, several dying replicas) settle first.
				timer.Reset(reconcileDebounce)
				continue
			case <-timer.C:
			}
			c.syncWatchers(watchers)
			if err := c.Reconcile(c.bg); err != nil && c.bg.Err() == nil {
				c.log.Warn("reconcile failed", "err", err)
			}
			timer.Reset(reconcileEvery)
		}
	})
}

// Kick requests a reconcile soon.
func (c *Servers) Kick() {
	select {
	case c.reconcileKick <- struct{}{}:
	default:
	}
}

// syncWatchers runs one Docker event watcher per server.
func (c *Servers) syncWatchers(watchers map[string]context.CancelFunc) {
	servers, err := c.store.ListServers(c.bg)
	if err != nil {
		return
	}
	seen := map[string]bool{}
	for _, sv := range servers {
		seen[sv.ID] = true
		if _, ok := watchers[sv.ID]; !ok {
			ctx, cancel := context.WithCancel(c.bg)
			watchers[sv.ID] = cancel
			go c.watchEvents(ctx, sv.ID)
		}
	}
	for id, stop := range watchers {
		if !seen[id] {
			stop()
			delete(watchers, id)
		}
	}
}

// watchEvents kicks the reconciler when a managed container on a server dies
// or is removed, and reports the kernel killing one for memory, reconnecting
// if the event stream breaks.
func (c *Servers) watchEvents(ctx context.Context, serverID string) {
	for ctx.Err() == nil {
		f := make(client.Filters)
		f.Add("type", string(events.ContainerEventType))
		f.Add("label", docker.LabelManaged+"=true")
		f.Add("event", string(events.ActionDie), string(events.ActionDestroy), string(events.ActionOOM))
		res := c.DockerFor(serverID).Client.Events(ctx, client.EventsListOptions{Filters: f})
	stream:
		for {
			select {
			case m := <-res.Messages:
				if m.Action == events.ActionOOM {
					// Docker restarts the replica itself: the reconciler wouldn't see it.
					if sid := m.Actor.Attributes[docker.LabelService]; sid != "" {
						c.NotifyOOM(ctx, sid, m.Actor.Attributes[docker.LabelReplica])
					}
					continue
				}
				c.Kick()
			case err := <-res.Err:
				if err != nil && ctx.Err() == nil {
					c.log.Debug("docker event stream ended", "server", serverID, "err", err)
				}
				break stream
			case <-ctx.Done():
				return
			}
		}
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
		}
	}
}

// Reconcile is one pass of the reconciler, on every server.
func (c *Servers) Reconcile(ctx context.Context) error {
	servers, err := c.store.ListServers(ctx)
	if err != nil {
		return err
	}
	svcs, err := c.store.ListAllServices(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, sv := range servers {
		var here []domain.Service
		for _, s := range svcs {
			if s.ServerID == sv.ID {
				here = append(here, s)
			}
		}
		if err := c.reconcileServer(ctx, sv, here); err != nil {
			errs = append(errs, fmt.Errorf("server %s: %w", sv.Name, err))
		}
	}
	return errors.Join(errs...)
}

func (c *Servers) reconcileServer(ctx context.Context, sv domain.Server, svcs []domain.Service) error {
	dk := c.DockerFor(sv.ID)
	if err := dk.EnsureNetwork(ctx, docker.ProxyNetwork); err != nil {
		return err // server unreachable: nothing else will work either
	}
	if c.cfg.Traefik.Enabled {
		if err := c.mgr.EnsureTraefik(ctx, sv); err != nil {
			c.log.Warn("reconcile: traefik", "server", sv.Name, "err", err)
		}
		if sv.Kind == domain.ServerLocal {
			c.mgr.RefreshProxy(ctx)
		}
		if err := c.mgr.EnsureTunnel(ctx, sv); err != nil {
			c.log.Warn("reconcile: cloudflared", "server", sv.Name, "err", err)
		}
		if sv.Kind == domain.ServerLocal {
			if err := c.mgr.EnsureAnalytics(ctx); err != nil {
				c.log.Warn("reconcile: analytics", "err", err)
			}
		}
	}
	if sv.Kind == domain.ServerLocal {
		c.cleanupUpdater(ctx, dk)
	}
	if err := c.mgr.EnsureNetworks(ctx, sv); err != nil {
		c.log.Warn("reconcile: networks", "server", sv.Name, "err", err)
	}
	all, err := dk.ListSummaries(ctx, map[string]string{docker.LabelManaged: "true"})
	if err != nil {
		return err
	}
	c.mgr.RemoveStaleJobContainers(ctx, dk, all)
	byService := map[string][]container.Summary{}
	for _, ct := range all {
		if sid := ct.Labels[docker.LabelService]; sid != "" {
			byService[sid] = append(byService[sid], ct)
		}
	}

	var errs []error
	now := time.Now()
	known := map[string]bool{}
	projects := map[string]domain.Project{}
	for _, svc := range svcs {
		known[svc.ID] = true
		p, ok := projects[svc.ProjectID]
		if !ok {
			if p, err = c.store.GetProject(ctx, svc.ProjectID); err != nil {
				errs = append(errs, err)
				continue
			}
			projects[p.ID] = p
			if err := dk.EnsureNetwork(ctx, docker.ProjectNetwork(p.ID)); err != nil {
				errs = append(errs, err)
				continue
			}
		}
		c.mgr.TrackHealth(p, svc, byService[svc.ID], now)
		if err := c.mgr.ReconcileService(ctx, p, svc); err != nil {
			errs = append(errs, fmt.Errorf("%s/%s: %w", p.Name, svc.Name, err))
		}
	}
	c.mgr.ForgetHealth(sv.ID, known)

	// Containers of services that no longer exist (e.g. a delete interrupted
	// by a crash).
	for sid, cts := range byService {
		if known[sid] {
			continue
		}
		for _, ct := range cts {
			c.log.Info("reconcile: removing orphaned container", "server", sv.Name, "container", ct.Names[0][1:])
			if err := dk.RemoveContainer(ctx, ct.ID, domain.StopTimeout); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}
