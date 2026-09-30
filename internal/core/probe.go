package core

import (
	"archive/tar"
	"bytes"
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
	probeBin     []byte
	probeBinHash string
	probeBinErr  error
)

// ownBinary reads the manager's static executable once: it is the probe.
func ownBinary() ([]byte, string, error) {
	probeBinOnce.Do(func() {
		exe, err := os.Executable()
		if err != nil {
			probeBinErr = err
			return
		}
		probeBin, probeBinErr = os.ReadFile(exe)
		sum := sha256.Sum256(probeBin)
		probeBinHash = hex.EncodeToString(sum[:6])
	})
	return probeBin, probeBinHash, probeBinErr
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
// container, then removes the helper.
func installProbe(ctx context.Context, dk *docker.Client, volume string, bin []byte) error {
	if _, err := dk.VolumeCreate(ctx, client.VolumeCreateOptions{
		Name:   volume,
		Labels: map[string]string{docker.LabelManaged: "true", docker.LabelComponent: "probe"},
	}); err != nil {
		return err
	}
	if err := dk.EnsureImage(ctx, probeHelperImage); err != nil {
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

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: "kipitiny", Mode: 0o755, Size: int64(len(bin)), ModTime: time.Now()}); err != nil {
		return err
	}
	if _, err := tw.Write(bin); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	_, err = dk.CopyToContainer(ctx, helper.ID, client.CopyToContainerOptions{DestinationPath: "/probe", Content: io.Reader(&buf)})
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
	target := fmt.Sprintf("tcp://127.0.0.1:%d", svc.Port)
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
// nil when the image has its own healthcheck, the service has no port, or the
// probe can't run on that server.
func (c *Core) probeFor(ctx context.Context, svc store.Service) (*mount.Mount, error) {
	if svc.Kind != store.ServiceKindApp || svc.Port == 0 {
		return nil, nil
	}
	has, err := imageHealthcheck(ctx, c.dockerFor(svc.ServerID), svc.Image)
	if err != nil || has {
		return nil, err
	}
	return c.probeMount(ctx, svc.ServerID)
}

// replicaSpec is containerSpec plus the injected health probe, if any.
func replicaSpec(project store.Project, svc store.Service, db *store.Service, deployID string, replica int, probe *mount.Mount) client.ContainerCreateOptions {
	spec := containerSpec(project, svc, db, deployID, replica)
	if probe != nil {
		withProbe(&spec, svc, *probe)
	}
	return spec
}
