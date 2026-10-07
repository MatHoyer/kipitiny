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

const (
	maxReplicas = 10
	// maxStopGraceSeconds bounds how long a stop (and so a deploy) may wait.
	maxStopGraceSeconds = 600
	// Docker's NanoCPUs granularity is 0.01 CPU in practice.
	minCPUs = 0.01
	maxCPUs = 256.0
)

var (
	domainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$|^localhost$|^([a-z0-9-]+\.)+localhost$`)
	envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	iconRe   = regexp.MustCompile(`^([a-z0-9][a-z0-9-]{0,39})?$`)
)

type ServiceInput struct {
	Name     string            `json:"name"`
	Kind     store.ServiceKind `json:"kind"`
	Image    string            `json:"image"`
	Icon     string            `json:"icon"`
	Replicas int               `json:"replicas"`
	Port     int               `json:"port"`
	Domain   string            `json:"domain"`
	Env      map[string]string `json:"env"`
	// Secrets names the write-only Env entries; nil makes them all secrets.
	Secrets    []string `json:"secrets"`
	MemoryMB   int      `json:"memoryMb"`
	CPUs       float64  `json:"cpus"`
	HealthPath string   `json:"healthPath"`
	PreDeploy  string   `json:"preDeploy"`
	// Volumes are named volumes mounted in every replica (apps only).
	Volumes []store.Volume `json:"volumes"`
	// Middlewares apply to a public app's requests (apps only).
	Middlewares *MiddlewaresInput `json:"middlewares"`
	PreBackup   string            `json:"preBackup"`
	// PublishedPorts bind host ports to the app's container (apps only).
	PublishedPorts []store.PublishedPort `json:"publishedPorts"`
	// StopGraceSeconds is the app's time to exit on stop; 0 means 10 s.
	StopGraceSeconds int `json:"stopGraceSeconds"`
	// Password is a new database's password, from a compose export; empty
	// generates one. Not settable through the API.
	Password string `json:"-"`
}

// ServicePatch updates only the fields that are set. Env replaces the whole
// map; secrets equal to SecretMask keep their stored value. Secrets names the
// write-only entries; nil keeps the stored flags.
type ServicePatch struct {
	Image      *string           `json:"image"`
	Icon       *string           `json:"icon"`
	Replicas   *int              `json:"replicas"`
	Port       *int              `json:"port"`
	Domain     *string           `json:"domain"`
	Env        map[string]string `json:"env"`
	Secrets    []string          `json:"secrets"`
	MemoryMB   *int              `json:"memoryMb"`
	CPUs       *float64          `json:"cpus"`
	HealthPath *string           `json:"healthPath"`
	PreDeploy  *string           `json:"preDeploy"`
	// Volumes replaces the app's volumes; nil keeps them.
	Volumes []store.Volume `json:"volumes"`
	// Middlewares replaces the app's middlewares; nil keeps them.
	Middlewares *MiddlewaresInput `json:"middlewares"`
	PreBackup   *string           `json:"preBackup"`
	// PublishedPorts replaces the app's published ports; nil keeps them.
	PublishedPorts   []store.PublishedPort `json:"publishedPorts"`
	StopGraceSeconds *int                  `json:"stopGraceSeconds"`
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
	if err := c.checkGitOwned(ctx, projectID); err != nil {
		return ServiceView{}, err
	}
	svc, err := c.serviceFromInput(projectID, in)
	if err != nil {
		return ServiceView{}, err
	}
	if err := c.validate(ctx, svc); err != nil {
		return ServiceView{}, err
	}
	svc, err = c.store.CreateService(ctx, svc)
	if err != nil {
		return ServiceView{}, err
	}
	if svc.Domain != "" {
		c.kickDNS()
	}
	return ServiceView{Service: masked(svc), Containers: []ContainerView{}}, nil
}

// serviceFromInput builds a new service, with the kind's defaults, before
// validation.
func (c *Core) serviceFromInput(projectID string, in ServiceInput) (store.Service, error) {
	if in.Kind == "" {
		in.Kind = store.ServiceKindApp
	}
	if in.Replicas == 0 {
		in.Replicas = 1
	}
	svc := store.Service{
		ProjectID:        projectID,
		Name:             in.Name,
		Kind:             in.Kind,
		Image:            strings.TrimSpace(in.Image),
		Icon:             strings.TrimSpace(in.Icon),
		Replicas:         in.Replicas,
		Port:             in.Port,
		Domain:           strings.ToLower(strings.TrimSpace(in.Domain)),
		Env:              in.Env,
		MemoryMB:         in.MemoryMB,
		CPUs:             in.CPUs,
		HealthPath:       strings.TrimSpace(in.HealthPath),
		PreDeploy:        strings.TrimSpace(in.PreDeploy),
		Volumes:          normalizeVolumes(in.Volumes),
		PreBackup:        strings.TrimSpace(in.PreBackup),
		PublishedPorts:   normalizePorts(in.PublishedPorts),
		StopGraceSeconds: in.StopGraceSeconds,
	}
	if svc.Env == nil {
		svc.Env = map[string]string{}
	}
	env, secrets, envErr := mergeEnv(svc.Env, in.Secrets, nil, nil)
	if envErr != nil {
		return store.Service{}, envErr
	}
	svc.Env, svc.Secrets = env, secrets
	if in.Middlewares != nil {
		mw, err := mergeMiddlewares(*in.Middlewares, store.Middlewares{})
		if err != nil {
			return store.Service{}, err
		}
		svc.Middlewares = mw
	}
	if !nameRe.MatchString(svc.Name) {
		return store.Service{}, fmt.Errorf("%w: name must be lowercase letters, digits and dashes (max 40)", ErrInvalid)
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
		if in.Password != "" {
			svc.Env[pgPassword] = in.Password
		}
	case store.ServiceKindRedis:
		if svc.Image == "" {
			svc.Image = DefaultRedisImage
		}
		if svc.MemoryMB == 0 {
			svc.MemoryMB = defaultRedisMemoryMB
		}
		svc.Env, svc.Secrets = newRedisEnv(), []string{redisPassword}
		if in.Password != "" {
			svc.Env[redisPassword] = in.Password
		}
	default:
		return store.Service{}, fmt.Errorf("%w: unknown service kind %q", ErrInvalid, svc.Kind)
	}
	return svc, nil
}

func (c *Core) UpdateService(ctx context.Context, id string, p ServicePatch) (ServiceView, error) {
	svc, err := c.updateService(ctx, id, p)
	if err != nil {
		return ServiceView{}, err
	}
	return c.view(ctx, svc)
}

func (c *Core) updateService(ctx context.Context, id string, p ServicePatch) (store.Service, error) {
	old, err := c.store.GetService(ctx, id)
	if err != nil {
		return store.Service{}, err
	}
	if err := c.checkGitOwned(ctx, old.ProjectID); err != nil {
		return store.Service{}, err
	}
	svc, err := c.patched(old, p)
	if err != nil {
		return store.Service{}, err
	}
	if err := c.validate(ctx, svc); err != nil {
		return store.Service{}, err
	}
	if svc, err = c.store.UpdateService(ctx, svc); err != nil {
		return store.Service{}, err
	}
	if svc.Replicas != old.Replicas {
		c.kick() // scaling applies right away, other changes on the next deploy
	}
	if svc.Domain != old.Domain {
		c.kickDNS()
	}
	return svc, nil
}

// patched is old with p applied, before validation.
func (c *Core) patched(old store.Service, p ServicePatch) (store.Service, error) {
	var err error
	svc := old
	svc.Env = maps.Clone(old.Env)
	if p.Image != nil {
		svc.Image = strings.TrimSpace(*p.Image)
	}
	if p.Icon != nil {
		svc.Icon = strings.TrimSpace(*p.Icon)
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
			return store.Service{}, err
		}
	}
	if p.MemoryMB != nil {
		svc.MemoryMB = *p.MemoryMB
	}
	if p.CPUs != nil {
		svc.CPUs = *p.CPUs
	}
	if p.HealthPath != nil {
		svc.HealthPath = strings.TrimSpace(*p.HealthPath)
	}
	if p.PreDeploy != nil {
		svc.PreDeploy = strings.TrimSpace(*p.PreDeploy)
	}
	if p.Volumes != nil {
		svc.Volumes = normalizeVolumes(p.Volumes)
	}
	if p.PreBackup != nil {
		svc.PreBackup = strings.TrimSpace(*p.PreBackup)
	}
	if p.PublishedPorts != nil {
		svc.PublishedPorts = normalizePorts(p.PublishedPorts)
	}
	if p.StopGraceSeconds != nil {
		svc.StopGraceSeconds = *p.StopGraceSeconds
	}
	if p.Middlewares != nil {
		if svc.Middlewares, err = mergeMiddlewares(*p.Middlewares, old.Middlewares); err != nil {
			return store.Service{}, err
		}
	}
	if svc.Kind.IsDatabase() {
		if err := checkDatabaseUpdate(old, svc); err != nil {
			return store.Service{}, err
		}
	}
	return svc, nil
}

// validate checks a service's fields and that its env references resolve.
func (c *Core) validate(ctx context.Context, s store.Service) error {
	project, err := c.store.GetProject(ctx, s.ProjectID)
	if err != nil {
		return err
	}
	dbs, err := c.projectDatabases(ctx, s.ProjectID)
	if err != nil {
		return err
	}
	return c.validateIn(ctx, s, project, dbs)
}

// validateIn is validate against a project's variables and databases as
// they will be (ApplyCompose checks a whole file before changing anything).
func (c *Core) validateIn(ctx context.Context, s store.Service, project store.Project, dbs map[string]store.Service) error {
	if err := validateService(s); err != nil {
		return err
	}
	if s.Domain != "" && s.Domain == c.cfg.Domain {
		return fmt.Errorf("%w: domain %q is the manager's own", ErrInvalid, s.Domain)
	}
	if err := c.checkSecretSchemes(s.Env); err != nil {
		return err
	}
	if err := c.checkBasicAuthSchemes(s.Middlewares); err != nil {
		return err
	}
	if err := c.checkPortsFree(ctx, s); err != nil {
		return err
	}
	// Its record points at the server, which publishes no HTTP port behind
	// a tunnel.
	if httpRouted(s) && len(s.PublishedPorts) > 0 && c.viaTunnel(ctx, project.ServerID) {
		return fmt.Errorf("%w: behind a Cloudflare tunnel, an app with published ports can't also route HTTP on its domain: "+
			"its record must point at the server; clear the container port, or serve HTTP from another service", ErrInvalid)
	}
	return checkRefs(s.Env, envSources{project: project.Env, dbs: dbs})
}

// IconHint names the logo for svc: its icon, else its image's base name
// (ghcr.io/n8n-io/n8n:1 → n8n). The UI shows the logo if it knows the name,
// else one for the kind. Backups keep it, as they outlive the service.
func IconHint(svc store.Service) string {
	if svc.Icon != "" {
		return svc.Icon
	}
	named, err := reference.ParseNormalizedNamed(svc.Image)
	if err != nil {
		return ""
	}
	p := reference.Path(named)
	return p[strings.LastIndex(p, "/")+1:]
}

func validateService(s store.Service) error {
	if _, err := reference.ParseNormalizedNamed(s.Image); err != nil {
		return fmt.Errorf("%w: image: %v", ErrInvalid, err)
	}
	if !iconRe.MatchString(s.Icon) {
		return fmt.Errorf("%w: icon must be lowercase letters, digits and dashes", ErrInvalid)
	}
	for k := range s.Env {
		if !envKeyRe.MatchString(k) {
			return fmt.Errorf("%w: invalid env var name %q", ErrInvalid, k)
		}
	}
	if s.MemoryMB < 0 {
		return fmt.Errorf("%w: memory must be positive", ErrInvalid)
	}
	if s.CPUs != 0 && (s.CPUs < minCPUs || s.CPUs > maxCPUs) {
		return fmt.Errorf("%w: CPU limit must be between %g and %g cores", ErrInvalid, minCPUs, maxCPUs)
	}
	if s.Kind.IsDatabase() {
		if len(s.Volumes) > 0 || s.PreBackup != "" {
			return fmt.Errorf("%w: databases keep their data in their own volume, backed up as is", ErrInvalid)
		}
		if !s.Middlewares.IsZero() || len(s.PublishedPorts) > 0 {
			return fmt.Errorf("%w: databases are never public, so take no middlewares or published ports", ErrInvalid)
		}
		if s.StopGraceSeconds != 0 {
			return fmt.Errorf("%w: databases have their own stop timeout", ErrInvalid)
		}
		return validateDatabase(s)
	}
	if err := validateVolumes(s.Volumes); err != nil {
		return err
	}
	if err := validateMiddlewares(s.Middlewares); err != nil {
		return err
	}
	if err := validatePorts(s); err != nil {
		return err
	}
	if s.StopGraceSeconds < 0 || s.StopGraceSeconds > maxStopGraceSeconds {
		return fmt.Errorf("%w: stop grace period must be between 0 and %d seconds", ErrInvalid, maxStopGraceSeconds)
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
		// With published ports and no container port, the domain only gets
		// a DNS record.
		if (s.Port < 1 && len(s.PublishedPorts) == 0) || s.Port < 0 || s.Port > 65535 {
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
	s.Env = maskEnv(s.Env, s.Secrets)
	if s.Secrets == nil {
		s.Secrets = []string{}
	}
	if s.Volumes == nil {
		s.Volumes = []store.Volume{}
	}
	if s.PublishedPorts == nil {
		s.PublishedPorts = []store.PublishedPort{}
	}
	s.Middlewares = maskMiddlewares(s.Middlewares)
	return s
}

// DeleteService removes the service's containers, its record and deploy logs.
// Deleting a database or an app with volumes also destroys its data, so
// confirm must repeat the service name.
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
	if !svc.Orphaned {
		if err := c.checkGitOwned(ctx, svc.ProjectID); err != nil {
			return err
		}
	}
	if hasData(svc) && confirm != svc.Name {
		return fmt.Errorf("%w: deleting %s destroys its data; confirm with the service name", ErrInvalid, svc.Name)
	}
	if svc.Kind.IsDatabase() {
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

// removeServiceContainers removes containers and the service's volumes.
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
	return removeServiceVolumes(ctx, dk, svc)
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
