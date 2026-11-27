package core

import (
	"context"

	"github.com/moby/moby/api/types/container"

	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/notify"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"
)

// dataPorts is what the data use cases reach of the rest of core
// (data.Manager).
type dataPorts struct{ c *Core }

func (p dataPorts) DockerFor(serverID string) *docker.Client { return p.c.dockerFor(serverID) }

func (p dataPorts) RegistryAuth(ctx context.Context, image string) string {
	return p.c.RegistryAuth(ctx, image)
}

func (p dataPorts) OwnImageRef() string { return p.c.OwnImageRef() }

func (p dataPorts) GoBackground(f func()) error { return p.c.goBackground(f) }

func (p dataPorts) ShuttingDown() bool {
	p.c.bgMu.Lock()
	defer p.c.bgMu.Unlock()
	return p.c.closed
}

func (p dataPorts) LockService(id string) (func(), error) { return p.c.lockService(id) }

func (p dataPorts) RequireOn(ctx context.Context, serviceID string, perm identity.Perm) error {
	return p.c.RequireOn(ctx, serviceID, perm)
}

func (p dataPorts) Audit(ctx context.Context, action, target string, status int, err error) {
	p.c.Audit(ctx, action, target, status, err)
}

func (p dataPorts) Notify(e notify.Event, path, key string) { p.c.Notify(e, path, key) }

func (p dataPorts) ServiceContainers(ctx context.Context, svc domain.Service) ([]container.Summary, error) {
	return p.c.ServiceContainers(ctx, svc)
}

func (p dataPorts) ActiveContainers(ctx context.Context, svc domain.Service) ([]container.Summary, error) {
	return p.c.ActiveContainers(ctx, svc)
}

func (p dataPorts) WakeFor(ctx context.Context, svc domain.Service) error {
	return p.c.WakeFor(ctx, svc)
}

func (p dataPorts) PreviewSettings(ctx context.Context, projectID string) (domain.PreviewSettings, error) {
	return p.c.PreviewSettings(ctx, projectID)
}
