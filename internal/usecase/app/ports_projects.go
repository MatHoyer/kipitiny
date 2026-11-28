package app

import (
	"context"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/domain/notify"
	"github.com/kipit-labs/kipitiny/internal/usecase/deploy"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"
)

// projectsPorts is what the projects use cases reach of the rest of core
// (projects.Manager).
type projectsPorts struct{ c *App }

func (p projectsPorts) DockerFor(serverID string) domain.ContainerRuntime {
	return p.c.dockerFor(serverID)
}

func (p projectsPorts) GoBackground(f func()) error { return p.c.goBackground(f) }

func (p projectsPorts) GoLoop(f func()) error { return p.c.goLoop(f) }

func (p projectsPorts) LockService(id string) (func(), error) { return p.c.lockService(id) }

func (p projectsPorts) GrantCreator(ctx context.Context, projectID string) error {
	return p.c.GrantCreator(ctx, projectID)
}

func (p projectsPorts) UserActor(ctx context.Context, u domain.User) (identity.Actor, error) {
	return p.c.UserActor(ctx, u)
}

func (p projectsPorts) Restricted(ctx context.Context, a identity.Actor, t domain.APIToken) (identity.Actor, error) {
	return p.c.Restricted(ctx, a, t)
}

func (p projectsPorts) Notify(e notify.Event, path, key string) { p.c.Notify(e, path, key) }

func (p projectsPorts) ApplyCompose(ctx context.Context, projectID string, data []byte, opts deploy.ApplyOptions) (deploy.ComposePlan, error) {
	return p.c.ApplyCompose(ctx, projectID, data, opts)
}

func (p projectsPorts) ApplyGitCompose(ctx context.Context, projectID string, data []byte, opts deploy.ApplyOptions, files func(path string) ([]domain.ServiceFile, error)) (deploy.ComposePlan, map[string]string, error) {
	return p.c.ApplyGitCompose(ctx, projectID, data, opts, files)
}

func (p projectsPorts) PlanCompose(ctx context.Context, project domain.Project, data []byte, opts deploy.ApplyOptions) (deploy.ComposePlan, error) {
	return p.c.PlanCompose(ctx, project, data, opts)
}

func (p projectsPorts) StartJobContainer(ctx context.Context, project domain.Project, svc domain.Service, j domain.Job, runID string, logf func(string, ...any)) (domain.ContainerRuntime, string, error) {
	return p.c.StartJobContainer(ctx, project, svc, j, runID, logf)
}

func (p projectsPorts) RemoveServiceContainers(ctx context.Context, svc domain.Service) error {
	return p.c.RemoveServiceContainers(ctx, svc)
}

func (p projectsPorts) RemoveDeployLogs(serviceID string) { p.c.RemoveDeployLogs(serviceID) }
