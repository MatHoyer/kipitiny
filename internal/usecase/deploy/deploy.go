package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kipit-labs/kipitiny/internal/usecase/data"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/distribution/reference"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/opencontainers/go-digest"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/notify"
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
func (c *Deployer) Deploy(ctx context.Context, serviceID string, opts DeployOptions) (domain.Deployment, error) {
	req := deployRequest{commit: strings.ToLower(strings.TrimSpace(opts.Commit))}
	if req.commit != "" && !commitRe.MatchString(req.commit) {
		return domain.Deployment{}, fmt.Errorf("%w: commit must be a hex SHA", ErrInvalid)
	}
	if opts.Tag != "" || opts.Digest != "" {
		svc, err := c.store.GetService(ctx, serviceID)
		if err != nil {
			return domain.Deployment{}, err
		}
		// A database's version changes only through its settings: a new major
		// can't start on the old data directory.
		if svc.Kind != domain.ServiceKindApp {
			return domain.Deployment{}, fmt.Errorf("%w: only apps deploy a tag or digest", ErrInvalid)
		}
		if req.image, err = retagImage(svc.Image, opts.Tag, opts.Digest); err != nil {
			return domain.Deployment{}, err
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
		return "", fmt.Errorf("%w: service image %q: %s", ErrInvalid, image, err.Error())
	}
	ref := reference.TrimNamed(named)
	if tag != "" {
		if ref, err = reference.WithTag(ref, tag); err != nil {
			return "", fmt.Errorf("%w: tag %q: %s", ErrInvalid, tag, err.Error())
		}
	}
	if dgst != "" {
		d, err := digest.Parse(dgst)
		if err != nil {
			return "", fmt.Errorf("%w: digest %q: %s", ErrInvalid, dgst, err.Error())
		}
		if ref, err = reference.WithDigest(ref, d); err != nil {
			return "", fmt.Errorf("%w: digest %q: %s", ErrInvalid, dgst, err.Error())
		}
	}
	return reference.FamiliarString(ref), nil
}

// RetagOptions turns image into the DeployOptions that move a service from
// current to it, refusing a different repository: what the deploy
// permission alone may do. A bare repository means its latest tag.
func RetagOptions(current, image string) (DeployOptions, error) {
	cur, err := reference.ParseNormalizedNamed(current)
	if err != nil {
		return DeployOptions{}, fmt.Errorf("%w: service image %q: %s", ErrInvalid, current, err.Error())
	}
	named, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return DeployOptions{}, fmt.Errorf("%w: image %q: %s", ErrInvalid, image, err.Error())
	}
	if named.Name() != cur.Name() {
		return DeployOptions{}, fmt.Errorf("%w: needs the settings permission to change the image repository (%s)", identity.ErrForbidden, reference.FamiliarName(cur))
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
func (c *Deployer) Rollback(ctx context.Context, serviceID, deploymentID string) (domain.Deployment, error) {
	svc, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return domain.Deployment{}, err
	}
	deps, err := c.store.ListDeployments(ctx, serviceID, 50)
	if err != nil {
		return domain.Deployment{}, err
	}
	for _, d := range deps {
		if d.Status != domain.DeploymentSucceeded {
			continue
		}
		if (deploymentID == "" && d.ID != svc.CurrentDeploymentID) || d.ID == deploymentID {
			return c.startDeploy(ctx, serviceID, deployRequest{image: d.Image, commit: d.GitCommit})
		}
	}
	return domain.Deployment{}, fmt.Errorf("%w: no earlier successful deployment to roll back to", ErrInvalid)
}

func (c *Deployer) startDeploy(ctx context.Context, serviceID string, req deployRequest) (domain.Deployment, error) {
	unlock, err := c.mgr.LockService(serviceID)
	if err != nil {
		return domain.Deployment{}, err
	}
	svc, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		unlock()
		return domain.Deployment{}, err
	}
	if svc.Preview == "only" {
		unlock()
		return domain.Deployment{}, errPreviewOnly(svc)
	}
	project, err := c.store.GetProject(ctx, svc.ProjectID)
	if err != nil {
		unlock()
		return domain.Deployment{}, err
	}
	others, err := c.store.ListServices(ctx, svc.ProjectID)
	if err != nil {
		unlock()
		return domain.Deployment{}, err
	}
	if err := checkRefs(refEnv(svc), envSources{project: project.Env, dbs: databasesOf(others), apps: c.appURLs(others)}); err != nil {
		unlock()
		return domain.Deployment{}, err
	}
	if req.image != "" {
		svc.Image = req.image
	}
	image := svc.Image
	d := domain.Deployment{
		ServiceID: svc.ID,
		Status:    domain.DeploymentRunning,
		Image:     image,
		GitCommit: req.commit,
		Config:    svc,
	}
	if a, ok := identity.ActorFrom(ctx); ok {
		d.TriggeredBy = a.String()
	}
	dep, err := c.store.CreateDeployment(ctx, d)
	if err != nil {
		unlock()
		return domain.Deployment{}, err
	}

	if err := c.mgr.GoBackground(func() {
		defer unlock()
		c.runDeploy(project, svc, dep, req)
	}); err != nil {
		unlock()
		_ = c.store.FinishDeployment(ctx, dep.ID, domain.DeploymentFailed, err.Error())
		return domain.Deployment{}, err
	}
	return dep, nil
}

func (c *Deployer) runDeploy(project domain.Project, svc domain.Service, dep domain.Deployment, req deployRequest) {
	ctx, cancel := context.WithTimeout(c.bg, deployTimeout)
	defer cancel()
	log := c.log.With("deploy", dep.ID, "project", project.Name, "service", svc.Name)

	var out = io.Discard
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

	status, msg := domain.DeploymentSucceeded, ""
	if err != nil {
		status, msg = domain.DeploymentFailed, err.Error()
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
	if err := c.store.FinishDeployment(context.WithoutCancel(ctx), dep.ID, status, msg); err != nil && !errors.Is(err, domain.ErrNotFound) {
		log.Error("cannot record deployment result", "err", err)
	}
	if err == nil && req.keep {
		if err := c.store.SetServiceImage(context.WithoutCancel(ctx), svc.ID, req.image); err != nil && !errors.Is(err, domain.ErrNotFound) {
			log.Error("cannot record the deployed image", "err", err)
		}
	}

	e := notify.Event{
		Type:  domain.EventDeploySucceeded,
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
		e.Type, e.Level, e.Title, e.Message = domain.EventDeployFailed, notify.Error, fmt.Sprintf("%s/%s deployment failed", project.Name, svc.Name), msg
	}
	c.mgr.Notify(e, "/services/"+svc.ID, "")
}

func (c *Deployer) deploy(ctx context.Context, project domain.Project, svc domain.Service, dep domain.Deployment,
	out io.Writer, logf func(string, ...any)) error {

	dk := c.mgr.DockerFor(svc.ServerID)
	// A deploy wakes a sleeping project: the pre-deploy command and the new
	// replicas need its databases.
	if svc.Sleeping {
		logf("Waking %s", project.Name)
	}
	if err := c.WakeFor(ctx, svc); err != nil {
		return fmt.Errorf("wake %s: %w", project.Name, err)
	}
	if n := len(secretRefs(svc.Env, project.Env)); n > 0 {
		logf("Fetching %d secret(s) from password managers", n)
	}
	src, err := c.envSources(ctx, project, svc)
	if err != nil {
		return err
	}
	if n, err := c.mgr.HashBasicAuth(ctx, &svc); err != nil {
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
		if domain.UsesDatabase(svc.Env, name) {
			logf("Using credentials of database %s", name)
		}
	}

	if err := data.CheckExternalVolumes(ctx, dk, svc); err != nil {
		return err
	}
	logf("Pulling %s", svc.Image)
	if err := dk.PullImage(ctx, svc.Image, c.RegistryAuth(ctx, svc.Image), svc.Runtime.Platform, out); err != nil {
		return fmt.Errorf("pull %s: %w%s", svc.Image, err, pullHint(err))
	}
	if res, err := dk.InspectImage(ctx, svc.Image, svc.Runtime.Platform); err != nil {
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
