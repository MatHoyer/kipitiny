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
	// Secrets names the write-only Env entries; nil makes them all secrets.
	Secrets    []string `json:"secrets"`
	MemoryMB   int      `json:"memoryMb"`
	HealthPath string   `json:"healthPath"`
	PreDeploy  string   `json:"preDeploy"`
	// Git source (Source "git"): Image is then ignored.
	Source       store.ServiceSource `json:"source"`
	GitURL       string              `json:"gitUrl"`
	GitBranch    string              `json:"gitBranch"`
	GitToken     string              `json:"gitToken"`
	Dockerfile   string              `json:"dockerfile"`
	BuildContext string              `json:"buildContext"`
}

// ServicePatch updates only the fields that are set. Env replaces the whole
// map; secrets equal to SecretMask keep their stored value. Secrets names the
// write-only entries; nil keeps the stored flags.
type ServicePatch struct {
	Image      *string           `json:"image"`
	Replicas   *int              `json:"replicas"`
	Port       *int              `json:"port"`
	Domain     *string           `json:"domain"`
	Env        map[string]string `json:"env"`
	Secrets    []string          `json:"secrets"`
	MemoryMB   *int              `json:"memoryMb"`
	HealthPath *string           `json:"healthPath"`
	PreDeploy  *string           `json:"preDeploy"`
	GitURL     *string           `json:"gitUrl"`
	GitBranch  *string           `json:"gitBranch"`
	// GitToken equal to SecretMask keeps the stored token.
	GitToken     *string `json:"gitToken"`
	Dockerfile   *string `json:"dockerfile"`
	BuildContext *string `json:"buildContext"`
}

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

type ServiceView struct {
	store.Service
	Containers []ContainerView `json:"containers"`
	// DNS is set when the manager manages the domain's Cloudflare record.
	DNS *DNSStatus `json:"dns,omitempty"`
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
		ProjectID:    projectID,
		Name:         in.Name,
		Kind:         in.Kind,
		Image:        strings.TrimSpace(in.Image),
		Replicas:     in.Replicas,
		Port:         in.Port,
		Domain:       strings.ToLower(strings.TrimSpace(in.Domain)),
		Env:          in.Env,
		MemoryMB:     in.MemoryMB,
		HealthPath:   strings.TrimSpace(in.HealthPath),
		PreDeploy:    strings.TrimSpace(in.PreDeploy),
		Source:       in.Source,
		GitURL:       strings.TrimSpace(in.GitURL),
		GitBranch:    strings.TrimSpace(in.GitBranch),
		GitToken:     strings.TrimSpace(in.GitToken),
		Dockerfile:   strings.Trim(strings.TrimSpace(in.Dockerfile), "/"),
		BuildContext: strings.Trim(strings.TrimSpace(in.BuildContext), "/"),
	}
	if svc.Source == "" {
		svc.Source = store.SourceImage
	}
	if svc.Source == store.SourceGit {
		svc.Image = "" // built on deploy
		if svc.GitBranch == "" {
			svc.GitBranch = "main"
		}
		if svc.Dockerfile == "" {
			svc.Dockerfile = "Dockerfile"
		}
		svc.WebhookSecret = randomToken(24)
	}
	if svc.Env == nil {
		svc.Env = map[string]string{}
	}
	env, secrets, envErr := mergeEnv(svc.Env, in.Secrets, nil, nil)
	if envErr != nil {
		return ServiceView{}, envErr
	}
	svc.Env, svc.Secrets = env, secrets
	if !nameRe.MatchString(svc.Name) {
		return ServiceView{}, fmt.Errorf("%w: name must be lowercase letters, digits and dashes (max 40)", ErrInvalid)
	}
	switch svc.Kind {
	case store.ServiceKindApp:
	case store.ServiceKindPostgres:
		if svc.Image == "" {
			svc.Image = DefaultPostgresImage
		}
		if svc.MemoryMB == 0 {
			svc.MemoryMB = defaultPostgresMemoryMB
		}
		// The env is generated and fixed: anything sent in is ignored.
		svc.Env, svc.Secrets = newPostgresEnv(), []string{pgPassword}
	default:
		return ServiceView{}, fmt.Errorf("%w: unknown service kind %q", ErrInvalid, svc.Kind)
	}
	if err := c.validate(ctx, svc); err != nil {
		return ServiceView{}, err
	}
	svc, err := c.store.CreateService(ctx, svc)
	if err != nil {
		return ServiceView{}, err
	}
	if svc.Domain != "" {
		c.kickDNS()
	}
	return ServiceView{Service: masked(svc), Containers: []ContainerView{}}, nil
}

func (c *Core) UpdateService(ctx context.Context, id string, p ServicePatch) (ServiceView, error) {
	old, err := c.store.GetService(ctx, id)
	if err != nil {
		return ServiceView{}, err
	}
	svc := old
	svc.Env = maps.Clone(old.Env)
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
	if p.Env != nil || p.Secrets != nil {
		in := p.Env
		if in == nil {
			in = maskEnv(svc.Env, svc.Secrets)
		}
		if svc.Env, svc.Secrets, err = mergeEnv(in, p.Secrets, old.Env, old.Secrets); err != nil {
			return ServiceView{}, err
		}
	}
	if p.MemoryMB != nil {
		svc.MemoryMB = *p.MemoryMB
	}
	if p.HealthPath != nil {
		svc.HealthPath = strings.TrimSpace(*p.HealthPath)
	}
	if p.PreDeploy != nil {
		svc.PreDeploy = strings.TrimSpace(*p.PreDeploy)
	}
	if svc.Source == store.SourceGit {
		if p.GitURL != nil {
			svc.GitURL = strings.TrimSpace(*p.GitURL)
		}
		if p.GitBranch != nil {
			svc.GitBranch = strings.TrimSpace(*p.GitBranch)
		}
		if p.GitToken != nil && *p.GitToken != SecretMask {
			svc.GitToken = strings.TrimSpace(*p.GitToken)
		}
		if p.Dockerfile != nil {
			svc.Dockerfile = strings.Trim(strings.TrimSpace(*p.Dockerfile), "/")
		}
		if p.BuildContext != nil {
			svc.BuildContext = strings.Trim(strings.TrimSpace(*p.BuildContext), "/")
		}
	}
	if svc.Kind == store.ServiceKindPostgres {
		if err := checkPostgresUpdate(old, svc); err != nil {
			return ServiceView{}, err
		}
	}
	if err := c.validate(ctx, svc); err != nil {
		return ServiceView{}, err
	}
	if svc, err = c.store.UpdateService(ctx, svc); err != nil {
		return ServiceView{}, err
	}
	if svc.Replicas != old.Replicas {
		c.kick() // scaling applies right away, other changes on the next deploy
	}
	if svc.Domain != old.Domain {
		c.kickDNS()
	}
	return c.view(ctx, svc)
}

// validate checks a service's fields and that its env references resolve.
func (c *Core) validate(ctx context.Context, s store.Service) error {
	if err := validateService(s); err != nil {
		return err
	}
	if s.Domain != "" && s.Domain == c.cfg.Domain {
		return fmt.Errorf("%w: domain %q is the manager's own", ErrInvalid, s.Domain)
	}
	project, err := c.store.GetProject(ctx, s.ProjectID)
	if err != nil {
		return err
	}
	dbs, err := c.projectDatabases(ctx, s.ProjectID)
	if err != nil {
		return err
	}
	if err := c.checkSecretSchemes(s.Env); err != nil {
		return err
	}
	return checkRefs(s.Env, envSources{project: project.Env, dbs: dbs})
}

func validateService(s store.Service) error {
	switch s.Source {
	case store.SourceGit:
		if s.Kind != store.ServiceKindApp {
			return fmt.Errorf("%w: only apps can be built from git", ErrInvalid)
		}
		if err := validateGit(s); err != nil {
			return err
		}
	default:
		if _, err := reference.ParseNormalizedNamed(s.Image); err != nil {
			return fmt.Errorf("%w: image: %v", ErrInvalid, err)
		}
	}
	for k := range s.Env {
		if !envKeyRe.MatchString(k) {
			return fmt.Errorf("%w: invalid env var name %q", ErrInvalid, k)
		}
	}
	if s.MemoryMB < 0 {
		return fmt.Errorf("%w: memory must be positive", ErrInvalid)
	}
	if s.Kind == store.ServiceKindPostgres {
		if s.HealthPath != "" || s.PreDeploy != "" {
			return fmt.Errorf("%w: databases have a built-in healthcheck and no pre-deploy command", ErrInvalid)
		}
		return validatePostgres(s)
	}
	if s.HealthPath != "" {
		if !strings.HasPrefix(s.HealthPath, "/") || strings.ContainsAny(s.HealthPath, " \t\n") {
			return fmt.Errorf("%w: health path must start with / (e.g. /healthz)", ErrInvalid)
		}
		if s.Port == 0 {
			return fmt.Errorf("%w: a health path needs the container port", ErrInvalid)
		}
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
	project, err := c.store.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	svcs, err := c.store.ListServices(ctx, projectID)
	if err != nil {
		return nil, err
	}
	cts, err := c.dockerFor(project.ServerID).ListContainers(ctx, map[string]string{docker.LabelProject: projectID})
	if err != nil {
		return nil, err
	}
	svcByID := map[string]store.Service{}
	for _, s := range svcs {
		svcByID[s.ID] = s
	}
	byService := map[string][]ContainerView{}
	for _, ct := range cts {
		sid := ct.Labels[docker.LabelService]
		if s, ok := svcByID[sid]; ok {
			byService[sid] = append(byService[sid], containerView(ct, s))
		}
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
		views[i] = containerView(ct, svc)
	}
	v := ServiceView{Service: masked(svc), Containers: sortedContainers(views)}
	if svc.Domain != "" {
		v.DNS = c.dnsStatus(ctx, svc.Domain)
	}
	return v, nil
}

func (c *Core) serviceContainers(ctx context.Context, svc store.Service) ([]container.Summary, error) {
	return c.dockerFor(svc.ServerID).ListContainers(ctx, map[string]string{
		docker.LabelProject: svc.ProjectID,
		docker.LabelService: svc.ID,
	})
}

// activeContainers returns the containers of the deployment serving traffic.
func (c *Core) activeContainers(ctx context.Context, svc store.Service) ([]container.Summary, error) {
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

func containerView(ct container.Summary, svc store.Service) ContainerView {
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

func masked(s store.Service) store.Service {
	if s.GitToken != "" {
		s.GitToken = SecretMask
	}
	s.Env = maskEnv(s.Env, s.Secrets)
	if s.Secrets == nil {
		s.Secrets = []string{}
	}
	return s
}

// DeleteService removes the service's containers, its record and deploy logs.
// Deleting a database also destroys its data volume, so confirm must repeat
// the service name.
func (c *Core) DeleteService(ctx context.Context, id, confirm string) error {
	unlock, err := c.lockService(id)
	if err != nil {
		return err
	}
	defer unlock()

	svc, err := c.store.GetService(ctx, id)
	if err != nil {
		return err
	}
	if svc.Kind == store.ServiceKindPostgres {
		if confirm != svc.Name {
			return fmt.Errorf("%w: deleting a database destroys its data; confirm with the service name", ErrInvalid)
		}
		siblings, err := c.store.ListServices(ctx, svc.ProjectID)
		if err != nil {
			return err
		}
		for _, s := range siblings {
			if s.ID != svc.ID && usesDatabase(s.Env, svc.Name) {
				return fmt.Errorf("%w: database is used by %q; remove its references first", ErrInvalid, s.Name)
			}
		}
	}
	if err := c.removeServiceContainers(ctx, svc); err != nil {
		return err
	}
	if err := c.store.DeleteService(ctx, id); err != nil {
		return err
	}
	c.kickDNS()
	c.removeDeployLogs(id)
	return nil
}

// removeServiceContainers removes containers and, for databases, the volume.
func (c *Core) removeServiceContainers(ctx context.Context, svc store.Service) error {
	cts, err := c.serviceContainers(ctx, svc)
	if err != nil {
		return err
	}
	dk := c.dockerFor(svc.ServerID)
	for _, ct := range cts {
		if err := dk.RemoveContainer(ctx, ct.ID, stopTimeoutFor(svc)); err != nil {
			return err
		}
	}
	if svc.Kind == store.ServiceKindPostgres {
		return dk.RemoveVolume(ctx, PostgresVolume(svc.ID))
	}
	if svc.Source == store.SourceGit {
		return dk.RemoveImagesByLabel(ctx, docker.LabelService, svc.ID)
	}
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
	svc.Stopped = action == ActionStop
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
