package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/distribution/reference"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/opencontainers/go-digest"

	"github.com/MatHoyer/kipitiny/internal/compose"
	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/notify"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	deployTimeout = 30 * time.Minute
	// retireGap lets Traefik drop a stopped replica before the next stops.
	retireGap = 500 * time.Millisecond
)

// DeployOptions picks what a deploy ships. Tag and Digest replace those of
// the service's image (its repository stays), so CI can ship the image it
// built without being able to run any other.
type DeployOptions struct {
	Tag    string `json:"tag,omitempty"`
	Digest string `json:"digest,omitempty"`
	// Commit is the source revision, recorded on the deployment.
	Commit string `json:"commit,omitempty"`
}

// Deploy starts a deployment of the service's current configuration in the
// background and returns immediately. Progress goes to the deployment's log.
// With a tag or digest, the service keeps that image once deployed.
func (c *Core) Deploy(ctx context.Context, serviceID string, opts DeployOptions) (store.Deployment, error) {
	req := deployRequest{commit: strings.ToLower(strings.TrimSpace(opts.Commit))}
	if req.commit != "" && !commitRe.MatchString(req.commit) {
		return store.Deployment{}, fmt.Errorf("%w: commit must be a hex SHA", ErrInvalid)
	}
	if opts.Tag != "" || opts.Digest != "" {
		svc, err := c.store.GetService(ctx, serviceID)
		if err != nil {
			return store.Deployment{}, err
		}
		// A database's version changes only through its settings: a new major
		// can't start on the old data directory.
		if svc.Kind != store.ServiceKindApp {
			return store.Deployment{}, fmt.Errorf("%w: only apps deploy a tag or digest", ErrInvalid)
		}
		if req.image, err = retagImage(svc.Image, opts.Tag, opts.Digest); err != nil {
			return store.Deployment{}, err
		}
		req.keep = true
	}
	return c.startDeploy(ctx, serviceID, req)
}

var commitRe = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// retagImage swaps the tag and/or digest of image, keeping its repository.
func retagImage(image, tag, dgst string) (string, error) {
	named, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return "", fmt.Errorf("%w: service image %q: %v", ErrInvalid, image, err)
	}
	ref := reference.TrimNamed(named)
	if tag != "" {
		if ref, err = reference.WithTag(ref, tag); err != nil {
			return "", fmt.Errorf("%w: tag %q: %v", ErrInvalid, tag, err)
		}
	}
	if dgst != "" {
		d, err := digest.Parse(dgst)
		if err != nil {
			return "", fmt.Errorf("%w: digest %q: %v", ErrInvalid, dgst, err)
		}
		if ref, err = reference.WithDigest(ref, d); err != nil {
			return "", fmt.Errorf("%w: digest %q: %v", ErrInvalid, dgst, err)
		}
	}
	return reference.FamiliarString(ref), nil
}

// RetagOptions turns image into the DeployOptions that move a service from
// current to it, refusing a different repository: what a deploy token may
// do. A bare repository means its latest tag.
func RetagOptions(current, image string) (DeployOptions, error) {
	cur, err := reference.ParseNormalizedNamed(current)
	if err != nil {
		return DeployOptions{}, fmt.Errorf("%w: service image %q: %v", ErrInvalid, current, err)
	}
	named, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return DeployOptions{}, fmt.Errorf("%w: image %q: %v", ErrInvalid, image, err)
	}
	if named.Name() != cur.Name() {
		return DeployOptions{}, fmt.Errorf("%w: needs the admin scope to change the image repository (%s)", ErrForbidden, reference.FamiliarName(cur))
	}
	var opts DeployOptions
	if d, ok := named.(reference.Digested); ok {
		opts.Digest = d.Digest().String()
	}
	if t, ok := named.(reference.Tagged); ok {
		opts.Tag = t.Tag()
	} else if opts.Digest == "" {
		opts.Tag = "latest"
	}
	return opts, nil
}

// deployRequest is what startDeploy ships: image overrides the service's
// (empty: its own), and keep makes it the service's image once deployed.
type deployRequest struct {
	image  string
	commit string
	keep   bool
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
			return c.startDeploy(ctx, serviceID, deployRequest{image: d.Image, commit: d.GitCommit})
		}
	}
	return store.Deployment{}, fmt.Errorf("%w: no earlier successful deployment to roll back to", ErrInvalid)
}

func (c *Core) startDeploy(ctx context.Context, serviceID string, req deployRequest) (store.Deployment, error) {
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
	dbs, err := c.projectDatabases(ctx, svc.ProjectID)
	if err != nil {
		unlock()
		return store.Deployment{}, err
	}
	if err := checkRefs(svc.Env, envSources{project: project.Env, dbs: dbs}); err != nil {
		unlock()
		return store.Deployment{}, err
	}
	if req.image != "" {
		svc.Image = req.image
	}
	image := svc.Image
	d := store.Deployment{
		ServiceID: svc.ID,
		Status:    store.DeploymentRunning,
		Image:     image,
		GitCommit: req.commit,
		Config:    svc,
	}
	if a, ok := ActorFrom(ctx); ok {
		d.TriggeredBy = a.String()
	}
	dep, err := c.store.CreateDeployment(ctx, d)
	if err != nil {
		unlock()
		return store.Deployment{}, err
	}

	if err := c.goBackground(func() {
		defer unlock()
		c.runDeploy(project, svc, dep, req)
	}); err != nil {
		unlock()
		_ = c.store.FinishDeployment(ctx, dep.ID, store.DeploymentFailed, err.Error())
		return store.Deployment{}, err
	}
	return dep, nil
}

func (c *Core) runDeploy(project store.Project, svc store.Service, dep store.Deployment, req deployRequest) {
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
	if err == nil && req.keep {
		if err := c.store.SetServiceImage(context.WithoutCancel(ctx), svc.ID, req.image); err != nil && !errors.Is(err, store.ErrNotFound) {
			log.Error("cannot record the deployed image", "err", err)
		}
	}

	e := notify.Event{
		Type:  EventDeploySucceeded,
		Level: notify.Success,
		Title: fmt.Sprintf("%s/%s deployed", project.Name, svc.Name),
		Fields: []notify.Field{
			{Name: "Project", Value: project.Name},
			{Name: "Service", Value: svc.Name},
			{Name: "Image", Value: svc.Image},
			{Name: "Duration", Value: time.Since(start).Round(time.Second).String()},
		},
	}
	if err != nil {
		e.Type, e.Level, e.Title, e.Message = EventDeployFailed, notify.Error, fmt.Sprintf("%s/%s deployment failed", project.Name, svc.Name), msg
	}
	c.notify(e, "/services/"+svc.ID, "")
}

func (c *Core) deploy(ctx context.Context, project store.Project, svc store.Service, dep store.Deployment,
	out io.Writer, logf func(string, ...any)) error {

	dk := c.dockerFor(svc.ServerID)
	if n := len(secretRefs(svc.Env, project.Env)); n > 0 {
		logf("Fetching %d secret(s) from password managers", n)
	}
	src, err := c.envSources(ctx, project, svc)
	if err != nil {
		return err
	}
	if n, err := c.hashBasicAuth(ctx, &svc); err != nil {
		return err
	} else if n > 0 {
		logf("Fetched %d basic auth password(s) from password managers", n)
		// Replicas recreated later reuse these hashes, so they define the
		// same middleware as their siblings without fetching again.
		if err := c.store.SetDeploymentImage(ctx, dep.ID, svc.Image, svc); err != nil {
			return err
		}
	}
	dbs := src.dbs
	for _, name := range slices.Sorted(maps.Keys(dbs)) {
		if usesDatabase(svc.Env, name) {
			logf("Using credentials of database %s", name)
		}
	}

	logf("Pulling %s", svc.Image)
	if err := dk.PullImage(ctx, svc.Image, c.registryAuth(ctx, svc.Image), out); err != nil {
		return fmt.Errorf("pull %s: %w%s", svc.Image, err, pullHint(err))
	}
	if res, err := dk.ImageInspect(ctx, svc.Image); err != nil {
		return err
	} else if err := checkImageLabels(res.Config); err != nil {
		return err
	} else if pinned := pinDigest(svc.Image, res.RepoDigests); pinned != svc.Image {
		logf("Pinned to %s", pinned)
		svc.Image = pinned
		if err := c.store.SetDeploymentImage(ctx, dep.ID, pinned, svc); err != nil {
			return err
		}
	}
	switch {
	case svc.Kind.IsDatabase():
		return c.recreate(ctx, project, svc, src, dep, logf)
	case len(svc.PublishedPorts) > 0 || svc.HostNetwork:
		return c.swap(ctx, project, svc, src, dep, out, logf)
	}
	return c.rollout(ctx, project, svc, src, dep, out, logf)
}

// checkImageLabels refuses images that carry Traefik labels. Docker merges
// image labels into the container's, and Traefik reads them all: an image
// could add a router for another app's domain (or the manager's) and hijack
// it. Routing labels must come from kipitiny only.
func checkImageLabels(cfg *dockerspec.DockerOCIImageConfig) error {
	if cfg == nil {
		return nil
	}
	var bad []string
	for k := range cfg.Labels {
		if strings.HasPrefix(strings.ToLower(k), "traefik.") {
			bad = append(bad, k)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	slices.Sort(bad)
	return fmt.Errorf("%w: the image sets Traefik labels (%s); kipitiny manages routing itself, rebuild the image without them",
		ErrInvalid, strings.Join(bad, ", "))
}

// pinDigest pins image to the digest it was pulled at (name:tag@sha256:…), so
// replicas recreated later and rollbacks run the same image even when the tag
// moves. It is unchanged when already pinned or the registry gave no digest.
func pinDigest(image string, repoDigests []string) string {
	named, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return image
	}
	if _, ok := named.(reference.Digested); ok {
		return image
	}
	for _, rd := range repoDigests {
		ref, err := reference.ParseNormalizedNamed(rd)
		if err != nil || ref.Name() != named.Name() {
			continue
		}
		if d, ok := ref.(reference.Digested); ok {
			if pinned, err := reference.WithDigest(reference.TagNameOnly(named), d.Digest()); err == nil {
				return reference.FamiliarString(pinned)
			}
		}
	}
	return image
}

// recreate replaces a database's container in place: its volume can't be
// shared by two servers, so a short restart is unavoidable.
func (c *Core) recreate(ctx context.Context, project store.Project, svc store.Service, src envSources, dep store.Deployment,
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
	spec := containerSpec(project, svc, src, dep.ID, 1, c.routeFor(ctx, svc))
	logf("Starting %s", spec.Name)
	id, err := dk.Run(ctx, spec)
	if err != nil {
		return err
	}
	logf("Waiting for %s to accept connections", svc.Kind)
	if err := dk.WaitHealthy(ctx, id, databaseReadyTimeout); err != nil {
		return fmt.Errorf("%s did not become ready: %w", svc.Kind, err)
	}
	if err := c.store.SetServiceStopped(ctx, svc.ID, false); err != nil {
		return err
	}
	return c.store.SetCurrentDeployment(ctx, svc.ID, dep.ID)
}

// rollout is a blue-green deploy: every new replica starts next to the old
// ones and must become ready before any old one stops. If one fails, all new
// containers are removed and the old version keeps serving.
func (c *Core) rollout(ctx context.Context, project store.Project, svc store.Service, src envSources,
	dep store.Deployment, out io.Writer, logf func(string, ...any)) error {

	dk := c.dockerFor(svc.ServerID)
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
			_ = dk.RemoveContainer(context.WithoutCancel(ctx), id, stopTimeout)
		}
	}
	for i := 1; i <= svc.Replicas; i++ {
		spec := replicaSpec(project, svc, src, dep.ID, i, probe, c.routeFor(ctx, svc))
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
	secs := int(stopTimeoutFor(svc).Seconds())
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
func (c *Core) removeRetired(ctx context.Context, svc store.Service, logf func(string, ...any)) ([]container.Summary, error) {
	dk := c.dockerFor(svc.ServerID)
	existing, err := c.serviceContainers(ctx, svc)
	if err != nil {
		return nil, err
	}
	var active []container.Summary
	for _, ct := range existing {
		if isActive(ct, svc) {
			active = append(active, ct)
			continue
		}
		logf("Removing retired container %s", ct.Names[0][1:])
		if err := dk.RemoveContainer(ctx, ct.ID, stopTimeout); err != nil {
			return nil, err
		}
	}
	return active, nil
}

// swap replaces an app that publishes host ports: two containers can't bind
// the same port, so the old replicas stop before the new one starts, a short
// downtime. They are kept stopped for their logs; if the new replica doesn't
// become ready, it is removed and they start again.
func (c *Core) swap(ctx context.Context, project store.Project, svc store.Service, src envSources,
	dep store.Deployment, out io.Writer, logf func(string, ...any)) error {

	dk := c.dockerFor(svc.ServerID)
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
	secs := int(stopTimeoutFor(svc).Seconds())
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

	spec := replicaSpec(project, svc, src, dep.ID, 1, probe, c.routeFor(ctx, svc))
	logf("Starting %s", spec.Name)
	id, err := dk.Run(ctx, spec)
	if err == nil {
		logf("Waiting for the new replica to become ready (%s)", readinessMode(svc))
		if err = c.waitReady(ctx, dk, id); err != nil {
			copyContainerLogs(ctx, dk, []string{id}, out)
			err = fmt.Errorf("new version not ready: %w", err)
		}
	}
	if err == nil {
		if err = c.store.SetServiceStopped(ctx, svc.ID, false); err == nil {
			err = c.store.SetCurrentDeployment(ctx, svc.ID, dep.ID)
		}
	}
	if err != nil {
		logf("Rolling back: removing the new container, the previous version starts again")
		if id != "" {
			_ = dk.RemoveContainer(context.WithoutCancel(ctx), id, stopTimeoutFor(svc))
		}
		restore()
		return err
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
	case svc.Healthcheck.Set() && svc.Healthcheck.Test[0] == "NONE":
		return "running for 5s"
	case svc.Healthcheck.Set():
		return "its healthcheck passing"
	case svc.HealthPath != "":
		return fmt.Sprintf("HTTP GET :%d%s", svc.Port, svc.HealthPath)
	case probePort(svc) > 0:
		return fmt.Sprintf("port %d accepting connections", probePort(svc))
	}
	return "running for 5s"
}

// containerSpec builds the container for one replica. src resolves env
// references.
func containerSpec(project store.Project, svc store.Service, src envSources, deployID string, replica int, rt route) client.ContainerCreateOptions {
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
	if svc.HostNetwork {
		endpoints = map[string]*network.EndpointSettings{}
	}
	if httpRouted(svc) {
		maps.Copy(labels, traefikLabels(svc, rt))
		endpoints[docker.ProxyNetwork] = &network.EndpointSettings{}
	}

	envMap := resolveEnv(svc.Env, src)
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
		// No raw sockets: a compromised container can't ARP-spoof its
		// networks (e.g. the plain-HTTP Traefik → manager hop). Ping still
		// works through Docker's default ping_group_range.
		CapDrop: []string{"NET_RAW"},
	}
	if svc.MemoryMB > 0 {
		host.Memory = int64(svc.MemoryMB) << 20
	}
	if svc.CPUs > 0 {
		host.NanoCPUs = int64(math.Round(svc.CPUs * 1e9))
	}
	if svc.Kind.IsDatabase() {
		applyDatabaseSpec(cfg, host, svc)
	} else {
		host.Mounts = append(host.Mounts, appVolumeMounts(svc)...)
		if svc.HostNetwork {
			host.NetworkMode = "host"
		}
		if svc.DockerSocket != "" && rt.dockerSocket != "" {
			host.Mounts = append(host.Mounts, mount.Mount{
				Type: mount.TypeBind, Source: rt.dockerSocket, Target: compose.DockerSocket, ReadOnly: svc.DockerSocket == "ro",
			})
		}
		cfg.ExposedPorts, host.PortBindings = portBindings(svc.PublishedPorts)
		if svc.Healthcheck.Set() {
			cfg.Healthcheck = healthConfig(svc.Healthcheck)
		}
		if svc.StopGraceSeconds > 0 {
			// Docker's own stops (daemon shutdown, restarts) wait as long.
			cfg.StopTimeout = &svc.StopGraceSeconds
		}
	}
	name := fmt.Sprintf("%s-%s-%d", project.Name, svc.Name, replica)
	if !svc.Kind.IsDatabase() {
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
