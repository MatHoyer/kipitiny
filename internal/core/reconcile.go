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

// StartReconciler keeps Docker in line with the store: missing or stopped
// replicas of deployed services come back, replicas above the desired count
// go away, and containers of deleted services are removed. It runs every
// 30 s, after changes, and when a managed container dies or disappears.
func (c *Core) StartReconciler(ctx context.Context) error {
	return c.goBackground(func() {
		go c.watchEvents()
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

// watchEvents kicks the reconciler when a managed container dies or is
// removed, reconnecting if the event stream breaks.
func (c *Core) watchEvents() {
	for c.bg.Err() == nil {
		f := make(client.Filters)
		f.Add("type", string(events.ContainerEventType))
		f.Add("label", docker.LabelManaged+"=true")
		f.Add("event", string(events.ActionDie), string(events.ActionDestroy))
		res := c.docker.Events(c.bg, client.EventsListOptions{Filters: f})
	stream:
		for {
			select {
			case <-res.Messages:
				c.kick()
			case err := <-res.Err:
				if err != nil && c.bg.Err() == nil {
					c.log.Debug("docker event stream ended", "err", err)
				}
				break stream
			case <-c.bg.Done():
				return
			}
		}
		select {
		case <-time.After(5 * time.Second):
		case <-c.bg.Done():
		}
	}
}

func (c *Core) reconcile(ctx context.Context) error {
	if c.cfg.Traefik.Enabled {
		if err := c.ensureTraefik(ctx); err != nil {
			c.log.Warn("reconcile: traefik", "err", err)
		}
	}
	svcs, err := c.store.ListAllServices(ctx)
	if err != nil {
		return err
	}
	all, err := c.docker.ListContainers(ctx, map[string]string{docker.LabelManaged: "true"})
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
			if err := c.docker.EnsureNetwork(ctx, docker.ProjectNetwork(p.ID)); err != nil {
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
			c.log.Info("reconcile: removing orphaned container", "container", ct.Names[0][1:])
			if err := c.docker.RemoveContainer(ctx, ct.ID, stopTimeout); err != nil {
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
	for i := 1; i <= want.Replicas; i++ {
		ct, ok := byReplica[i]
		switch {
		case !ok:
			if err := c.recreateReplica(ctx, project, want, dep.ID, i); err != nil {
				return err
			}
			log.Info("reconcile: recreated missing replica", "replica", i)
		case ct.State == container.StateExited || ct.State == container.StateCreated:
			if _, err := c.docker.ContainerStart(ctx, ct.ID, client.ContainerStartOptions{}); err != nil {
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
		if err := c.docker.RemoveContainer(ctx, ct.ID, stopTimeoutFor(svc)); err != nil {
			return err
		}
	}
	return nil
}

func (c *Core) recreateReplica(ctx context.Context, project store.Project, svc store.Service, deployID string, replica int) error {
	var db *store.Service
	if svc.DatabaseID != "" {
		d, err := c.store.GetService(ctx, svc.DatabaseID)
		if err != nil {
			return fmt.Errorf("linked database: %w", err)
		}
		db = &d
	}
	if err := c.docker.EnsureImage(ctx, svc.Image); err != nil {
		return err
	}
	probe, err := c.probeFor(ctx, svc)
	if err != nil {
		return err
	}
	_, err = c.docker.Run(ctx, replicaSpec(project, svc, db, deployID, replica, probe))
	return err
}
