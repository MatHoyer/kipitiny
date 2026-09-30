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

const (
	deployTimeout = 30 * time.Minute
	// retireGap lets Traefik drop a stopped replica before the next stops.
	retireGap = 500 * time.Millisecond
)

// Deploy starts a deployment of the service's current configuration in the
// background and returns immediately. Progress goes to the deployment's log.
func (c *Core) Deploy(ctx context.Context, serviceID string) (store.Deployment, error) {
	return c.startDeploy(ctx, serviceID, "")
}

// Rollback redeploys the image of an earlier successful deployment (the one
// before the current, when deploymentID is empty) with the current settings.
func (c *Core) Rollback(ctx context.Context, serviceID, deploymentID string) (store.Deployment, error) {
	svc, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return store.Deployment{}, err
	}
	deps, err := c.store.ListDeployments(ctx, serviceID, 50)
	if err != nil {
		return store.Deployment{}, err
	}
	for _, d := range deps {
		if d.Status != store.DeploymentSucceeded {
			continue
		}
		if (deploymentID == "" && d.ID != svc.CurrentDeploymentID) || d.ID == deploymentID {
			return c.startDeploy(ctx, serviceID, d.Image)
		}
	}
	return store.Deployment{}, fmt.Errorf("%w: no earlier successful deployment to roll back to", ErrInvalid)
}

func (c *Core) startDeploy(ctx context.Context, serviceID, image string) (store.Deployment, error) {
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
	if image == "" && svc.Source != store.SourceGit {
		image = svc.Image
	}
	svc.Image = image // empty for a git service: built during the deploy
	dep, err := c.store.CreateDeployment(ctx, store.Deployment{
		ServiceID: svc.ID,
		Status:    store.DeploymentRunning,
		Image:     image,
		Config:    svc,
	})
	if err != nil {
		unlock()
		return store.Deployment{}, err
	}

	if err := c.goBackground(func() {
		defer unlock()
		c.runDeploy(project, svc, dep)
	}); err != nil {
		unlock()
		_ = c.store.FinishDeployment(ctx, dep.ID, store.DeploymentFailed, err.Error())
		return store.Deployment{}, err
	}
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

	dk := c.dockerFor(svc.ServerID)
	var db *store.Service
	if svc.DatabaseID != "" {
		d, err := c.store.GetService(ctx, svc.DatabaseID)
		if err != nil {
			return fmt.Errorf("linked database: %w", err)
		}
		db = &d
		logf("Injecting DATABASE_URL for database %s", d.Name)
	}

	switch {
	case svc.Image == "": // git service: build it
		tag, commit, err := c.buildImage(ctx, project, svc, dep, out, logf)
		if err != nil {
			return err
		}
		svc.Image = tag
		if err := c.store.SetDeploymentBuild(ctx, dep.ID, tag, commit, svc); err != nil {
			return err
		}
	case isBuiltImage(svc.Image): // rollback to an earlier build: local only
		if _, err := dk.ImageInspect(ctx, svc.Image); err != nil {
			return fmt.Errorf("build %s is no longer available: %w", svc.Image, err)
		}
	default:
		logf("Pulling %s", svc.Image)
		if err := dk.PullImage(ctx, svc.Image, out); err != nil {
			return fmt.Errorf("pull %s: %w", svc.Image, err)
		}
	}
	if svc.Kind == store.ServiceKindPostgres {
		return c.recreate(ctx, project, svc, dep, logf)
	}
	if err := c.rollout(ctx, project, svc, db, dep, out, logf); err != nil {
		return err
	}
	if svc.Source == store.SourceGit {
		svc.CurrentDeploymentID = dep.ID
		c.pruneBuilds(ctx, svc)
	}
	return nil
}

// isBuiltImage reports whether an image was built by the manager (and so
// exists only locally).
func isBuiltImage(image string) bool {
	return strings.HasPrefix(image, "kipitiny/")
}

// recreate replaces a database's container in place: its volume can't be
// shared by two servers, so a short restart is unavoidable.
func (c *Core) recreate(ctx context.Context, project store.Project, svc store.Service, dep store.Deployment,
	logf func(string, ...any)) error {

	dk := c.dockerFor(svc.ServerID)
	existing, err := c.serviceContainers(ctx, svc)
	if err != nil {
		return err
	}
	for _, old := range existing {
		logf("Stopping %s", old.Names[0][1:])
		if err := dk.RemoveContainer(ctx, old.ID, stopTimeoutFor(svc)); err != nil {
			return err
		}
	}
	spec := containerSpec(project, svc, nil, dep.ID, 1, c.certResolver(ctx, svc.ServerID, svc.Domain))
	logf("Starting %s", spec.Name)
	id, err := dk.Run(ctx, spec)
	if err != nil {
		return err
	}
	logf("Waiting for postgres to accept connections")
	if err := dk.WaitHealthy(ctx, id, postgresReadyTimeout); err != nil {
		return fmt.Errorf("postgres did not become ready: %w", err)
	}
	if err := c.store.SetServiceStopped(ctx, svc.ID, false); err != nil {
		return err
	}
	return c.store.SetCurrentDeployment(ctx, svc.ID, dep.ID)
}

// rollout is a blue-green deploy: every new replica starts next to the old
// ones and must become ready before any old one stops. If one fails, all new
// containers are removed and the old version keeps serving.
func (c *Core) rollout(ctx context.Context, project store.Project, svc store.Service, db *store.Service,
	dep store.Deployment, out io.Writer, logf func(string, ...any)) error {

	dk := c.dockerFor(svc.ServerID)
	existing, err := c.serviceContainers(ctx, svc)
	if err != nil {
		return err
	}
	var active []container.Summary
	for _, ct := range existing {
		if isActive(ct, svc) {
			active = append(active, ct)
			continue
		}
		// Retired by an earlier deploy (kept for its logs) or left by a failed one.
		logf("Removing retired container %s", ct.Names[0][1:])
		if err := dk.RemoveContainer(ctx, ct.ID, stopTimeout); err != nil {
			return err
		}
	}

	if svc.PreDeploy != "" {
		if err := c.preDeploy(ctx, project, svc, db, dep, out, logf); err != nil {
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
			_ = dk.RemoveContainer(context.WithoutCancel(ctx), id, stopTimeout)
		}
	}
	for i := 1; i <= svc.Replicas; i++ {
		spec := replicaSpec(project, svc, db, dep.ID, i, probe, c.certResolver(ctx, svc.ServerID, svc.Domain))
		logf("Starting %s", spec.Name)
		id, err := dk.Run(ctx, spec)
		if err != nil {
			cleanup()
			return err
		}
		started = append(started, id)
	}

	logf("Waiting for %d new replica(s) to become ready (%s)", len(started), readinessMode(svc))
	errs := make(chan error, len(started))
	for _, id := range started {
		go func() { errs <- c.waitReady(ctx, dk, id) }()
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

	if err := c.store.SetServiceStopped(ctx, svc.ID, false); err != nil {
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
	secs := int(stopTimeout.Seconds())
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

// isActive reports whether a container belongs to the deployment serving
// traffic. Containers from before deployments were tracked count while running.
func isActive(ct container.Summary, svc store.Service) bool {
	if svc.CurrentDeploymentID == "" {
		return ct.State == container.StateRunning
	}
	return ct.Labels[docker.LabelDeploy] == svc.CurrentDeploymentID
}

func readinessMode(svc store.Service) string {
	switch {
	case svc.HealthPath != "":
		return fmt.Sprintf("HTTP GET :%d%s", svc.Port, svc.HealthPath)
	case svc.Port > 0:
		return fmt.Sprintf("port %d accepting connections", svc.Port)
	}
	return "running for 5s"
}

// containerSpec builds the container for one replica. db is the linked
// database of an app, if any.
func containerSpec(project store.Project, svc store.Service, db *store.Service, deployID string, replica int, resolver string) client.ContainerCreateOptions {
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
		maps.Copy(labels, traefikLabels(svc, resolver))
		endpoints[docker.ProxyNetwork] = &network.EndpointSettings{}
	}

	envMap := maps.Clone(svc.Env)
	if db != nil {
		if _, set := envMap["DATABASE_URL"]; !set { // an explicit value wins
			envMap["DATABASE_URL"] = DatabaseURL(*db)
		}
	}
	env := make([]string, 0, len(envMap))
	for _, k := range slices.Sorted(maps.Keys(envMap)) {
		env = append(env, k+"="+envMap[k])
	}

	cfg := &container.Config{
		Image:  svc.Image,
		Env:    env,
		Labels: labels,
	}
	host := &container.HostConfig{
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
		LogConfig:     docker.DefaultLogConfig(),
	}
	if svc.MemoryMB > 0 {
		host.Memory = int64(svc.MemoryMB) << 20
	}
	if svc.Kind == store.ServiceKindPostgres {
		applyPostgresSpec(cfg, host, svc)
	}
	name := fmt.Sprintf("%s-%s-%d", project.Name, svc.Name, replica)
	if svc.Kind != store.ServiceKindPostgres {
		// Old and new replicas run side by side during a rollout.
		name += "-" + deploySuffix(deployID)
	}
	return client.ContainerCreateOptions{
		Name:             name,
		Config:           cfg,
		HostConfig:       host,
		NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: endpoints},
	}
}

// deploySuffix is a short, name-safe token from a deployment ID.
func deploySuffix(deployID string) string {
	id := strings.ToLower(deployID)
	return id[max(0, len(id)-6):]
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
