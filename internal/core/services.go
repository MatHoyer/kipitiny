package core

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/distribution/reference"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// SecretMask replaces env values in read responses. Sending it back in an
// update keeps the stored value.
const SecretMask = "********"

const maxReplicas = 10

var (
	domainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$|^localhost$|^([a-z0-9-]+\.)+localhost$`)
	envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

type ServiceInput struct {
	Name     string            `json:"name"`
	Kind     store.ServiceKind `json:"kind"`
	Image    string            `json:"image"`
	Replicas int               `json:"replicas"`
	Port     int               `json:"port"`
	Domain   string            `json:"domain"`
	Env      map[string]string `json:"env"`
}

// ServicePatch updates only the fields that are set. Env replaces the whole
// map; values equal to SecretMask keep their stored value.
type ServicePatch struct {
	Image    *string           `json:"image"`
	Replicas *int              `json:"replicas"`
	Port     *int              `json:"port"`
	Domain   *string           `json:"domain"`
	Env      map[string]string `json:"env"`
}

type ContainerView struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Replica  int    `json:"replica"`
	DeployID string `json:"deployId"`
	Image    string `json:"image"`
	State    string `json:"state"`
	Status   string `json:"status"`
}

type ServiceView struct {
	store.Service
	Containers []ContainerView `json:"containers"`
}

func (c *Core) CreateService(ctx context.Context, projectID string, in ServiceInput) (ServiceView, error) {
	if _, err := c.store.GetProject(ctx, projectID); err != nil {
		return ServiceView{}, err
	}
	if in.Kind == "" {
		in.Kind = store.ServiceKindApp
	}
	if in.Replicas == 0 {
		in.Replicas = 1
	}
	svc := store.Service{
		ProjectID: projectID,
		Name:      in.Name,
		Kind:      in.Kind,
		Image:     strings.TrimSpace(in.Image),
		Replicas:  in.Replicas,
		Port:      in.Port,
		Domain:    strings.ToLower(strings.TrimSpace(in.Domain)),
		Env:       in.Env,
	}
	if svc.Env == nil {
		svc.Env = map[string]string{}
	}
	if !nameRe.MatchString(svc.Name) {
		return ServiceView{}, fmt.Errorf("%w: name must be lowercase letters, digits and dashes (max 40)", ErrInvalid)
	}
	if svc.Kind != store.ServiceKindApp {
		return ServiceView{}, fmt.Errorf("%w: only app services are supported for now", ErrInvalid)
	}
	if err := validateService(svc); err != nil {
		return ServiceView{}, err
	}
	svc, err := c.store.CreateService(ctx, svc)
	if err != nil {
		return ServiceView{}, err
	}
	return ServiceView{Service: masked(svc), Containers: []ContainerView{}}, nil
}

func (c *Core) UpdateService(ctx context.Context, id string, p ServicePatch) (ServiceView, error) {
	svc, err := c.store.GetService(ctx, id)
	if err != nil {
		return ServiceView{}, err
	}
	if p.Image != nil {
		svc.Image = strings.TrimSpace(*p.Image)
	}
	if p.Replicas != nil {
		svc.Replicas = *p.Replicas
	}
	if p.Port != nil {
		svc.Port = *p.Port
	}
	if p.Domain != nil {
		svc.Domain = strings.ToLower(strings.TrimSpace(*p.Domain))
	}
	if p.Env != nil {
		env := make(map[string]string, len(p.Env))
		for k, v := range p.Env {
			if old, ok := svc.Env[k]; ok && v == SecretMask {
				v = old
			}
			env[k] = v
		}
		svc.Env = env
	}
	if err := validateService(svc); err != nil {
		return ServiceView{}, err
	}
	if svc, err = c.store.UpdateService(ctx, svc); err != nil {
		return ServiceView{}, err
	}
	return c.view(ctx, svc)
}

func validateService(s store.Service) error {
	if _, err := reference.ParseNormalizedNamed(s.Image); err != nil {
		return fmt.Errorf("%w: image: %v", ErrInvalid, err)
	}
	if s.Replicas < 1 || s.Replicas > maxReplicas {
		return fmt.Errorf("%w: replicas must be between 1 and %d", ErrInvalid, maxReplicas)
	}
	if s.Domain != "" {
		if !domainRe.MatchString(s.Domain) {
			return fmt.Errorf("%w: domain %q is not a valid hostname", ErrInvalid, s.Domain)
		}
		if s.Port < 1 || s.Port > 65535 {
			return fmt.Errorf("%w: a public service needs the container port to route to", ErrInvalid)
		}
	} else if s.Port < 0 || s.Port > 65535 {
		return fmt.Errorf("%w: port out of range", ErrInvalid)
	}
	for k := range s.Env {
		if !envKeyRe.MatchString(k) {
			return fmt.Errorf("%w: invalid env var name %q", ErrInvalid, k)
		}
	}
	return nil
}

func (c *Core) GetService(ctx context.Context, id string) (ServiceView, error) {
	svc, err := c.store.GetService(ctx, id)
	if err != nil {
		return ServiceView{}, err
	}
	return c.view(ctx, svc)
}

// ListServices returns the project's services with their containers, using a
// single Docker call for the whole project.
func (c *Core) ListServices(ctx context.Context, projectID string) ([]ServiceView, error) {
	svcs, err := c.store.ListServices(ctx, projectID)
	if err != nil {
		return nil, err
	}
	cts, err := c.docker.ListContainers(ctx, map[string]string{docker.LabelProject: projectID})
	if err != nil {
		return nil, err
	}
	byService := map[string][]ContainerView{}
	for _, ct := range cts {
		sid := ct.Labels[docker.LabelService]
		byService[sid] = append(byService[sid], containerView(ct))
	}
	views := make([]ServiceView, len(svcs))
	for i, s := range svcs {
		views[i] = ServiceView{Service: masked(s), Containers: sortedContainers(byService[s.ID])}
	}
	return views, nil
}

func (c *Core) view(ctx context.Context, svc store.Service) (ServiceView, error) {
	cts, err := c.serviceContainers(ctx, svc)
	if err != nil {
		return ServiceView{}, err
	}
	views := make([]ContainerView, len(cts))
	for i, ct := range cts {
		views[i] = containerView(ct)
	}
	return ServiceView{Service: masked(svc), Containers: sortedContainers(views)}, nil
}

func (c *Core) serviceContainers(ctx context.Context, svc store.Service) ([]container.Summary, error) {
	return c.docker.ListContainers(ctx, map[string]string{
		docker.LabelProject: svc.ProjectID,
		docker.LabelService: svc.ID,
	})
}

func containerView(ct container.Summary) ContainerView {
	replica, _ := strconv.Atoi(ct.Labels[docker.LabelReplica])
	name := ""
	if len(ct.Names) > 0 {
		name = strings.TrimPrefix(ct.Names[0], "/")
	}
	return ContainerView{
		ID:       ct.ID[:12],
		Name:     name,
		Replica:  replica,
		DeployID: ct.Labels[docker.LabelDeploy],
		Image:    ct.Image,
		State:    string(ct.State),
		Status:   ct.Status,
	}
}

func sortedContainers(v []ContainerView) []ContainerView {
	if v == nil {
		return []ContainerView{}
	}
	slices.SortFunc(v, func(a, b ContainerView) int { return a.Replica - b.Replica })
	return v
}

func masked(s store.Service) store.Service {
	env := maps.Clone(s.Env)
	for k := range env {
		env[k] = SecretMask
	}
	if env == nil {
		env = map[string]string{}
	}
	s.Env = env
	return s
}

// DeleteService removes the service's containers, its record and deploy logs.
func (c *Core) DeleteService(ctx context.Context, id string) error {
	unlock, err := c.lockService(id)
	if err != nil {
		return err
	}
	defer unlock()

	svc, err := c.store.GetService(ctx, id)
	if err != nil {
		return err
	}
	cts, err := c.serviceContainers(ctx, svc)
	if err != nil {
		return err
	}
	for _, ct := range cts {
		if err := c.docker.RemoveContainer(ctx, ct.ID, stopTimeout); err != nil {
			return err
		}
	}
	if err := c.store.DeleteService(ctx, id); err != nil {
		return err
	}
	c.removeDeployLogs(id)
	return nil
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
	cts, err := c.serviceContainers(ctx, svc)
	if err != nil {
		return ServiceView{}, err
	}
	if len(cts) == 0 {
		return ServiceView{}, fmt.Errorf("%w: service has not been deployed yet", ErrInvalid)
	}
	secs := int(stopTimeout.Seconds())
	for _, ct := range cts {
		switch action {
		case ActionStart:
			if ct.State != container.StateRunning {
				_, err = c.docker.ContainerStart(ctx, ct.ID, client.ContainerStartOptions{})
			}
		case ActionStop:
			_, err = c.docker.ContainerStop(ctx, ct.ID, client.ContainerStopOptions{Timeout: &secs})
		case ActionRestart:
			_, err = c.docker.ContainerRestart(ctx, ct.ID, client.ContainerRestartOptions{Timeout: &secs})
		default:
			return ServiceView{}, fmt.Errorf("%w: unknown action %q", ErrInvalid, action)
		}
		if err != nil {
			return ServiceView{}, fmt.Errorf("%s %s: %w", action, ct.Names[0], err)
		}
	}
	return c.view(ctx, svc)
}
