package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
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
func (c *Core) StartReconciler(ctx context.Context) error {
	return c.goLoop(func() {
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
			if err := c.reconcile(c.bg); err != nil && c.bg.Err() == nil {
				c.log.Warn("reconcile failed", "err", err)
			}
			timer.Reset(reconcileEvery)
		}
	})
}

// kick requests a reconcile soon.
func (c *Core) kick() {
	select {
	case c.reconcileKick <- struct{}{}:
	default:
	}
}

// syncWatchers runs one Docker event watcher per server.
func (c *Core) syncWatchers(watchers map[string]context.CancelFunc) {
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
// or is removed, reconnecting if the event stream breaks.
func (c *Core) watchEvents(ctx context.Context, serverID string) {
	for ctx.Err() == nil {
		f := make(client.Filters)
		f.Add("type", string(events.ContainerEventType))
		f.Add("label", docker.LabelManaged+"=true")
		f.Add("event", string(events.ActionDie), string(events.ActionDestroy))
		res := c.dockerFor(serverID).Events(ctx, client.EventsListOptions{Filters: f})
	stream:
		for {
			select {
			case <-res.Messages:
				c.kick()
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

func (c *Core) reconcile(ctx context.Context) error {
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
		var here []store.Service
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

func (c *Core) reconcileServer(ctx context.Context, sv store.Server, svcs []store.Service) error {
	dk := c.dockerFor(sv.ID)
	if err := dk.EnsureNetwork(ctx, docker.ProxyNetwork); err != nil {
		return err // server unreachable: nothing else will work either
	}
	if c.cfg.Traefik.Enabled {
		if err := c.ensureTraefik(ctx, sv); err != nil {
			c.log.Warn("reconcile: traefik", "server", sv.Name, "err", err)
		}
		if err := c.ensureTunnel(ctx, sv); err != nil {
			c.log.Warn("reconcile: cloudflared", "server", sv.Name, "err", err)
		}
	}
	if sv.Kind == store.ServerLocal {
		c.cleanupUpdater(ctx, dk)
	}
	all, err := dk.ListContainers(ctx, map[string]string{docker.LabelManaged: "true"})
	if err != nil {
		return err
	}
	byService := map[string][]container.Summary{}
	for _, ct := range all {
		if sid := ct.Labels[docker.LabelService]; sid != "" {
			byService[sid] = append(byService[sid], ct)
		}
	}

	var errs []error
	known := map[string]bool{}
	projects := map[string]store.Project{}
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
		if err := c.reconcileService(ctx, p, svc, byService[svc.ID]); err != nil {
			errs = append(errs, fmt.Errorf("%s/%s: %w", p.Name, svc.Name, err))
		}
	}

	// Containers of services that no longer exist (e.g. a delete interrupted
	// by a crash).
	for sid, cts := range byService {
		if known[sid] {
			continue
		}
		for _, ct := range cts {
			c.log.Info("reconcile: removing orphaned container", "server", sv.Name, "container", ct.Names[0][1:])
			if err := dk.RemoveContainer(ctx, ct.ID, stopTimeout); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (c *Core) reconcileService(ctx context.Context, project store.Project, svc store.Service, cts []container.Summary) error {
	if svc.CurrentDeploymentID == "" || svc.Stopped {
		return nil // never deployed, or stopped on purpose
	}
	unlock, err := c.lockService(svc.ID)
	if errors.Is(err, ErrBusy) {
		return nil // a deploy/backup/restore is working on it
	}
	if err != nil {
		return err
	}
	defer unlock()

	// Re-read under the lock: a deploy may just have finished.
	if svc, err = c.store.GetService(ctx, svc.ID); err != nil || svc.Stopped {
		return err
	}
	if cts, err = c.serviceContainers(ctx, svc); err != nil {
		return err
	}
	dep, err := c.store.GetDeployment(ctx, svc.CurrentDeploymentID)
	if err != nil {
		return err
	}
	// Recreate replicas as deployed; only the count follows live settings.
	want := dep.Config
	if want.ID == "" { // deployed before snapshots existed
		want = svc
		want.Image = dep.Image
	}
	want.Replicas = svc.Replicas
	if want.Kind == store.ServiceKindPostgres {
		want.Replicas = 1
	}

	byReplica := map[int]container.Summary{}
	var extra []container.Summary
	for _, ct := range cts {
		if !isActive(ct, svc) {
			continue
		}
		idx, _ := strconv.Atoi(ct.Labels[docker.LabelReplica])
		if _, dup := byReplica[idx]; dup || idx < 1 || idx > want.Replicas {
			extra = append(extra, ct)
			continue
		}
		byReplica[idx] = ct
	}

	log := c.log.With("project", project.Name, "service", svc.Name)
	dk := c.dockerFor(svc.ServerID)
	for i := 1; i <= want.Replicas; i++ {
		ct, ok := byReplica[i]
		switch {
		case !ok:
			if err := c.recreateReplica(ctx, project, want, dep.ID, i); err != nil {
				return err
			}
			log.Info("reconcile: recreated missing replica", "replica", i)
		case ct.State == container.StateExited || ct.State == container.StateCreated:
			if _, err := dk.ContainerStart(ctx, ct.ID, client.ContainerStartOptions{}); err != nil {
				return fmt.Errorf("start %s: %w", ct.Names[0][1:], err)
			}
			log.Info("reconcile: started stopped replica", "container", ct.Names[0][1:])
		}
	}

	// Scale down from the highest replica, one at a time.
	slices.SortFunc(extra, func(a, b container.Summary) int {
		ai, _ := strconv.Atoi(a.Labels[docker.LabelReplica])
		bi, _ := strconv.Atoi(b.Labels[docker.LabelReplica])
		return bi - ai
	})
	for i, ct := range extra {
		if i > 0 {
			time.Sleep(retireGap)
		}
		log.Info("reconcile: removing extra replica", "container", ct.Names[0][1:])
		if err := dk.RemoveContainer(ctx, ct.ID, stopTimeoutFor(svc)); err != nil {
			return err
		}
	}
	return nil
}

func (c *Core) recreateReplica(ctx context.Context, project store.Project, svc store.Service, deployID string, replica int) error {
	dbs, err := c.projectDatabases(ctx, svc.ProjectID)
	if err != nil {
		return err
	}
	dk := c.dockerFor(svc.ServerID)
	if isBuiltImage(svc.Image) {
		if _, err := dk.ImageInspect(ctx, svc.Image); err != nil {
			return fmt.Errorf("build %s is gone; redeploy: %w", svc.Image, err)
		}
	} else if err := dk.EnsureImage(ctx, svc.Image); err != nil {
		return err
	}
	probe, err := c.probeFor(ctx, svc)
	if err != nil {
		return err
	}
	_, err = dk.Run(ctx, replicaSpec(project, svc, dbs, deployID, replica, probe, c.certResolver(ctx, svc.ServerID, svc.Domain)))
	return err
}
