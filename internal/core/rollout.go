package core

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	readyTimeout     = 3 * time.Minute
	stableFor        = 5 * time.Second
	preDeployTimeout = 30 * time.Minute
)

// preDeploy runs the service's pre-deploy command once in a one-off
// container with the new image and the same environment and network.
func (c *Core) preDeploy(ctx context.Context, project store.Project, svc store.Service, db *store.Service,
	dep store.Deployment, out io.Writer, logf func(string, ...any)) error {

	ctx, cancel := context.WithTimeout(ctx, preDeployTimeout)
	defer cancel()

	spec := containerSpec(project, svc, db, dep.ID, 0)
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

	logf("Running pre-deploy command: %s", svc.PreDeploy)
	id, err := c.docker.Run(ctx, spec)
	if err != nil {
		return fmt.Errorf("pre-deploy: %w", err)
	}
	defer c.docker.RemoveContainer(context.WithoutCancel(ctx), id, stopTimeout)

	wait := c.docker.ContainerWait(ctx, id, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	var code int64
	select {
	case res := <-wait.Result:
		code = res.StatusCode
	case err := <-wait.Error:
		return fmt.Errorf("pre-deploy: %w", err)
	}
	c.copyContainerLogs(ctx, []string{id}, out)
	if code != 0 {
		return fmt.Errorf("pre-deploy command exited with code %d", code)
	}
	logf("Pre-deploy command succeeded")
	return nil
}

// waitReady waits until a new replica can take traffic: its healthcheck
// (image or injected probe) passes, or, without one, it stays up for a few
// seconds. A container that exits or restarts fails immediately.
func (c *Core) waitReady(ctx context.Context, svc store.Service, id string) error {
	ctx, cancel := context.WithTimeout(ctx, readyTimeout)
	defer cancel()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	var stableSince time.Time
	for {
		res, err := c.docker.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
		if err != nil {
			return err
		}
		ct := res.Container
		st := ct.State
		if st == nil || (!st.Running && st.Status != container.StateCreated) || st.Restarting || ct.RestartCount > 0 {
			return fmt.Errorf("%s crashed (exit code %d)", ct.Name[1:], exitCode(st))
		}
		switch {
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
			return fmt.Errorf("%s not ready after %s", ct.Name[1:], readyTimeout)
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

// selfContainer returns the manager's container ID when it runs in Docker
// (the default hostname of a container is its short ID).
func (c *Core) selfContainer(ctx context.Context) string {
	c.selfOnce.Do(func() {
		host, err := os.Hostname()
		if err != nil {
			return
		}
		res, err := c.docker.ContainerInspect(ctx, host, client.ContainerInspectOptions{})
		if err == nil {
			c.self = res.Container.ID
		}
	})
	return c.self
}

// copyContainerLogs appends the last lines of containers' logs to the
// deploy log, so a failed rollout shows why.
func (c *Core) copyContainerLogs(ctx context.Context, ids []string, out io.Writer) {
	ctx = context.WithoutCancel(ctx)
	for _, id := range ids {
		rc, err := c.docker.ContainerLogs(ctx, id, client.ContainerLogsOptions{
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

type selfState struct {
	selfOnce sync.Once
	self     string
}
