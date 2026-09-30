package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const deployTimeout = 30 * time.Minute

// Deploy starts a deployment of the service's current configuration in the
// background and returns immediately. Progress goes to the deployment's log.
//
// Replicas are recreated one by one (brief downtime per replica). Zero-downtime
// rollouts with health checks come later.
func (c *Core) Deploy(ctx context.Context, serviceID string) (store.Deployment, error) {
	unlock, err := c.lockService(serviceID)
	if err != nil {
		return store.Deployment{}, err
	}
	svc, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		unlock()
		return store.Deployment{}, err
	}
	project, err := c.store.GetProject(ctx, svc.ProjectID)
	if err != nil {
		unlock()
		return store.Deployment{}, err
	}
	dep, err := c.store.CreateDeployment(ctx, store.Deployment{
		ServiceID: svc.ID,
		Status:    store.DeploymentRunning,
		Image:     svc.Image,
	})
	if err != nil {
		unlock()
		return store.Deployment{}, err
	}

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer unlock()
		c.runDeploy(project, svc, dep)
	}()
	return dep, nil
}

func (c *Core) runDeploy(project store.Project, svc store.Service, dep store.Deployment) {
	ctx, cancel := context.WithTimeout(c.bg, deployTimeout)
	defer cancel()
	log := c.log.With("deploy", dep.ID, "project", project.Name, "service", svc.Name)

	var out io.Writer = io.Discard
	if f, err := c.createDeployLog(svc.ID, dep.ID); err != nil {
		log.Error("cannot create deploy log", "err", err)
	} else {
		defer f.Close()
		out = f
	}
	logf := func(format string, args ...any) {
		fmt.Fprintf(out, "[%s] "+format+"\n", append([]any{time.Now().UTC().Format(time.TimeOnly)}, args...)...)
	}

	start := time.Now()
	err := c.deploy(ctx, project, svc, dep, out, logf)

	status, msg := store.DeploymentSucceeded, ""
	if err != nil {
		status, msg = store.DeploymentFailed, err.Error()
		if errors.Is(ctx.Err(), context.Canceled) {
			msg = "interrupted by manager shutdown"
		}
		logf("Deployment failed: %s", msg)
		log.Warn("deployment failed", "err", err)
	} else {
		logf("Deployment succeeded in %s", time.Since(start).Round(time.Millisecond))
		log.Info("deployment succeeded")
	}
	// The deploy context may be cancelled; recording the outcome must not be.
	if err := c.store.FinishDeployment(context.WithoutCancel(ctx), dep.ID, status, msg); err != nil && !errors.Is(err, store.ErrNotFound) {
		log.Error("cannot record deployment result", "err", err)
	}
}

func (c *Core) deploy(ctx context.Context, project store.Project, svc store.Service, dep store.Deployment,
	out io.Writer, logf func(string, ...any)) error {

	logf("Pulling %s", svc.Image)
	if err := c.docker.PullImage(ctx, svc.Image, out); err != nil {
		return fmt.Errorf("pull %s: %w", svc.Image, err)
	}

	existing, err := c.serviceContainers(ctx, svc)
	if err != nil {
		return err
	}
	byReplica := map[int][]container.Summary{}
	for _, ct := range existing {
		idx, _ := strconv.Atoi(ct.Labels[docker.LabelReplica])
		byReplica[idx] = append(byReplica[idx], ct)
	}

	for i := 1; i <= svc.Replicas; i++ {
		for _, old := range byReplica[i] {
			logf("Removing old container %s", old.Names[0][1:])
			if err := c.docker.RemoveContainer(ctx, old.ID, stopTimeout); err != nil {
				return err
			}
		}
		spec := appContainerSpec(project, svc, dep.ID, i)
		logf("Starting %s", spec.Name)
		if _, err := c.docker.Run(ctx, spec); err != nil {
			return err
		}
	}

	// Scale down: replicas above the desired count.
	for _, idx := range slices.Sorted(maps.Keys(byReplica)) {
		if idx >= 1 && idx <= svc.Replicas {
			continue
		}
		for _, old := range byReplica[idx] {
			logf("Removing extra container %s", old.Names[0][1:])
			if err := c.docker.RemoveContainer(ctx, old.ID, stopTimeout); err != nil {
				return err
			}
		}
	}
	return nil
}

func appContainerSpec(project store.Project, svc store.Service, deployID string, replica int) client.ContainerCreateOptions {
	labels := map[string]string{
		docker.LabelManaged: "true",
		docker.LabelProject: project.ID,
		docker.LabelService: svc.ID,
		docker.LabelReplica: strconv.Itoa(replica),
		docker.LabelDeploy:  deployID,
	}
	endpoints := map[string]*network.EndpointSettings{
		// Other services of the project reach this one by its service name.
		docker.ProjectNetwork(project.ID): {Aliases: []string{svc.Name}},
	}
	if svc.Domain != "" {
		maps.Copy(labels, traefikLabels(svc))
		endpoints[docker.ProxyNetwork] = &network.EndpointSettings{}
	}

	env := make([]string, 0, len(svc.Env))
	for _, k := range slices.Sorted(maps.Keys(svc.Env)) {
		env = append(env, k+"="+svc.Env[k])
	}

	return client.ContainerCreateOptions{
		Name: fmt.Sprintf("%s-%s-%d", project.Name, svc.Name, replica),
		Config: &container.Config{
			Image:  svc.Image,
			Env:    env,
			Labels: labels,
		},
		HostConfig: &container.HostConfig{
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			LogConfig:     docker.DefaultLogConfig(),
		},
		NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: endpoints},
	}
}

func (c *Core) ListDeployments(ctx context.Context, serviceID string) ([]store.Deployment, error) {
	if _, err := c.store.GetService(ctx, serviceID); err != nil {
		return nil, err
	}
	return c.store.ListDeployments(ctx, serviceID, 50)
}

func (c *Core) GetDeployment(ctx context.Context, id string) (store.Deployment, error) {
	return c.store.GetDeployment(ctx, id)
}

// DeploymentLog opens the deployment's log file. Missing logs yield an empty reader.
func (c *Core) DeploymentLog(ctx context.Context, id string) (io.ReadCloser, error) {
	dep, err := c.store.GetDeployment(ctx, id)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(c.deployLogPath(dep.ServiceID, dep.ID))
	if errors.Is(err, os.ErrNotExist) {
		return io.NopCloser(strings.NewReader("")), nil
	}
	return f, err
}

func (c *Core) deployLogDir(serviceID string) string {
	return filepath.Join(c.cfg.DataDir, "deploys", serviceID)
}

func (c *Core) deployLogPath(serviceID, deployID string) string {
	return filepath.Join(c.deployLogDir(serviceID), deployID+".log")
}

func (c *Core) createDeployLog(serviceID, deployID string) (*os.File, error) {
	if err := os.MkdirAll(c.deployLogDir(serviceID), 0o700); err != nil {
		return nil, err
	}
	return os.Create(c.deployLogPath(serviceID, deployID))
}

func (c *Core) removeDeployLogs(serviceID string) {
	if err := os.RemoveAll(c.deployLogDir(serviceID)); err != nil {
		c.log.Warn("cannot remove deploy logs", "service", serviceID, "err", err)
	}
}
