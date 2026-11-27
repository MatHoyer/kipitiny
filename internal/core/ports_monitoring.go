package core

import (
	"context"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

// monitoringPorts is what the monitoring use cases reach of the rest of core
// (monitoring.Manager).
type monitoringPorts struct{ c *Core }

func (p monitoringPorts) DockerFor(serverID string) domain.ContainerRuntime {
	return p.c.dockerFor(serverID)
}

func (p monitoringPorts) RegistryAuth(ctx context.Context, image string) string {
	return p.c.RegistryAuth(ctx, image)
}

func (p monitoringPorts) OwnImageRef() string { return p.c.OwnImageRef() }

func (p monitoringPorts) GoLoop(f func()) error { return p.c.goLoop(f) }

func (p monitoringPorts) GoBackground(f func()) error { return p.c.goBackground(f) }

func (p monitoringPorts) BrandName(ctx context.Context) string { return p.c.BrandName(ctx) }

func (p monitoringPorts) BrandLogo(ctx context.Context) string { return p.c.Branding(ctx).Logo }

func (p monitoringPorts) BrandAccent(ctx context.Context) string {
	return p.c.BrandAccent(ctx)
}

func (p monitoringPorts) CurrentUser(ctx context.Context) (domain.User, error) {
	return p.c.CurrentUser(ctx)
}

func (p monitoringPorts) CheckGitOwned(ctx context.Context, projectID string) error {
	return p.c.CheckGitOwned(ctx, projectID)
}

func (p monitoringPorts) ResourceAlerts(ctx context.Context) domain.ResourceAlerts {
	return p.c.ResourceAlerts(ctx)
}

func (p monitoringPorts) CertResolver(ctx context.Context, serverID, domain string) string {
	return p.c.CertResolver(ctx, serverID, domain)
}

func (p monitoringPorts) KickDNS() { p.c.KickDNS() }
