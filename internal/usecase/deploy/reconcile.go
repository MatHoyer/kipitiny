package deploy

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	cerrdefs "github.com/containerd/errdefs"

	"github.com/moby/moby/client"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/notify"
	"github.com/kipit-labs/kipitiny/internal/usecase/data"
	"github.com/kipit-labs/kipitiny/internal/usecase/monitoring"
)

func (c *Deployer) ReconcileService(ctx context.Context, project domain.Project, svc domain.Service) error {
	if svc.CurrentDeploymentID == "" || svc.Stopped || svc.Sleeping {
		return nil // never deployed, stopped on purpose, or asleep
	}
	unlock, err := c.mgr.LockServiceBackground(svc.ID)
	if errors.Is(err, ErrBusy) {
		return nil // a deploy/backup/restore is working on it
	}
	if err != nil {
		return err
	}
	defer unlock()

	// Re-read under the lock: a deploy may just have finished.
	if svc, err = c.store.GetService(ctx, svc.ID); err != nil || svc.Stopped || svc.Sleeping {
		return err
	}
	cts, err := c.ServiceContainers(ctx, svc)
	if err != nil {
		return err
	}
	dep, err := c.store.GetDeployment(ctx, svc.CurrentDeploymentID)
	if err != nil {
		return err
	}
	// Recreate replicas as deployed, but with the live name (a rename doesn't
	// redeploy) and replica count.
	want := dep.Config
	if want.ID == "" { // deployed before snapshots existed
		want = svc
		want.Image = dep.Image
	}
	want.Name, want.Replicas, want.Networks = svc.Name, svc.Replicas, svc.Networks
	if want.Kind.IsDatabase() {
		want.Replicas = 1
	}

	byReplica := map[int]domain.Container{}
	var extra []domain.Container
	for _, ct := range cts {
		if !IsActive(ct, svc) {
			continue
		}
		idx, _ := strconv.Atoi(ct.Labels[domain.LabelReplica])
		if _, dup := byReplica[idx]; dup || idx < 1 || idx > want.Replicas {
			extra = append(extra, ct)
			continue
		}
		byReplica[idx] = ct
	}

	log := c.log.With("project", project.Name, "service", svc.Name)
	dk := c.mgr.DockerFor(svc.ServerID)
	for i := 1; i <= want.Replicas; i++ {
		ct, ok := byReplica[i]
		switch {
		case !ok:
			if err := c.recreateReplica(ctx, project, want, dep.ID, i); err != nil {
				return err
			}
			log.Info("reconcile: recreated missing replica", "replica", i)
		case ct.State == domain.StateExited && isJob(want):
			// Ran once (or Docker gave up on-failure): not restarted.
		case ct.State == domain.StateExited || ct.State == domain.StateCreated:
			// Read before the start resets it.
			in, err := dk.ContainerInspect(ctx, ct.ID, client.ContainerInspectOptions{})
			oom := err == nil && in.Container.State != nil && in.Container.State.OOMKilled
			if _, err := dk.ContainerStart(ctx, ct.ID, client.ContainerStartOptions{}); cerrdefs.IsNotFound(err) {
				// A network it was on was removed outside kipitiny: Docker
				// can't start it any more, even once the network is back.
				if err := dk.RemoveContainer(ctx, ct.ID, 0); err != nil {
					return err
				}
				if err := c.recreateReplica(ctx, project, want, dep.ID, i); err != nil {
					return err
				}
				log.Info("reconcile: recreated replica whose network was removed", "replica", i)
				continue
			} else if err != nil {
				return fmt.Errorf("start %s: %w", ct.Names[0][1:], err)
			}
			log.Info("reconcile: started stopped replica", "container", ct.Names[0][1:])
			if oom {
				c.mgr.NotifyOOM(ctx, svc.ID, strconv.Itoa(i))
				continue
			}
			c.mgr.Notify(notify.Event{
				Type:    domain.EventServiceRestarted,
				Level:   notify.Warning,
				Title:   fmt.Sprintf("%s/%s restarted", project.Name, svc.Name),
				Message: fmt.Sprintf("Replica %d had stopped (%s); kipitiny started it again. Further restarts are not reported for %s.", i, ct.Status, monitoring.NotifyCooldown),
				Fields:  []notify.Field{{Name: "Project", Value: project.Name}, {Name: "Service", Value: svc.Name}},
			}, "/services/"+svc.ID, "restarted:"+svc.ID)
		}
	}

	// Scale down from the highest replica, one at a time.
	slices.SortFunc(extra, func(a, b domain.Container) int {
		ai, _ := strconv.Atoi(a.Labels[domain.LabelReplica])
		bi, _ := strconv.Atoi(b.Labels[domain.LabelReplica])
		return bi - ai
	})
	for i, ct := range extra {
		if i > 0 {
			time.Sleep(retireGap)
		}
		log.Info("reconcile: removing extra replica", "container", ct.Names[0][1:])
		if err := dk.RemoveContainer(ctx, ct.ID, data.StopTimeoutFor(svc)); err != nil {
			return err
		}
	}
	return c.syncNetworks(ctx, project, svc, cts, false)
}

func (c *Deployer) recreateReplica(ctx context.Context, project domain.Project, svc domain.Service, deployID string, replica int) error {
	src, err := c.envSources(ctx, project, svc)
	if err != nil {
		return err
	}
	dk := c.mgr.DockerFor(svc.ServerID)
	if err := dk.EnsureImage(ctx, svc.Image, c.RegistryAuth(ctx, svc.Image), svc.Runtime.Platform); err != nil {
		return err
	}
	probe, err := c.probeFor(ctx, svc)
	if err != nil {
		return err
	}
	_, err = c.runContainer(ctx, dk, replicaSpec(project, svc, src, deployID, replica, probe, c.mgr.RouteFor(ctx, svc)), serviceFiles(svc, src)...)
	return err
}
