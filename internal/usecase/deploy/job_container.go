package deploy

import (
	"context"
	"fmt"

	"github.com/moby/moby/api/types/container"

	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
)

// JobComponent labels the one-off containers jobs run in.
const JobComponent = "job"

// StartJobContainer starts job j's run runID in a one-off container of the
// service's current deployment: its pinned image, env, networks and
// volumes, like the pre-deploy command, waking the project first. It
// returns the container on its daemon, for the caller to wait for and
// remove.
func (c *Deployer) StartJobContainer(ctx context.Context, project domain.Project, svc domain.Service, j domain.Job, runID string,
	logf func(string, ...any)) (*docker.Client, string, error) {

	if svc.Sleeping {
		logf("Waking %s", project.Name)
	}
	if err := c.WakeFor(ctx, svc); err != nil {
		return nil, "", fmt.Errorf("wake %s: %w", project.Name, err)
	}
	dep, err := c.store.GetDeployment(ctx, svc.CurrentDeploymentID)
	if err != nil {
		return nil, "", err
	}
	want := dep.Config
	if want.ID == "" { // deployed before snapshots existed
		want = svc
		want.Image = dep.Image
	}
	want.Name, want.Networks = svc.Name, svc.Networks
	src, err := c.envSources(ctx, project, want)
	if err != nil {
		return nil, "", err
	}
	dk := c.mgr.DockerFor(svc.ServerID)
	if err := dk.EnsureImage(ctx, want.Image, c.RegistryAuth(ctx, want.Image), want.Runtime.Platform); err != nil {
		return nil, "", fmt.Errorf("pull %s: %w", want.Image, err)
	}

	spec := containerSpec(project, want, src, dep.ID, 0, network.Routing{DockerSocket: c.mgr.DockerSocketFor(ctx, want)}) // never routed
	spec.Name = fmt.Sprintf("%s-%s-job-%s-%s", project.Name, svc.Name, j.Name, deploySuffix(runID))
	spec.Config.Cmd = []string{"sh", "-c", j.Command}
	spec.Config.Entrypoint = []string{}
	spec.Config.Healthcheck = &container.HealthConfig{Test: []string{"NONE"}}
	// Not a replica: no service label (so it never shows up as one).
	spec.Config.Labels = map[string]string{
		domain.LabelManaged:   "true",
		domain.LabelProject:   project.ID,
		domain.LabelComponent: JobComponent,
		domain.LabelJob:       j.ID,
	}
	spec.HostConfig.RestartPolicy = container.RestartPolicy{Name: container.RestartPolicyDisabled}
	delete(spec.NetworkingConfig.EndpointsConfig, domain.ProxyNetwork)
	spec.Config.ExposedPorts, spec.HostConfig.PortBindings = nil, nil

	logf("Running %s in %s", j.Command, want.Image)
	id, err := c.runContainer(ctx, dk, spec, serviceFiles(want, src)...)
	return dk, id, err
}
