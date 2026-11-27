package core

import (
	"context"

	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

// deployPorts is what the deploy use cases reach of the rest of core
// (deploy.Manager). The other areas' methods come with the embedded Core;
// a Manager method must never be one the Deployer defines (it would call
// itself). DockerFor, Kick and NotifyOOM come with Core's servers.
type deployPorts struct{ *Core }

func (p deployPorts) GoBackground(f func()) error { return p.goBackground(f) }

func (p deployPorts) GoLoop(f func()) error { return p.goLoop(f) }

func (p deployPorts) LockService(id string) (func(), error) { return p.lockService(id) }

func (p deployPorts) LockServiceBackground(id string) (func(), error) {
	return p.lockServiceBackground(id)
}

func (p deployPorts) RemoveProject(ctx context.Context, project domain.Project) error {
	return p.deleteProject(ctx, project)
}

func (p deployPorts) CheckGitOwned(ctx context.Context, projectID string) error {
	return p.checkGitOwned(ctx, projectID)
}

func (p deployPorts) WithGitFile(ctx context.Context, g domain.ProjectGit, fn func(data []byte, commit string, files func(rel string) ([]domain.ServiceFile, error)) error) error {
	return p.withGitFile(ctx, g, fn)
}

func (p deployPorts) DeleteJob(ctx context.Context, j domain.Job) error { return p.deleteJob(ctx, j) }

func (p deployPorts) ReloadJobs(ctx context.Context) error { return p.reloadJobs(ctx) }

func (p deployPorts) ForgetServiceJobs(serviceID string) { p.forgetServiceJobs(serviceID) }

func (p deployPorts) RemoveJobContainers(ctx context.Context, dk *docker.Client, serviceID string) error {
	return p.removeJobContainers(ctx, dk, serviceID)
}

func (p deployPorts) JobRunningIn(projectID string) bool { return p.jobRunningIn(projectID) }
