package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kipit-labs/kipitiny/internal/anonymize"
	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
)

type ServiceInput struct {
	Name     string             `json:"name"`
	Kind     domain.ServiceKind `json:"kind"`
	Image    string             `json:"image"`
	Icon     string             `json:"icon"`
	Tags     []string           `json:"tags"`
	Replicas int                `json:"replicas"`
	Port     int                `json:"port"`
	Domain   string             `json:"domain"`
	// Analytics counts the domain's visits (apps with a domain only).
	Analytics bool `json:"analytics"`
	// Errors takes the errors of the app's Sentry SDK (apps only).
	Errors bool              `json:"errors"`
	Env    map[string]string `json:"env"`
	// Secrets names the write-only Env entries; nil makes them all secrets.
	Secrets    []string `json:"secrets"`
	MemoryMB   int      `json:"memoryMb"`
	CPUs       float64  `json:"cpus"`
	HealthPath string   `json:"healthPath"`
	// Healthcheck replaces the image's own and the injected probe (apps only).
	Healthcheck domain.Healthcheck `json:"healthcheck"`
	PreDeploy   string             `json:"preDeploy"`
	// Volumes are named volumes mounted in every replica (apps only).
	Volumes []domain.Volume `json:"volumes"`
	// Middlewares apply to a public app's requests (apps only).
	Middlewares *network.MiddlewaresInput `json:"middlewares"`
	PreBackup   string                    `json:"preBackup"`
	// PublishedPorts bind host ports to the app's container (apps only).
	PublishedPorts []domain.PublishedPort `json:"publishedPorts"`
	// Routes send some paths of the domain to the app (apps only).
	Routes []domain.Route `json:"routes"`
	// StopGraceSeconds is the app's time to exit on stop; 0 means 10 s.
	StopGraceSeconds int `json:"stopGraceSeconds"`
	// HostNetwork runs the app in the host's network (apps only).
	HostNetwork bool `json:"hostNetwork"`
	// DockerSocket mounts the host's Docker socket: "", "ro" or "rw".
	DockerSocket string `json:"dockerSocket"`
	// Runtime holds command, entrypoint, user, tmpfs… (apps only).
	Runtime domain.Runtime `json:"runtime"`
	// Files are written into the app's containers (apps only).
	Files []domain.ServiceFile `json:"files"`
	// DependsOn names services of the project started first (apps only).
	DependsOn []domain.Dependency `json:"dependsOn"`
	// ProjectNetworks are the project's own networks it's on; empty is the
	// default one.
	ProjectNetworks []string `json:"projectNetworks"`
	// Preview is "only" (deployed in previews alone), "skip" (left out of
	// previews) or "" (both).
	Preview string `json:"preview"`
	// Password is a new database's password, from a compose export; empty
	// generates one. Not settable through the API.
	Password string `json:"-"`
	// Anonymize are a database's rules for anonymized preview seeds, from
	// a compose file; the preview settings set them otherwise.
	Anonymize anonymize.Rules `json:"-"`
	// networks are the networks created by hand it joins, from a compose
	// file whose service lists networks; nil keeps them.
	networks []string
}

type ServiceView struct {
	domain.Service
	Containers []ContainerView `json:"containers"`
	// DNS is set when the manager manages the domain's Cloudflare record.
	DNS *network.DNSStatus `json:"dns,omitempty"`
	// RoutePending is set when the domain or port changed since the running
	// deployment: Traefik only routes the new one after a deploy.
	RoutePending bool `json:"routePending,omitempty"`
	// NetworkIssues are the networks its running containers miss, e.g. one
	// removed outside kipitiny; the reconciler attaches them again.
	NetworkIssues []string `json:"networkIssues,omitempty"`
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
	if err := checkServiceChange(ctx, domain.Service{ProjectID: projectID, Env: map[string]string{}, Secrets: []string{}}, svc); err != nil {
		return ServiceView{}, err
	}
	if err := c.validate(ctx, svc); err != nil {
		return ServiceView{}, err
	}
	if err := c.ErrorsIdentity(ctx, &svc); err != nil {
		return ServiceView{}, err
	}
	if svc.Analytics && svc.Domain != "" {
		svc.AnalyticsSince = time.Now().UTC().Truncate(time.Microsecond) // as stored
	}
	svc, err = c.store.CreateService(ctx, svc)
	if err != nil {
		return ServiceView{}, err
	}
	if svc.Domain != "" {
		c.KickDNS()
	}
	return ServiceView{Service: masked(svc), Containers: []ContainerView{}}, nil
}

// serviceFromInput builds a new service, with the kind's defaults, before
// validation.
func (c *Core) serviceFromInput(projectID string, in ServiceInput) (domain.Service, error) {
	if in.Kind == "" {
		in.Kind = domain.ServiceKindApp
	}
	if in.Replicas == 0 {
		in.Replicas = 1
	}
	svc := domain.Service{
		ProjectID:        projectID,
		Name:             in.Name,
		Kind:             in.Kind,
		Image:            strings.TrimSpace(in.Image),
		Icon:             strings.TrimSpace(in.Icon),
		Replicas:         in.Replicas,
		Port:             in.Port,
		Domain:           strings.ToLower(strings.TrimSpace(in.Domain)),
		Analytics:        in.Analytics,
		Errors:           in.Errors,
		Env:              in.Env,
		MemoryMB:         in.MemoryMB,
		CPUs:             in.CPUs,
		HealthPath:       strings.TrimSpace(in.HealthPath),
		Healthcheck:      in.Healthcheck,
		PreDeploy:        strings.TrimSpace(in.PreDeploy),
		Volumes:          normalizeVolumes(in.Volumes),
		PreBackup:        strings.TrimSpace(in.PreBackup),
		PublishedPorts:   network.NormalizePorts(in.PublishedPorts),
		Routes:           network.NormalizeRoutes(in.Routes),
		StopGraceSeconds: in.StopGraceSeconds,
		HostNetwork:      in.HostNetwork,
		DockerSocket:     in.DockerSocket,
		Runtime:          normalizeRuntime(in.Runtime),
		Files:            normalizeFiles(in.Files, nil),
		DependsOn:        in.DependsOn,
		ProjectNetworks:  normalizeProjectNetworks(in.ProjectNetworks),
		Preview:          in.Preview,
	}
	if svc.Env == nil {
		svc.Env = map[string]string{}
	}
	tags, err := normalizeTags(in.Tags)
	if err != nil {
		return domain.Service{}, err
	}
	svc.Tags = tags
	svc.Anonymize = in.Anonymize
	env, secrets, envErr := mergeEnv(svc.Env, in.Secrets, nil, nil)
	if envErr != nil {
		return domain.Service{}, envErr
	}
	svc.Env, svc.Secrets = env, secrets
	if in.Middlewares != nil {
		mw, err := network.MergeMiddlewares(*in.Middlewares, domain.Middlewares{})
		if err != nil {
			return domain.Service{}, err
		}
		svc.Middlewares = mw
	}
	if !domain.ValidName(svc.Name) {
		return domain.Service{}, fmt.Errorf("%w: name must be lowercase letters, digits and dashes (max 40)", ErrInvalid)
	}
	switch svc.Kind {
	case domain.ServiceKindApp:
	case domain.ServiceKindPostgres:
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
	case domain.ServiceKindRedis:
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
	case domain.ServiceKindMySQL, domain.ServiceKindMariaDB:
		if svc.Image == "" {
			svc.Image = defaultMySQLImage(svc.Kind)
		}
		if svc.MemoryMB == 0 {
			svc.MemoryMB = defaultMySQLMemoryMB
		}
		svc.Env, svc.Secrets = newMySQLEnv(), []string{mysqlPassword, mysqlRootPassword}
		if in.Password != "" {
			svc.Env[mysqlPassword] = in.Password
		}
	case domain.ServiceKindMongoDB:
		if svc.Image == "" {
			svc.Image = DefaultMongoImage
		}
		if svc.MemoryMB == 0 {
			svc.MemoryMB = defaultMongoMemoryMB
		}
		svc.Env, svc.Secrets = newMongoEnv(), []string{mongoPassword}
		if in.Password != "" {
			svc.Env[mongoPassword] = in.Password
		}
	default:
		return domain.Service{}, fmt.Errorf("%w: unknown service kind %q", ErrInvalid, svc.Kind)
	}
	if svc.Kind.IsDatabase() {
		for _, k := range databaseNameKeys {
			if _, generated := svc.Env[k]; generated && in.Env[k] != "" {
				svc.Env[k] = in.Env[k]
			}
		}
	}
	return svc, nil
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
	svcByID := map[string]domain.Service{}
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
		views[i] = ServiceView{Service: masked(s), Containers: sortedContainers(byService[s.ID]), RoutePending: c.routePending(ctx, s)}
		if s.Domain != "" {
			views[i].DNS = c.DNSStatusOf(ctx, s.Domain)
		}
	}
	return views, nil
}

func (c *Core) view(ctx context.Context, svc domain.Service) (ServiceView, error) {
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
		v.DNS = c.DNSStatusOf(ctx, svc.Domain)
	}
	v.RoutePending = c.routePending(ctx, svc)
	if p, err := c.store.GetProject(ctx, svc.ProjectID); err == nil {
		v.NetworkIssues = networkIssues(p, svc, cts)
	}
	return v, nil
}

// routePending reports whether svc's domain or port differ from what its
// running deployment routes.
func (c *Core) routePending(ctx context.Context, svc domain.Service) bool {
	if svc.Kind != domain.ServiceKindApp || svc.Stopped || svc.CurrentDeploymentID == "" {
		return false
	}
	d, err := c.store.GetDeployment(ctx, svc.CurrentDeploymentID)
	if err != nil || d.Config.ID == "" { // older deployments didn't keep their config
		return false
	}
	return d.Config.Domain != svc.Domain || d.Config.Port != svc.Port || !network.SameRoutes(d.Config.Routes, svc.Routes)
}

func masked(s domain.Service) domain.Service {
	s.Env = maskEnv(s.Env, s.Secrets)
	if s.Secrets == nil {
		s.Secrets = []string{}
	}
	if s.Volumes == nil {
		s.Volumes = []domain.Volume{}
	}
	if s.PublishedPorts == nil {
		s.PublishedPorts = []domain.PublishedPort{}
	}
	files := make([]domain.ServiceFile, len(s.Files))
	for i, f := range s.Files {
		f.Content = nil // can be large, and secret: Size says how large
		files[i] = f
	}
	s.Files = files
	s.Middlewares = network.MaskMiddlewares(s.Middlewares)
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
	c.KickDNS()
	c.removeDeployLogs(id)
	c.forgetServiceJobs(id)
	return nil
}

// removeServiceContainers removes containers and the service's volumes.
func (c *Core) removeServiceContainers(ctx context.Context, svc domain.Service) error {
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
	if err := c.removeJobContainers(ctx, dk, svc.ID); err != nil {
		return err
	}
	c.closeFilesHelper(svc.ID) // it holds the volumes
	return removeServiceVolumes(ctx, dk, svc)
}

// errPreviewOnly refuses running a previews-only service in its project.
func errPreviewOnly(svc domain.Service) error {
	return fmt.Errorf("%w: %s runs in previews only", ErrInvalid, svc.Name)
}
