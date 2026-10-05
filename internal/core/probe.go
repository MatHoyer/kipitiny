package core

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	// probeDir is where the probe volume is mounted in app containers.
	probeDir = "/.kipitiny"
	// probeHelperImage only hosts the volume while the binary is copied in.
	probeHelperImage = "busybox:stable"
)

var (
	probeBinOnce sync.Once
	probeBinPath string
	probeBinHash string
	probeBinErr  error
)

// ownBinary locates the manager's static executable (the probe) and hashes
// it once, streaming: it is ~20 MB and must not stay in memory.
func ownBinary() (path, hash string, err error) {
	probeBinOnce.Do(func() {
		if probeBinPath, probeBinErr = os.Executable(); probeBinErr != nil {
			return
		}
		f, err := os.Open(probeBinPath)
		if err != nil {
			probeBinErr = err
			return
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			probeBinErr = err
			return
		}
		probeBinHash = hex.EncodeToString(h.Sum(nil)[:6])
	})
	return probeBinPath, probeBinHash, probeBinErr
}

// probeMount returns a read-only mount holding the probe binary on the
// service's server, creating the volume on first use (one per manager build).
// Returns nil when the server's CPU architecture differs from the manager's.
func (c *Core) probeMount(ctx context.Context, serverID string) (*mount.Mount, error) {
	if m, ok := c.probes.Load(serverID); ok {
		return m.(*mount.Mount), nil
	}
	dk := c.dockerFor(serverID)
	info, err := dk.Info(ctx)
	if err != nil {
		return nil, err
	}
	if info.Arch != runtime.GOARCH {
		c.log.Warn("server architecture differs from the manager's: no injected health probe",
			"server", serverID, "arch", info.Arch)
		c.probes.Store(serverID, (*mount.Mount)(nil))
		return nil, nil
	}
	bin, hash, err := ownBinary()
	if err != nil {
		return nil, err
	}
	name := "kipitiny-probe-" + hash
	if _, err := dk.VolumeInspect(ctx, name, client.VolumeInspectOptions{}); cerrdefs.IsNotFound(err) {
		if err := installProbe(ctx, dk, name, bin); err != nil {
			return nil, fmt.Errorf("install health probe: %w", err)
		}
	} else if err != nil {
		return nil, err
	}
	m := &mount.Mount{Type: mount.TypeVolume, Source: name, Target: probeDir, ReadOnly: true}
	c.probes.Store(serverID, m)
	return m, nil
}

// installProbe copies the binary into a new volume through a stopped helper
// container, then removes the helper. The file is streamed, not buffered.
func installProbe(ctx context.Context, dk *docker.Client, volume, bin string) error {
	if _, err := dk.VolumeCreate(ctx, client.VolumeCreateOptions{
		Name:   volume,
		Labels: map[string]string{docker.LabelManaged: "true", docker.LabelComponent: "probe"},
	}); err != nil {
		return err
	}
	// A fixed public image: no registry credential.
	if err := dk.EnsureImage(ctx, probeHelperImage, ""); err != nil {
		return err
	}
	helper, err := dk.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{Image: probeHelperImage, Labels: map[string]string{docker.LabelManaged: "true"}},
		HostConfig: &container.HostConfig{
			Mounts: []mount.Mount{{Type: mount.TypeVolume, Source: volume, Target: "/probe"}},
		},
	})
	if err != nil {
		return err
	}
	defer dk.RemoveContainerAndVolumes(context.WithoutCancel(ctx), helper.ID)

	f, err := os.Open(bin)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	pr, pw := io.Pipe()
	go func() {
		tw := tar.NewWriter(pw)
		err := tw.WriteHeader(&tar.Header{Name: "kipitiny", Mode: 0o755, Size: info.Size(), ModTime: time.Now()})
		if err == nil {
			_, err = io.Copy(tw, f)
		}
		if err == nil {
			err = tw.Close()
		}
		pw.CloseWithError(err)
	}()
	_, err = dk.CopyToContainer(ctx, helper.ID, client.CopyToContainerOptions{DestinationPath: "/probe", Content: pr})
	pr.Close()
	return err
}

// imageHealthcheck reports whether an image defines its own HEALTHCHECK.
func imageHealthcheck(ctx context.Context, dk *docker.Client, image string) (bool, error) {
	res, err := dk.ImageInspect(ctx, image)
	if err != nil {
		return false, err
	}
	cfg := res.Config
	return cfg != nil && cfg.Healthcheck != nil && len(cfg.Healthcheck.Test) > 0 &&
		!slices.Equal(cfg.Healthcheck.Test, []string{"NONE"}), nil
}

// withProbe gives an app replica a healthcheck: an HTTP GET of the health
// path, or a TCP connect to the port. Traefik only routes to healthy
// containers, so a new replica gets traffic once it actually serves, and one
// that stops answering is taken out of rotation.
func withProbe(spec *client.ContainerCreateOptions, svc store.Service, m mount.Mount) {
	target := fmt.Sprintf("tcp://127.0.0.1:%d", probePort(svc))
	if svc.HealthPath != "" {
		target = fmt.Sprintf("http://127.0.0.1:%d%s", svc.Port, svc.HealthPath)
	}
	spec.Config.Healthcheck = &container.HealthConfig{
		Test:     []string{"CMD", probeDir + "/kipitiny", "probe", target},
		Interval: 10 * time.Second,
		Timeout:  5 * time.Second,
		Retries:  3,
		// Probe every second while starting, so readiness is noticed fast.
		StartPeriod:   readyTimeout,
		StartInterval: time.Second,
	}
	spec.HostConfig.Mounts = append(spec.HostConfig.Mounts, m)
}

// probeFor returns the probe mount to inject into the service's replicas, or
// nil when the image has its own healthcheck, the service has no TCP port, or
// the probe can't run on that server.
func (c *Core) probeFor(ctx context.Context, svc store.Service) (*mount.Mount, error) {
	if svc.Kind != store.ServiceKindApp || probePort(svc) == 0 {
		return nil, nil
	}
	has, err := imageHealthcheck(ctx, c.dockerFor(svc.ServerID), svc.Image)
	if err != nil || has {
		return nil, err
	}
	return c.probeMount(ctx, svc.ServerID)
}

// replicaSpec is containerSpec plus the injected health probe, if any.
func replicaSpec(project store.Project, svc store.Service, src envSources, deployID string, replica int, probe *mount.Mount, rt route) client.ContainerCreateOptions {
	spec := containerSpec(project, svc, src, deployID, replica, rt)
	if probe != nil {
		withProbe(&spec, svc, *probe)
	}
	return spec
}
