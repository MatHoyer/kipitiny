package core

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	maxVolumes = 10
	// volumeComponent labels the named volumes of apps.
	volumeComponent = "volume"
)

var volumeNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

// AppVolume is the Docker volume behind an app's named volume.
func AppVolume(serviceID, name string) string {
	return "kipitiny-vol-" + strings.ToLower(serviceID) + "-" + name
}

// validateVolumes checks an app's volumes: unique names and absolute,
// distinct mount paths outside the manager's own mounts.
func validateVolumes(vols []store.Volume) error {
	if len(vols) > maxVolumes {
		return fmt.Errorf("%w: at most %d volumes", ErrInvalid, maxVolumes)
	}
	names, paths := map[string]bool{}, map[string]bool{}
	for _, v := range vols {
		if !volumeNameRe.MatchString(v.Name) {
			return fmt.Errorf("%w: volume name %q must be lowercase letters, digits and dashes (max 32)", ErrInvalid, v.Name)
		}
		if !strings.HasPrefix(v.Path, "/") || path.Clean(v.Path) != v.Path || strings.ContainsAny(v.Path, ":,\n") {
			return fmt.Errorf("%w: volume %s: mount path must be a clean absolute path (e.g. /app/uploads)", ErrInvalid, v.Name)
		}
		if v.Path == "/" || v.Path == probeDir || strings.HasPrefix(v.Path, probeDir+"/") {
			return fmt.Errorf("%w: volume %s can't be mounted at %s", ErrInvalid, v.Name, v.Path)
		}
		if names[v.Name] {
			return fmt.Errorf("%w: duplicate volume %q", ErrInvalid, v.Name)
		}
		if paths[v.Path] {
			return fmt.Errorf("%w: two volumes mounted at %s", ErrInvalid, v.Path)
		}
		names[v.Name], paths[v.Path] = true, true
	}
	return nil
}

// normalizeVolumes trims what users type; nil becomes empty.
func normalizeVolumes(vols []store.Volume) []store.Volume {
	out := make([]store.Volume, 0, len(vols))
	for _, v := range vols {
		out = append(out, store.Volume{Name: strings.TrimSpace(v.Name), Path: strings.TrimSpace(v.Path)})
	}
	return out
}

// appVolumeMounts mounts an app's volumes. Docker creates a missing volume
// on first use, with these labels.
func appVolumeMounts(projectID string, svc store.Service) []mount.Mount {
	mounts := make([]mount.Mount, 0, len(svc.Volumes))
	for _, v := range svc.Volumes {
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeVolume,
			Source: AppVolume(svc.ID, v.Name),
			Target: v.Path,
			VolumeOptions: &mount.VolumeOptions{Labels: map[string]string{
				docker.LabelManaged:   "true",
				docker.LabelProject:   projectID,
				docker.LabelService:   svc.ID,
				docker.LabelComponent: volumeComponent,
			}},
		})
	}
	return mounts
}

// hasData reports whether deleting the service destroys data.
func hasData(svc store.Service) bool {
	return svc.Kind.IsDatabase() || len(svc.Volumes) > 0
}

// removeServiceVolumes removes a database's data volume and every volume
// created for an app, including ones no longer in its settings.
func removeServiceVolumes(ctx context.Context, dk *docker.Client, svc store.Service) error {
	if vol := DataVolume(svc); vol != "" {
		if err := dk.RemoveVolume(ctx, vol); err != nil {
			return err
		}
	}
	f := make(client.Filters).
		Add("label", docker.LabelService+"="+svc.ID).
		Add("label", docker.LabelComponent+"="+volumeComponent)
	vols, err := dk.VolumeList(ctx, client.VolumeListOptions{Filters: f})
	if err != nil {
		return err
	}
	for _, v := range vols.Items {
		if err := dk.RemoveVolume(ctx, v.Name); err != nil {
			return err
		}
	}
	return nil
}
