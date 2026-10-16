package core

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
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

var (
	volumeNameRe   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)
	dockerVolumeRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,254}$`)
)

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
		if v.External != "" && (!dockerVolumeRe.MatchString(v.External) || len(v.DriverOpts) > 0) {
			return fmt.Errorf("%w: volume %s: external must be a Docker volume's name, without driver options", ErrInvalid, v.Name)
		}
		for k := range v.DriverOpts {
			if k != "type" && k != "o" && k != "device" {
				return fmt.Errorf("%w: volume %s: driver options are type, o and device", ErrInvalid, v.Name)
			}
		}
	}
	return nil
}

// normalizeVolumes trims what users type; nil becomes empty.
func normalizeVolumes(vols []store.Volume) []store.Volume {
	out := make([]store.Volume, 0, len(vols))
	for _, v := range vols {
		v.Name, v.Path, v.External = strings.TrimSpace(v.Name), strings.TrimSpace(v.Path), strings.TrimSpace(v.External)
		if len(v.DriverOpts) == 0 {
			v.DriverOpts = nil
		}
		out = append(out, v)
	}
	return out
}

// appVolumeMounts mounts an app's volumes in a replica.
func appVolumeMounts(svc store.Service) []mount.Mount {
	mounts := make([]mount.Mount, 0, len(svc.Volumes))
	for _, v := range svc.Volumes {
		mounts = append(mounts, appVolumeMount(svc, v.Name, v.Path))
	}
	return mounts
}

// checkExternalVolumes refuses to deploy with a missing external volume:
// Docker would create an empty one in its place, which compose refuses too.
func checkExternalVolumes(ctx context.Context, dk *docker.Client, svc store.Service) error {
	for _, v := range svc.Volumes {
		if v.External == "" {
			continue
		}
		if _, err := dk.VolumeInspect(ctx, v.External, client.VolumeInspectOptions{}); cerrdefs.IsNotFound(err) {
			return fmt.Errorf("external volume %s doesn't exist on the server: create it first", v.External)
		} else if err != nil {
			return err
		}
	}
	return nil
}

// appVolumeMount mounts one app volume (by name) at path. Docker creates a
// missing volume on first use, with these labels; an external one is
// mounted as is.
func appVolumeMount(svc store.Service, name, path string) mount.Mount {
	var v store.Volume
	if i := slices.IndexFunc(svc.Volumes, func(v store.Volume) bool { return v.Name == name }); i >= 0 {
		v = svc.Volumes[i]
	}
	if v.External != "" {
		return mount.Mount{Type: mount.TypeVolume, Source: v.External, Target: path, ReadOnly: v.ReadOnly}
	}
	m := mount.Mount{
		Type:     mount.TypeVolume,
		Source:   AppVolume(svc.ID, name),
		Target:   path,
		ReadOnly: v.ReadOnly,
		VolumeOptions: &mount.VolumeOptions{Labels: map[string]string{
			docker.LabelManaged:   "true",
			docker.LabelProject:   svc.ProjectID,
			docker.LabelService:   svc.ID,
			docker.LabelComponent: volumeComponent,
		}},
	}
	if len(v.DriverOpts) > 0 {
		m.VolumeOptions.DriverConfig = &mount.Driver{Name: "local", Options: v.DriverOpts}
	}
	return m
}

// volumesHostAccess is the part of vols only admins change.
func volumesHostAccess(vols []store.Volume) []store.Volume {
	var out []store.Volume
	for _, v := range vols {
		if v.HostAccess() {
			out = append(out, store.Volume{Name: v.Name, External: v.External, DriverOpts: v.DriverOpts})
		}
	}
	return out
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
