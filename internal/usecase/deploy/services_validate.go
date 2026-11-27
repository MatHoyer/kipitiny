package deploy

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/distribution/reference"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/data"
	"github.com/kipit-labs/kipitiny/internal/usecase/monitoring"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
)

const (
	maxReplicas = 10
	// maxStopGraceSeconds bounds how long a stop (and so a deploy) may wait.
	maxStopGraceSeconds = 600
	// Sleep mode waits at least a few idle checks, and at most a month.
	minSleepAfterSeconds = 5 * 60
	maxSleepAfterSeconds = 30 * 24 * 3600
	// Docker's NanoCPUs granularity is 0.01 CPU in practice.
	minCPUs = 0.01
	maxCPUs = 256.0
)

var (
	envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	iconRe   = regexp.MustCompile(`^([a-z0-9][a-z0-9-]{0,39})?$`)
)

// validate checks a service's fields and that its env references resolve.
func (c *Deployer) validate(ctx context.Context, s domain.Service) error {
	project, err := c.store.GetProject(ctx, s.ProjectID)
	if err != nil {
		return err
	}
	dbs, err := c.projectDatabases(ctx, s.ProjectID)
	if err != nil {
		return err
	}
	if err := c.validateIn(ctx, s, project, dbs); err != nil {
		return err
	}
	all, err := c.store.ListAllServices(ctx)
	if err != nil {
		return err
	}
	others := slices.DeleteFunc(all, func(o domain.Service) bool { return s.ID != "" && o.ID == s.ID })
	return c.mgr.CheckDomain(ctx, s, project.ServerID, others)
}

// validateIn is validate against a project's variables and databases as
// they will be (ApplyCompose checks a whole file before changing anything).
func (c *Deployer) validateIn(ctx context.Context, s domain.Service, project domain.Project, dbs map[string]domain.Service) error {
	if err := validateService(s); err != nil {
		return err
	}
	if s.Domain != "" && s.Domain == c.cfg.Domain {
		return fmt.Errorf("%w: domain %q is the manager's own", ErrInvalid, s.Domain)
	}
	if s.Analytics && project.ParentID != "" {
		return fmt.Errorf("%w: previews aren't counted by analytics", ErrInvalid)
	}
	if s.Analytics && project.ServerID != domain.LocalServerID {
		return fmt.Errorf("%w: analytics only run on the manager's server for now", ErrInvalid)
	}
	if project.SleepAfterSeconds != 0 && (len(s.PublishedPorts) > 0 || s.HostNetwork) {
		return fmt.Errorf("%w: %s sleeps, and traffic that goes around Traefik can't wake it: turn sleep mode off first", ErrInvalid, project.Name)
	}
	if project.ParentID != "" && s.Preview != "" {
		return fmt.Errorf("%w: a preview's services all run in it", ErrInvalid)
	}
	if project.ParentID != "" && s.Replicas > 1 {
		// Each deploy resets it: a preview is a cheap copy, not a load test.
		return fmt.Errorf("%w: previews run one replica of each service", ErrInvalid)
	}
	if err := c.checkSecretSchemes(ctx, refEnv(s)); err != nil {
		return err
	}
	if err := c.mgr.CheckBasicAuthSchemes(ctx, s.Middlewares); err != nil {
		return err
	}
	if err := c.mgr.CheckPortsFree(ctx, s); err != nil {
		return err
	}
	// Its record points at the server, which publishes no HTTP port behind
	// a tunnel.
	if network.HTTPRouted(s) && len(s.PublishedPorts) > 0 && c.mgr.ViaTunnel(ctx, project.ServerID) {
		return fmt.Errorf("%w: behind a Cloudflare tunnel, an app with published ports can't also route HTTP on its domain: "+
			"its record must point at the server; clear the container port, or serve HTTP from another service", ErrInvalid)
	}
	return checkRefs(refEnv(s), envSources{project: project.Env, dbs: dbs})
}

func validateService(s domain.Service) error {
	if err := validateProjectNetworks(s); err != nil {
		return err
	}
	if _, err := reference.ParseNormalizedNamed(s.Image); err != nil {
		return fmt.Errorf("%w: image: %s", ErrInvalid, err.Error())
	}
	if !iconRe.MatchString(s.Icon) {
		return fmt.Errorf("%w: icon must be lowercase letters, digits and dashes", ErrInvalid)
	}
	if s.Preview != "" && s.Preview != "only" && s.Preview != "skip" {
		return fmt.Errorf("%w: preview must be only, skip or empty", ErrInvalid)
	}
	if s.Errors && s.Kind.IsDatabase() {
		return fmt.Errorf("%w: error tracking is for apps, whose Sentry SDK reports their errors", ErrInvalid)
	}
	if s.Analytics && (s.Kind.IsDatabase() || !network.HTTPRouted(s)) {
		return fmt.Errorf("%w: analytics count the visits of an app's domain: set a domain and a container port", ErrInvalid)
	}
	if err := monitoring.ValidateAnalyticsPaths(s.AnalyticsScript, s.AnalyticsHit); err != nil {
		return err
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
		if !s.Middlewares.IsZero() || len(s.PublishedPorts) > 0 || len(s.Routes) > 0 {
			return fmt.Errorf("%w: databases are never public, so take no middlewares, routes or published ports", ErrInvalid)
		}
		if s.StopGraceSeconds != 0 {
			return fmt.Errorf("%w: databases have their own stop timeout", ErrInvalid)
		}
		if s.HostNetwork || s.DockerSocket != "" {
			return fmt.Errorf("%w: databases can't use the host network or the Docker socket", ErrInvalid)
		}
		if !s.Runtime.IsZero() {
			return fmt.Errorf("%w: the manager sets how a database runs (command, user…)", ErrInvalid)
		}
		if len(s.DependsOn) > 0 {
			return fmt.Errorf("%w: databases start first, so depend on nothing", ErrInvalid)
		}
		return data.ValidateDatabase(s)
	}
	if err := data.ValidateVolumes(s.Volumes); err != nil {
		return err
	}
	if err := network.ValidateMiddlewares(s.Middlewares); err != nil {
		return err
	}
	if err := network.ValidatePorts(s); err != nil {
		return err
	}
	if err := network.ValidateHostAccess(s); err != nil {
		return err
	}
	if err := validateRuntime(s); err != nil {
		return err
	}
	if err := validateFiles(s); err != nil {
		return err
	}
	if err := validateDependsOn(s); err != nil {
		return err
	}
	if err := network.ValidateRoutes(s); err != nil {
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
	if err := validateHealthcheck(s); err != nil {
		return err
	}
	if s.Replicas < 1 || s.Replicas > maxReplicas {
		return fmt.Errorf("%w: replicas must be between 1 and %d", ErrInvalid, maxReplicas)
	}
	if s.Domain != "" {
		if !domain.ValidDomain(s.Domain) {
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

// maxHealthcheckSeconds bounds a healthcheck's durations.
const maxHealthcheckSeconds = 3600

func validateHealthcheck(s domain.Service) error {
	h := s.Healthcheck
	if !h.Set() {
		return nil
	}
	if s.HealthPath != "" {
		return fmt.Errorf("%w: set a health path or a healthcheck, not both", ErrInvalid)
	}
	switch h.Test[0] {
	case "NONE":
	case "CMD", "CMD-SHELL":
		if len(h.Test) < 2 || strings.TrimSpace(h.Test[1]) == "" {
			return fmt.Errorf("%w: the healthcheck test needs a command", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: a healthcheck test starts with CMD, CMD-SHELL or NONE", ErrInvalid)
	}
	for _, v := range []int{h.IntervalSeconds, h.TimeoutSeconds, h.StartPeriodSeconds} {
		if v < 0 || v > maxHealthcheckSeconds {
			return fmt.Errorf("%w: healthcheck durations must be between 0 and %d seconds", ErrInvalid, maxHealthcheckSeconds)
		}
	}
	if h.Retries < 0 || h.Retries > 100 {
		return fmt.Errorf("%w: healthcheck retries must be between 0 and 100", ErrInvalid)
	}
	return nil
}
