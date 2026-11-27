package core

import (
	"context"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

// deployPorts is what the deploy use cases reach of the rest of core
// (deploy.Manager). The other areas' methods come with the embedded Core;
// a Manager method must never be one the Deployer defines (it would call
// itself). DockerFor, Kick and NotifyOOM come with Core's servers, the git
// and jobs methods with its projects.
type deployPorts struct{ *Core }

func (p deployPorts) GoBackground(f func()) error { return p.goBackground(f) }

func (p deployPorts) GoLoop(f func()) error { return p.goLoop(f) }

func (p deployPorts) LockService(id string) (func(), error) { return p.lockService(id) }

func (p deployPorts) LockServiceBackground(id string) (func(), error) {
	return p.lockServiceBackground(id)
}

// DeleteJob is projects' RemoveJob: Core's DeleteJob takes a job ID.
func (p deployPorts) DeleteJob(ctx context.Context, j domain.Job) error {
	return p.RemoveJob(ctx, j)
}
