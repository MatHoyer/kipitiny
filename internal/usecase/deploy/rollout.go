package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/data"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
)

const (
	readyTimeout     = 3 * time.Minute
	stableFor        = 5 * time.Second
	preDeployTimeout = 30 * time.Minute
)

// preDeploy runs the service's pre-deploy command once in a one-off
// container with the new image and the same environment and network.
func (c *Deployer) preDeploy(ctx context.Context, project domain.Project, svc domain.Service, src envSources,
	dep domain.Deployment, out io.Writer, logf func(string, ...any)) error {

	ctx, cancel := context.WithTimeout(ctx, preDeployTimeout)
	defer cancel()

	spec := containerSpec(project, svc, src, dep.ID, 0, network.Routing{DockerSocket: c.mgr.DockerSocketFor(ctx, svc)}) // never routed
	spec.Name = fmt.Sprintf("%s-%s-predeploy-%s", project.Name, svc.Name, deploySuffix(dep.ID))
	spec.Config.Cmd = []string{"sh", "-c", svc.PreDeploy}
	spec.Config.Entrypoint = []string{}
	// Not a replica: no service label (so it never shows up as one), no routing.
	spec.Config.Labels = map[string]string{
		docker.LabelManaged:   "true",
		docker.LabelProject:   project.ID,
		docker.LabelComponent: "predeploy",
		docker.LabelDeploy:    dep.ID,
	}
	spec.HostConfig.RestartPolicy = container.RestartPolicy{Name: container.RestartPolicyDisabled}
	delete(spec.NetworkingConfig.EndpointsConfig, docker.ProxyNetwork)
	// The replica it precedes holds the published ports.
	spec.Config.ExposedPorts, spec.HostConfig.PortBindings = nil, nil

	logf("Running pre-deploy command: %s", svc.PreDeploy)
	dk := c.mgr.DockerFor(svc.ServerID)
	id, err := c.runContainer(ctx, dk, spec, serviceFiles(svc, src)...)
	if err != nil {
		return fmt.Errorf("pre-deploy: %w", err)
	}
	defer func() { _ = dk.RemoveContainer(context.WithoutCancel(ctx), id, domain.StopTimeout) }()

	wait := dk.ContainerWait(ctx, id, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	var code int64
	select {
	case res := <-wait.Result:
		code = res.StatusCode
	case err := <-wait.Error:
		return fmt.Errorf("pre-deploy: %w", err)
	}
	copyContainerLogs(ctx, dk, []string{id}, out)
	if code != 0 {
		return fmt.Errorf("pre-deploy command exited with code %d", code)
	}
	logf("Pre-deploy command succeeded")
	return nil
}

// waitReady waits until a new replica can take traffic: its healthcheck
// (image or injected probe) passes, or, without one, it stays up for a few
// seconds. A container that exits or restarts fails immediately. With a
// restart policy (domain.Runtime.Restart), exiting 0 is ready too; a job
// (restart "no") is ready only then, and on-failure may restart.
func (c *Deployer) waitReady(ctx context.Context, dk *docker.Client, id, restart string) error {
	ctx, cancel := context.WithTimeout(ctx, readyTimeout)
	defer cancel()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	var stableSince time.Time
	name := id
	notReady := func() error {
		if restart == "no" {
			return fmt.Errorf("%s still running after %s: a job (restart: \"no\") must exit", name, readyTimeout)
		}
		return fmt.Errorf("%s not ready after %s", name, readyTimeout)
	}
	for {
		res, err := dk.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
		if err != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) { // the timeout hit during the call
				return notReady()
			}
			return err
		}
		ct := res.Container
		name = ct.Name[1:]
		st := ct.State
		if restart != "" && st != nil && st.Status == container.StateExited && st.ExitCode == 0 {
			return nil
		}
		onFailure := strings.HasPrefix(restart, "on-failure")
		if st == nil || (!st.Running && !st.Restarting && st.Status != container.StateCreated) || (!onFailure && (st.Restarting || ct.RestartCount > 0)) {
			return fmt.Errorf("%s crashed (exit code %d)", ct.Name[1:], exitCode(st))
		}
		switch {
		case restart == "no" || st.Restarting: // a job: until it exits
		case st.Health != nil && st.Health.Status == container.Unhealthy:
			return fmt.Errorf("%s is unhealthy", ct.Name[1:])
		case st.Health != nil:
			if st.Health.Status == container.Healthy {
				return nil
			}
		case st.Running:
			if stableSince.IsZero() {
				stableSince = time.Now()
			} else if time.Since(stableSince) >= stableFor {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return notReady()
		case <-tick.C:
		}
	}
}

func exitCode(st *container.State) int {
	if st == nil {
		return -1
	}
	return st.ExitCode
}

// copyContainerLogs appends the last lines of containers' logs to the
// deploy log, so a failed rollout shows why.
func copyContainerLogs(ctx context.Context, dk *docker.Client, ids []string, out io.Writer) {
	ctx = context.WithoutCancel(ctx)
	for _, id := range ids {
		rc, err := dk.ContainerLogs(ctx, id, client.ContainerLogsOptions{
			ShowStdout: true, ShowStderr: true, Tail: "50",
		})
		if err != nil {
			continue
		}
		fmt.Fprintf(out, "--- last log lines of %s ---\n", id[:12])
		_, _ = stdcopy.StdCopy(out, out, rc)
		rc.Close()
	}
}

// recreate replaces a database's container in place: its volume can't be
// shared by two servers, so a short restart is unavoidable.
func (c *Deployer) recreate(ctx context.Context, project domain.Project, svc domain.Service, src envSources, dep domain.Deployment,
	logf func(string, ...any)) error {

	dk := c.mgr.DockerFor(svc.ServerID)
	existing, err := c.ServiceContainers(ctx, svc)
	if err != nil {
		return err
	}
	for _, old := range existing {
		logf("Stopping %s", old.Names[0][1:])
		if err := dk.RemoveContainer(ctx, old.ID, data.StopTimeoutFor(svc)); err != nil {
			return err
		}
	}
	spec := containerSpec(project, svc, src, dep.ID, 1, c.mgr.RouteFor(ctx, svc))
	logf("Starting %s", spec.Name)
	id, err := c.runContainer(ctx, dk, spec, serviceFiles(svc, src)...)
	if err != nil {
		return err
	}
	logf("Waiting for %s to accept connections", svc.Kind)
	if err := dk.WaitHealthy(ctx, id, data.DatabaseReadyTimeout); err != nil {
		return fmt.Errorf("%s did not become ready: %w", svc.Kind, err)
	}
	if err := c.setRunning(ctx, svc); err != nil {
		return err
	}
	return c.store.SetCurrentDeployment(ctx, svc.ID, dep.ID)
}

// rollout is a blue-green deploy: every new replica starts next to the old
// ones and must become ready before any old one stops. If one fails, all new
// containers are removed and the old version keeps serving.
func (c *Deployer) rollout(ctx context.Context, project domain.Project, svc domain.Service, src envSources,
	dep domain.Deployment, out io.Writer, logf func(string, ...any)) error {

	dk := c.mgr.DockerFor(svc.ServerID)
	active, err := c.removeRetired(ctx, svc, logf)
	if err != nil {
		return err
	}

	if svc.PreDeploy != "" {
		if err := c.preDeploy(ctx, project, svc, src, dep, out, logf); err != nil {
			return err
		}
	}

	probe, err := c.probeFor(ctx, svc)
	if err != nil {
		return err
	}

	var started []string
	cleanup := func() {
		for _, id := range started {
			_ = dk.RemoveContainer(context.WithoutCancel(ctx), id, domain.StopTimeout)
		}
	}
	for i := 1; i <= svc.Replicas; i++ {
		spec := replicaSpec(project, svc, src, dep.ID, i, probe, c.mgr.RouteFor(ctx, svc))
		logf("Starting %s", spec.Name)
		id, err := c.runContainer(ctx, dk, spec, serviceFiles(svc, src)...)
		if err != nil {
			cleanup()
			return err
		}
		started = append(started, id)
	}

	logf("Waiting for %d new replica(s) to become ready (%s)", len(started), readinessMode(svc))
	errs := make(chan error, len(started))
	for _, id := range started {
		go func() { errs <- c.waitReady(ctx, dk, id, svc.Runtime.Restart) }()
	}
	var readyErr error
	for range started {
		if err := <-errs; err != nil && readyErr == nil {
			readyErr = err
		}
	}
	if readyErr != nil {
		copyContainerLogs(ctx, dk, started, out)
		logf("Rolling back: removing new containers, the previous version keeps serving")
		cleanup()
		return fmt.Errorf("new version not ready: %w", readyErr)
	}

	if err := c.setRunning(ctx, svc); err != nil {
		cleanup()
		return err
	}
	if err := c.store.SetCurrentDeployment(ctx, svc.ID, dep.ID); err != nil {
		cleanup()
		return err
	}
	// Traffic now reaches the new replicas; stop the old ones gracefully but
	// keep them (and their logs) until the next deploy. One at a time, so
	// Traefik never holds more than one stopped backend at once.
	secs := int(data.StopTimeoutFor(svc).Seconds())
	for i, old := range active {
		if i > 0 {
			time.Sleep(retireGap)
		}
		logf("Retiring %s", old.Names[0][1:])
		if _, err := dk.ContainerStop(ctx, old.ID, client.ContainerStopOptions{Timeout: &secs}); err != nil {
			logf("Warning: could not stop %s: %v", old.Names[0][1:], err)
		}
	}
	return nil
}

// removeRetired removes the service's containers retired by an earlier
// deploy (kept for their logs) or left by a failed one, and returns the
// active ones.
func (c *Deployer) removeRetired(ctx context.Context, svc domain.Service, logf func(string, ...any)) ([]container.Summary, error) {
	dk := c.mgr.DockerFor(svc.ServerID)
	existing, err := c.ServiceContainers(ctx, svc)
	if err != nil {
		return nil, err
	}
	var active []container.Summary
	for _, ct := range existing {
		if IsActive(ct, svc) {
			active = append(active, ct)
			continue
		}
		logf("Removing retired container %s", ct.Names[0][1:])
		if err := dk.RemoveContainer(ctx, ct.ID, domain.StopTimeout); err != nil {
			return nil, err
		}
	}
	return active, nil
}

// swap replaces an app that publishes host ports: two containers can't bind
// the same port, so the old replicas stop before the new one starts, a short
// downtime. They are kept stopped for their logs; if the new replica doesn't
// become ready, it is removed and they start again.
func (c *Deployer) swap(ctx context.Context, project domain.Project, svc domain.Service, src envSources,
	dep domain.Deployment, out io.Writer, logf func(string, ...any)) error {

	dk := c.mgr.DockerFor(svc.ServerID)
	active, err := c.removeRetired(ctx, svc, logf)
	if err != nil {
		return err
	}
	if svc.PreDeploy != "" {
		if err := c.preDeploy(ctx, project, svc, src, dep, out, logf); err != nil {
			return err
		}
	}
	probe, err := c.probeFor(ctx, svc)
	if err != nil {
		return err
	}

	var stopped []container.Summary
	restore := func() {
		for _, old := range stopped {
			logf("Starting %s again", old.Names[0][1:])
			if _, err := dk.ContainerStart(context.WithoutCancel(ctx), old.ID, client.ContainerStartOptions{}); err != nil {
				logf("Warning: could not start %s: %v", old.Names[0][1:], err)
			}
		}
	}
	secs := int(data.StopTimeoutFor(svc).Seconds())
	for _, old := range active {
		if old.State != container.StateRunning {
			continue
		}
		logf("Stopping %s to free its ports", old.Names[0][1:])
		if _, err := dk.ContainerStop(ctx, old.ID, client.ContainerStopOptions{Timeout: &secs}); err != nil {
			restore()
			return err
		}
		stopped = append(stopped, old)
	}

	spec := replicaSpec(project, svc, src, dep.ID, 1, probe, c.mgr.RouteFor(ctx, svc))
	logf("Starting %s", spec.Name)
	id, err := c.runContainer(ctx, dk, spec, serviceFiles(svc, src)...)
	if err == nil {
		logf("Waiting for the new replica to become ready (%s)", readinessMode(svc))
		if err = c.waitReady(ctx, dk, id, svc.Runtime.Restart); err != nil {
			copyContainerLogs(ctx, dk, []string{id}, out)
			err = fmt.Errorf("new version not ready: %w", err)
		}
	}
	if err == nil {
		if err = c.setRunning(ctx, svc); err == nil {
			err = c.store.SetCurrentDeployment(ctx, svc.ID, dep.ID)
		}
	}
	if err != nil {
		logf("Rolling back: removing the new container, the previous version starts again")
		if id != "" {
			_ = dk.RemoveContainer(context.WithoutCancel(ctx), id, data.StopTimeoutFor(svc))
		}
		restore()
		return err
	}
	return nil
}

// IsActive reports whether a container belongs to the deployment serving
// traffic. Containers from before deployments were tracked count while running.
func IsActive(ct container.Summary, svc domain.Service) bool {
	if svc.CurrentDeploymentID == "" {
		return ct.State == container.StateRunning
	}
	return ct.Labels[docker.LabelDeploy] == svc.CurrentDeploymentID
}
