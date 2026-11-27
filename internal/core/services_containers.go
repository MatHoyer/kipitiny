package core

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

type ContainerView struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Replica  int    `json:"replica"`
	DeployID string `json:"deployId"`
	Image    string `json:"image"`
	State    string `json:"state"`
	Status   string `json:"status"`
	// Health is empty when the image has no healthcheck.
	Health string `json:"health,omitempty"`
	// Retired containers belong to a previous deployment; they are kept
	// stopped for their logs until the next deploy.
	Retired bool `json:"retired,omitempty"`
}

func (c *Core) serviceContainers(ctx context.Context, svc domain.Service) ([]container.Summary, error) {
	return c.dockerFor(svc.ServerID).ListContainers(ctx, map[string]string{
		docker.LabelProject: svc.ProjectID,
		docker.LabelService: svc.ID,
	})
}

// activeContainers returns the containers of the deployment serving traffic.
func (c *Core) activeContainers(ctx context.Context, svc domain.Service) ([]container.Summary, error) {
	cts, err := c.serviceContainers(ctx, svc)
	if err != nil {
		return nil, err
	}
	active := cts[:0]
	for _, ct := range cts {
		if isActive(ct, svc) {
			active = append(active, ct)
		}
	}
	return active, nil
}

func containerView(ct container.Summary, svc domain.Service) ContainerView {
	replica, _ := strconv.Atoi(ct.Labels[docker.LabelReplica])
	name := ""
	if len(ct.Names) > 0 {
		name = strings.TrimPrefix(ct.Names[0], "/")
	}
	health := ""
	// A stopped container keeps its last health status; it means nothing then.
	if ct.State == container.StateRunning && ct.Health != nil && ct.Health.Status != container.NoHealthcheck {
		health = string(ct.Health.Status)
	}
	return ContainerView{
		ID:       ct.ID[:12],
		Name:     name,
		Replica:  replica,
		DeployID: ct.Labels[docker.LabelDeploy],
		Image:    ct.Image,
		State:    string(ct.State),
		Status:   ct.Status,
		Health:   health,
		Retired:  !isActive(ct, svc),
	}
}

func sortedContainers(v []ContainerView) []ContainerView {
	if v == nil {
		return []ContainerView{}
	}
	// Active first, then by replica.
	slices.SortFunc(v, func(a, b ContainerView) int {
		if a.Retired != b.Retired {
			if a.Retired {
				return 1
			}
			return -1
		}
		return a.Replica - b.Replica
	})
	return v
}

type Action string

const (
	ActionStart   Action = "start"
	ActionStop    Action = "stop"
	ActionRestart Action = "restart"
)

// ServiceAction starts, stops or restarts every container of a deployed service.
func (c *Core) ServiceAction(ctx context.Context, id string, action Action) (ServiceView, error) {
	unlock, err := c.lockService(id)
	if err != nil {
		return ServiceView{}, err
	}
	defer unlock()

	svc, err := c.store.GetService(ctx, id)
	if err != nil {
		return ServiceView{}, err
	}
	if action != ActionStop && svc.Preview == "only" {
		return ServiceView{}, errPreviewOnly(svc)
	}
	if action != ActionStop {
		// Its project wakes with it: the service may need the others.
		if err := c.wakeFor(ctx, svc); err != nil {
			return ServiceView{}, err
		}
	}
	cts, err := c.activeContainers(ctx, svc)
	if err != nil {
		return ServiceView{}, err
	}
	if len(cts) == 0 {
		return ServiceView{}, fmt.Errorf("%w: service has not been deployed yet", ErrInvalid)
	}
	// Record the desired state first: the reconciler must not undo a stop.
	if err := c.store.SetServiceStopped(ctx, svc.ID, action == ActionStop); err != nil {
		return ServiceView{}, err
	}
	svc.Stopped, svc.Sleeping = action == ActionStop, false
	if !svc.Stopped {
		c.markActive(svc.ProjectID)
	}
	secs := int(stopTimeoutFor(svc).Seconds())
	dk := c.dockerFor(svc.ServerID)
	for _, ct := range cts {
		switch action {
		case ActionStart:
			if ct.State != container.StateRunning {
				_, err = dk.ContainerStart(ctx, ct.ID, client.ContainerStartOptions{})
			}
		case ActionStop:
			_, err = dk.ContainerStop(ctx, ct.ID, client.ContainerStopOptions{Timeout: &secs})
		case ActionRestart:
			_, err = dk.ContainerRestart(ctx, ct.ID, client.ContainerRestartOptions{Timeout: &secs})
		default:
			return ServiceView{}, fmt.Errorf("%w: unknown action %q", ErrInvalid, action)
		}
		if err != nil {
			return ServiceView{}, fmt.Errorf("%s %s: %w", action, ct.Names[0], err)
		}
	}
	return c.view(ctx, svc)
}
