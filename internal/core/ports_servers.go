package core

import (
	"context"
	"time"

	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/notify"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
)

// serversPorts is what the servers use cases reach of the rest of core
// (servers.Manager).
type serversPorts struct{ c *Core }

func (p serversPorts) GoBackground(f func()) error { return p.c.goBackground(f) }

func (p serversPorts) GoLoop(f func()) error { return p.c.goLoop(f) }

func (p serversPorts) RunningOperations() int { return int(p.c.running.Load()) }

func (p serversPorts) BusyServices() []string {
	var ids []string
	p.c.busy.Range(func(k, _ any) bool {
		ids = append(ids, k.(string))
		return true
	})
	return ids
}

func (p serversPorts) Audit(ctx context.Context, action, target string, status int, err error) {
	p.c.Audit(ctx, action, target, status, err)
}

func (p serversPorts) PruneAudit(ctx context.Context) { p.c.PruneAudit(ctx) }

func (p serversPorts) RequireOn(ctx context.Context, serviceID string, perm identity.Perm) error {
	return p.c.RequireOn(ctx, serviceID, perm)
}

func (p serversPorts) CleanupSecretHelpers(ctx context.Context, dk *docker.Client) error {
	return p.c.CleanupSecretHelpers(ctx, dk)
}

func (p serversPorts) EnsureTraefik(ctx context.Context, sv domain.Server) error {
	return p.c.EnsureTraefik(ctx, sv)
}

func (p serversPorts) EnsureTunnel(ctx context.Context, sv domain.Server) error {
	return p.c.EnsureTunnel(ctx, sv)
}

func (p serversPorts) RefreshProxy(ctx context.Context) { p.c.RefreshProxy(ctx) }

func (p serversPorts) DetectedPublicIP(serverID string) string {
	return p.c.DetectedPublicIP(serverID)
}

func (p serversPorts) SetServerNetwork(ctx context.Context, id string, in network.ServerNetwork) (domain.Server, error) {
	return p.c.Network.SetServerNetwork(ctx, id, in)
}

func (p serversPorts) Notify(e notify.Event, path, key string) { p.c.Notify(e, path, key) }

func (p serversPorts) EnsureAnalytics(ctx context.Context) error { return p.c.EnsureAnalytics(ctx) }

func (p serversPorts) MigrateSMTPSetting(ctx context.Context) error {
	return p.c.MigrateSMTPSetting(ctx)
}

func (p serversPorts) ReconcileService(ctx context.Context, project domain.Project, svc domain.Service) error {
	return p.c.ReconcileService(ctx, project, svc)
}

func (p serversPorts) TrackHealth(project domain.Project, svc domain.Service, cts []domain.Container, now time.Time) {
	p.c.TrackHealth(project, svc, cts, now)
}

func (p serversPorts) ForgetHealth(serverID string, known map[string]bool) {
	p.c.ForgetHealth(serverID, known)
}

func (p serversPorts) EnsureNetworks(ctx context.Context, sv domain.Server) error {
	return p.c.EnsureNetworks(ctx, sv)
}

func (p serversPorts) ActiveContainers(ctx context.Context, svc domain.Service) ([]domain.Container, error) {
	return p.c.ActiveContainers(ctx, svc)
}

func (p serversPorts) WakeFor(ctx context.Context, svc domain.Service) error {
	return p.c.WakeFor(ctx, svc)
}

func (p serversPorts) DeployLogPath(serviceID, deployID string) string {
	return p.c.DeployLogPath(serviceID, deployID)
}

func (p serversPorts) RegistryAuth(ctx context.Context, image string) string {
	return p.c.RegistryAuth(ctx, image)
}

func (p serversPorts) CleanupRestoreTests(ctx context.Context, dk *docker.Client) error {
	return p.c.CleanupRestoreTests(ctx, dk)
}

func (p serversPorts) RemoveStaleJobContainers(ctx context.Context, dk *docker.Client, all []domain.Container) {
	p.c.RemoveStaleJobContainers(ctx, dk, all)
}
