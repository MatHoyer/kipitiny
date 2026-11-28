package deploy

import (
	"context"
	"errors"
	"maps"
	"strings"
	"time"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/domain/anonymize"
	"github.com/kipit-labs/kipitiny/internal/usecase/data"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
)

// ServicePatch updates only the fields that are set. Env replaces the whole
// map; secrets equal to SecretMask keep their stored value. Secrets names the
// write-only entries; nil keeps the stored flags.
type ServicePatch struct {
	Image *string `json:"image"`
	Icon  *string `json:"icon"`
	// Tags replaces the service's tags; nil keeps them.
	Tags      []string `json:"tags"`
	Replicas  *int     `json:"replicas"`
	Port      *int     `json:"port"`
	Domain    *string  `json:"domain"`
	Analytics *bool    `json:"analytics"`
	Errors    *bool    `json:"errors"`
	// AnalyticsScript and AnalyticsHit set the app's own tracker paths,
	// both or neither (empty: the analytics domain's).
	AnalyticsScript *string           `json:"analyticsScript"`
	AnalyticsHit    *string           `json:"analyticsHit"`
	Env             map[string]string `json:"env"`
	Secrets         []string          `json:"secrets"`
	MemoryMB        *int              `json:"memoryMb"`
	CPUs            *float64          `json:"cpus"`
	HealthPath      *string           `json:"healthPath"`
	// Healthcheck replaces the app's healthcheck; nil keeps it.
	Healthcheck *domain.Healthcheck `json:"healthcheck"`
	PreDeploy   *string             `json:"preDeploy"`
	// Volumes replaces the app's volumes; nil keeps them.
	Volumes []domain.Volume `json:"volumes"`
	// Middlewares replaces the app's middlewares; nil keeps them.
	Middlewares *network.MiddlewaresInput `json:"middlewares"`
	PreBackup   *string                   `json:"preBackup"`
	// PublishedPorts replaces the app's published ports; nil keeps them.
	PublishedPorts []domain.PublishedPort `json:"publishedPorts"`
	// Routes replaces the app's routes; nil keeps them.
	Routes           []domain.Route `json:"routes"`
	StopGraceSeconds *int           `json:"stopGraceSeconds"`
	HostNetwork      *bool          `json:"hostNetwork"`
	DockerSocket     *string        `json:"dockerSocket"`
	// Runtime replaces the app's runtime settings; nil keeps them.
	Runtime *domain.Runtime `json:"runtime"`
	// Files replaces the app's files; nil keeps them. A file without its
	// content keeps the stored one.
	Files []domain.ServiceFile `json:"files"`
	// DependsOn replaces the services the app starts after; nil keeps them.
	DependsOn []domain.Dependency `json:"dependsOn"`
	// ProjectNetworks replaces the project networks it's on; nil keeps them.
	ProjectNetworks []string `json:"projectNetworks"`
	Preview         *string  `json:"preview"`
	// Anonymize replaces a database's anonymize rules (from a compose
	// file); nil keeps them.
	Anonymize *anonymize.Rules `json:"-"`
}

func (c *Deployer) UpdateService(ctx context.Context, id string, p ServicePatch) (ServiceView, error) {
	svc, err := c.updateService(ctx, id, p)
	if err != nil {
		return ServiceView{}, err
	}
	return c.view(ctx, svc)
}

func (c *Deployer) updateService(ctx context.Context, id string, p ServicePatch) (domain.Service, error) {
	old, err := c.store.GetService(ctx, id)
	if err != nil {
		return domain.Service{}, err
	}
	if err := c.mgr.CheckGitOwned(ctx, old.ProjectID); err != nil {
		return domain.Service{}, err
	}
	svc, err := c.patched(old, p)
	if err != nil {
		return domain.Service{}, err
	}
	if err := checkServiceChange(ctx, old, svc); err != nil {
		return domain.Service{}, err
	}
	if err := c.validate(ctx, svc); err != nil {
		return domain.Service{}, err
	}
	if err := c.mgr.ErrorsIdentity(ctx, &svc); err != nil {
		return domain.Service{}, err
	}
	if svc.Domain != old.Domain || svc.Analytics && !old.Analytics {
		svc.AnalyticsSince = time.Now().UTC().Truncate(time.Microsecond) // as stored
	}
	if svc, err = c.store.UpdateService(ctx, svc); err != nil {
		return domain.Service{}, err
	}
	switch {
	case svc.Preview == "only" && old.Preview != "only":
		// Its previews' copy only: the project's own containers stop for good.
		if err := c.store.SetServiceStopped(ctx, svc.ID, true); err != nil {
			return domain.Service{}, err
		}
		if _, err := c.ServiceAction(ctx, svc.ID, ActionStop); err != nil && !errors.Is(err, ErrInvalid) {
			return domain.Service{}, err
		}
		svc.Stopped = true
	case old.Preview == "only" && svc.Preview != "only":
		if err := c.store.SetServiceStopped(ctx, svc.ID, false); err != nil {
			return domain.Service{}, err
		}
		svc.Stopped = false
	}
	if svc.Replicas != old.Replicas {
		c.mgr.Kick() // scaling applies right away, other changes on the next deploy
	}
	if svc.Domain != old.Domain {
		c.mgr.KickDNS()
	}
	if svc.Analytics != old.Analytics || svc.Analytics && (svc.Domain != old.Domain ||
		svc.AnalyticsScript != old.AnalyticsScript || svc.AnalyticsHit != old.AnalyticsHit) {
		c.mgr.Kick() // the reconciler sends the analytics server its hostnames and paths
	}
	if svc.Errors != old.Errors {
		c.mgr.Kick() // and the DSN keys
	}
	return svc, nil
}

// patched is old with p applied, before validation.
func (c *Deployer) patched(old domain.Service, p ServicePatch) (domain.Service, error) {
	var err error
	svc := old
	svc.Env = maps.Clone(old.Env)
	if p.Image != nil {
		svc.Image = strings.TrimSpace(*p.Image)
	}
	if p.Icon != nil {
		svc.Icon = strings.TrimSpace(*p.Icon)
	}
	if p.Tags != nil {
		if svc.Tags, err = domain.NormalizeTags(p.Tags); err != nil {
			return domain.Service{}, err
		}
	}
	if p.Anonymize != nil {
		svc.Anonymize = *p.Anonymize
	}
	if p.Replicas != nil {
		svc.Replicas = *p.Replicas
	}
	if p.Analytics != nil {
		svc.Analytics = *p.Analytics
	}
	if p.Errors != nil {
		svc.Errors = *p.Errors
	}
	if p.AnalyticsScript != nil {
		svc.AnalyticsScript = strings.TrimSpace(*p.AnalyticsScript)
	}
	if p.AnalyticsHit != nil {
		svc.AnalyticsHit = strings.TrimSpace(*p.AnalyticsHit)
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
			return domain.Service{}, err
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
	if p.Healthcheck != nil {
		svc.Healthcheck = *p.Healthcheck
	}
	if p.Volumes != nil {
		svc.Volumes = data.NormalizeVolumes(p.Volumes)
	}
	if p.PreBackup != nil {
		svc.PreBackup = strings.TrimSpace(*p.PreBackup)
	}
	if p.PublishedPorts != nil {
		svc.PublishedPorts = network.NormalizePorts(p.PublishedPorts)
	}
	if p.Routes != nil {
		svc.Routes = network.NormalizeRoutes(p.Routes)
	}
	if p.StopGraceSeconds != nil {
		svc.StopGraceSeconds = *p.StopGraceSeconds
	}
	if p.HostNetwork != nil {
		svc.HostNetwork = *p.HostNetwork
	}
	if p.DockerSocket != nil {
		svc.DockerSocket = *p.DockerSocket
	}
	if p.Runtime != nil {
		svc.Runtime = normalizeRuntime(*p.Runtime)
	}
	if p.Files != nil {
		svc.Files = normalizeFiles(p.Files, old.Files)
	}
	if p.DependsOn != nil {
		svc.DependsOn = p.DependsOn
	}
	if p.ProjectNetworks != nil {
		svc.ProjectNetworks = normalizeProjectNetworks(p.ProjectNetworks)
	}
	if p.Preview != nil {
		svc.Preview = *p.Preview
	}
	if p.Middlewares != nil {
		if svc.Middlewares, err = network.MergeMiddlewares(*p.Middlewares, old.Middlewares); err != nil {
			return domain.Service{}, err
		}
	}
	if svc.Kind.IsDatabase() {
		if err := data.CheckDatabaseUpdate(old, svc); err != nil {
			return domain.Service{}, err
		}
	}
	return svc, nil
}

// SetServiceTags replaces a service's tags. A git-linked project's compose
// file owns its services' tags (x-kipitiny.tags).
func (c *Deployer) SetServiceTags(ctx context.Context, serviceID string, tags []string) (ServiceView, error) {
	tags, err := domain.NormalizeTags(tags)
	if err != nil {
		return ServiceView{}, err
	}
	svc, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return ServiceView{}, err
	}
	if err := c.mgr.CheckGitOwned(ctx, svc.ProjectID); err != nil {
		return ServiceView{}, err
	}
	if err := c.store.SetServiceTags(ctx, svc.ID, tags); err != nil {
		return ServiceView{}, err
	}
	svc.Tags = tags
	return c.view(ctx, svc)
}
