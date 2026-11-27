package servers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"

	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/data"
	"github.com/kipit-labs/kipitiny/internal/usecase/deploy"
)

// cleanServer removes what the settings allow on one server. Each step runs
// even if an earlier one failed.
func (c *Servers) cleanServer(ctx context.Context, sv domain.Server, svcs []domain.Service, s CleanupSettings) CleanupResult {
	r := CleanupResult{Server: sv.Name}
	dk := c.DockerFor(sv.ID)
	until := fmt.Sprintf("%dh", s.MinAgeHours)
	fail := func(what string, err error) {
		if err != nil {
			r.Errors = append(r.Errors, fmt.Sprintf("%s: %v", what, err))
		}
	}
	step := func(name string) { c.steps.SetOn(cleanupKey, name, sv.Name) }
	step(CleanupConnect)
	if _, err := dk.Client.Ping(ctx, client.PingOptions{}); err != nil {
		fail("docker", err)
		return r
	}

	// Containers first: what they held becomes unused for the next steps.
	if s.Containers {
		step(CleanupContainers)
		f := make(client.Filters).Add("until", until).Add("label!", domain.LabelManaged)
		res, err := dk.ContainerPrune(ctx, client.ContainerPruneOptions{Filters: f})
		fail("containers", err)
		r.Containers += len(res.Report.ContainersDeleted)
		r.Reclaimed += res.Report.SpaceReclaimed
	}
	if s.Images != CleanOff {
		step(CleanupImages)
	}
	switch s.Images {
	case CleanDangling:
		f := make(client.Filters).Add("dangling", "true").Add("until", until)
		res, err := dk.ImagePrune(ctx, client.ImagePruneOptions{Filters: f})
		fail("images", err)
		r.Images += len(res.Report.ImagesDeleted)
		r.Reclaimed += res.Report.SpaceReclaimed
	case CleanUnused:
		n, size, err := c.removeUnusedImages(ctx, dk, sv.ID, svcs, time.Duration(s.MinAgeHours)*time.Hour)
		fail("images", err)
		r.Images += n
		r.Reclaimed += size
	}
	if s.BuildCache {
		step(CleanupBuildCache)
		f := make(client.Filters).Add("until", until)
		res, err := dk.BuildCachePrune(ctx, client.BuildCachePruneOptions{All: true, Filters: f})
		fail("build cache", err)
		r.BuildCache += len(res.Report.CachesDeleted)
		r.Reclaimed += res.Report.SpaceReclaimed
	}
	step(CleanupVolumes) // stale probe volumes go even when volumes are off
	if s.Volumes != CleanOff {
		n, size, err := c.removeUnusedVolumes(ctx, dk, svcs, s.Volumes == CleanUnused, time.Duration(s.MinAgeHours)*time.Hour)
		fail("volumes", err)
		r.Volumes += n
		r.Reclaimed += size
	}
	// Health probe volumes of earlier manager versions, once no app uses them.
	n, err := c.removeStaleProbes(ctx, dk)
	fail("probe volumes", err)
	r.Volumes += n
	if s.Networks {
		step(CleanupNetworks)
		f := make(client.Filters).Add("until", until).Add("label!", domain.LabelManaged)
		res, err := dk.NetworkPrune(ctx, client.NetworkPruneOptions{Filters: f})
		fail("networks", err)
		r.Networks += len(res.Report.NetworksDeleted)
	}
	if len(r.Errors) > 0 {
		c.log.Warn("cleanup errors", "server", sv.Name, "errors", r.Errors)
	}
	return r
}

// keepRollbackImages is how many successful deployments per service keep
// their image for rollback.
const keepRollbackImages = 5

// removeUnusedImages removes images no container uses, except the images of
// the server's services and of their recent successful deployments, which
// deploys and rollbacks need.
func (c *Servers) removeUnusedImages(ctx context.Context, dk *docker.Client, serverID string, svcs []domain.Service, minAge time.Duration) (int, uint64, error) {
	keep := map[string]bool{}
	protect := func(ref string) {
		if ref == "" {
			return
		}
		if res, err := dk.ImageInspect(ctx, ref); err == nil {
			keep[res.ID] = true
		}
	}
	for _, svc := range svcs {
		if svc.ServerID != serverID {
			continue
		}
		protect(svc.Image)
		deps, err := c.store.ListDeployments(ctx, svc.ID, 50)
		if err != nil {
			return 0, 0, err
		}
		kept := 0
		for _, d := range deps {
			if d.ID == svc.CurrentDeploymentID || d.Status == domain.DeploymentRunning ||
				(d.Status == domain.DeploymentSucceeded && kept < keepRollbackImages) {
				protect(d.Image)
				if d.Status == domain.DeploymentSucceeded {
					kept++
				}
			}
		}
	}
	cts, err := dk.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return 0, 0, err
	}
	for _, ct := range cts.Items {
		keep[ct.ImageID] = true
	}
	imgs, err := dk.ImageList(ctx, client.ImageListOptions{})
	if err != nil {
		return 0, 0, err
	}
	n, size := 0, uint64(0)
	for _, img := range imgs.Items {
		if keep[img.ID] || time.Since(time.Unix(img.Created, 0)) < minAge {
			continue
		}
		// No force: an image a container started meanwhile stays.
		_, err := dk.ImageRemove(ctx, img.ID, client.ImageRemoveOptions{PruneChildren: true})
		if err != nil {
			if !cerrdefs.IsNotFound(err) && !cerrdefs.IsConflict(err) {
				return n, size, err
			}
			continue
		}
		n++
		size += uint64(max(img.Size, 0))
	}
	return n, size, nil
}

// removeUnusedVolumes removes volumes no container mounts: anonymous ones
// only, or named ones too. Volumes of the manager (labelled, kipitiny-*) and
// database volumes are never touched.
func (c *Servers) removeUnusedVolumes(ctx context.Context, dk *docker.Client, svcs []domain.Service, named bool, minAge time.Duration) (int, uint64, error) {
	protected := map[string]bool{}
	for _, svc := range svcs {
		if vol := data.DataVolume(svc); vol != "" {
			protected[vol] = true
		}
	}
	f := make(client.Filters).Add("dangling", "true")
	vols, err := dk.VolumeList(ctx, client.VolumeListOptions{Filters: f})
	if err != nil {
		return 0, 0, err
	}
	n, size := 0, uint64(0)
	for _, v := range vols.Items {
		_, managed := v.Labels[domain.LabelManaged]
		if managed || protected[v.Name] || strings.HasPrefix(v.Name, "kipitiny-") {
			continue
		}
		if !named && !isAnonymousVolume(v.Labels) {
			continue
		}
		if created, err := time.Parse(time.RFC3339, v.CreatedAt); err == nil && time.Since(created) < minAge {
			continue
		}
		if _, err := dk.VolumeRemove(ctx, v.Name, client.VolumeRemoveOptions{}); err != nil {
			if !cerrdefs.IsNotFound(err) && !cerrdefs.IsConflict(err) {
				return n, size, err
			}
			continue
		}
		n++
		if v.UsageData != nil && v.UsageData.Size > 0 {
			size += uint64(v.UsageData.Size)
		}
	}
	return n, size, nil
}

// isAnonymousVolume reports whether Docker created the volume without a
// name (it labels those since Engine 23).
func isAnonymousVolume(labels map[string]string) bool {
	_, ok := labels["com.docker.volume.anonymous"]
	return ok
}

// removeStaleProbes removes unused probe volumes left by older manager binaries.
func (c *Servers) removeStaleProbes(ctx context.Context, dk *docker.Client) (int, error) {
	_, current, err := deploy.OwnBinary()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	f := make(client.Filters).Add("dangling", "true").Add("label", domain.LabelComponent+"=probe")
	vols, err := dk.VolumeList(ctx, client.VolumeListOptions{Filters: f})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, v := range vols.Items {
		if current == "" || v.Name == "kipitiny-probe-"+current {
			continue
		}
		if _, err := dk.VolumeRemove(ctx, v.Name, client.VolumeRemoveOptions{}); err == nil {
			n++
		}
	}
	return n, nil
}

// trimDeployments deletes each service's deployments beyond the newest keep,
// with their logs. The current and running ones always stay.
func (c *Servers) trimDeployments(ctx context.Context, svcs []domain.Service, keep int) int {
	n := 0
	for _, svc := range svcs {
		deps, err := c.store.ListDeployments(ctx, svc.ID, 0) // 0: all
		if err != nil {
			c.log.Warn("cleanup: list deployments", "service", svc.ID, "err", err)
			continue
		}
		for i, d := range deps {
			if i < keep || d.ID == svc.CurrentDeploymentID || d.Status == domain.DeploymentRunning {
				continue
			}
			if err := c.store.DeleteDeployment(ctx, d.ID); err != nil && !errors.Is(err, domain.ErrNotFound) {
				c.log.Warn("cleanup: delete deployment", "deployment", d.ID, "err", err)
				continue
			}
			if err := os.Remove(c.mgr.DeployLogPath(svc.ID, d.ID)); err != nil && !errors.Is(err, os.ErrNotExist) {
				c.log.Warn("cleanup: delete deployment log", "deployment", d.ID, "err", err)
			}
			n++
		}
	}
	return n
}
