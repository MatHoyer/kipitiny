package deploy

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
	"github.com/moby/moby/client"

	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/data"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
)

const (
	probeDir = domain.ProbeDir
	// probeHelperImage only hosts the volume while the binary is copied in.
	probeHelperImage = data.HelperImage
)

var (
	probeBinOnce sync.Once
	probeBinPath string
	probeBinHash string
	probeBinErr  error
)

// OwnBinary locates the manager's static executable (the probe) and hashes
// it once, streaming: it is ~20 MB and must not stay in memory.
func OwnBinary() (path, hash string, err error) {
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
func (c *Deployer) probeMount(ctx context.Context, serverID string) (*domain.Mount, error) {
	if m, ok := c.probes.Load(serverID); ok {
		return m.(*domain.Mount), nil
	}
	dk := c.mgr.DockerFor(serverID)
	info, err := dk.Info(ctx)
	if err != nil {
		return nil, err
	}
	if info.Arch != runtime.GOARCH {
		c.log.Warn("server architecture differs from the manager's: no injected health probe",
			"server", serverID, "arch", info.Arch)
		c.probes.Store(serverID, (*domain.Mount)(nil))
		return nil, nil
	}
	bin, hash, err := OwnBinary()
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
	m := &domain.Mount{Type: domain.MountVolume, Source: name, Target: probeDir, ReadOnly: true}
	c.probes.Store(serverID, m)
	return m, nil
}

// installProbe copies the binary into a new volume through a stopped helper
// container, then removes the helper. The file is streamed, not buffered.
func installProbe(ctx context.Context, dk *docker.Client, volume, bin string) error {
	if _, err := dk.VolumeCreate(ctx, client.VolumeCreateOptions{
		Name:   volume,
		Labels: map[string]string{domain.LabelManaged: "true", domain.LabelComponent: "probe"},
	}); err != nil {
		return err
	}
	// A fixed public image: no registry credential.
	if err := dk.EnsureImage(ctx, probeHelperImage, "", ""); err != nil {
		return err
	}
	helper, err := dk.CreateContainer(ctx, domain.ContainerSpec{
		Image: probeHelperImage, Labels: map[string]string{domain.LabelManaged: "true"},
		Mounts: []domain.Mount{{Type: domain.MountVolume, Source: volume, Target: "/probe"}},
	})
	if err != nil {
		return err
	}
	defer func() { _ = dk.RemoveContainerAndVolumes(context.WithoutCancel(ctx), helper) }()

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
	_, err = dk.Client.CopyToContainer(ctx, helper, client.CopyToContainerOptions{DestinationPath: "/probe", Content: pr})
	pr.Close()
	return err
}

// imageHealthcheck reports whether an image defines its own HEALTHCHECK.
func imageHealthcheck(ctx context.Context, dk *docker.Client, image, platform string) (bool, error) {
	res, err := dk.InspectImageMoby(ctx, image, platform)
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
func withProbe(spec *domain.ContainerSpec, svc domain.Service, m domain.Mount) {
	target := fmt.Sprintf("tcp://127.0.0.1:%d", network.ProbePort(svc))
	if svc.HealthPath != "" {
		target = fmt.Sprintf("http://127.0.0.1:%d%s", svc.Port, svc.HealthPath)
	}
	spec.Healthcheck = &domain.HealthConfig{
		Test:     []string{"CMD", probeDir + "/kipitiny", "probe", target},
		Interval: 10 * time.Second,
		Timeout:  5 * time.Second,
		Retries:  3,
		// Probe every second while starting, so readiness is noticed fast.
		StartPeriod:   readyTimeout,
		StartInterval: time.Second,
	}
	spec.Mounts = append(spec.Mounts, m)
}

// healthConfig is an app's own healthcheck. Without a start period, it
// gets the probe's: checked every second while starting, so a new replica's
// readiness is noticed at once rather than after the first interval.
func healthConfig(h domain.Healthcheck) *domain.HealthConfig {
	if h.Test[0] == "NONE" {
		return &domain.HealthConfig{Test: []string{"NONE"}}
	}
	hc := &domain.HealthConfig{
		Test:          h.Test,
		Interval:      time.Duration(h.IntervalSeconds) * time.Second,
		Timeout:       time.Duration(h.TimeoutSeconds) * time.Second,
		StartPeriod:   time.Duration(h.StartPeriodSeconds) * time.Second,
		StartInterval: time.Second,
		Retries:       h.Retries,
	}
	if hc.StartPeriod == 0 {
		hc.StartPeriod = readyTimeout
	}
	return hc
}

// probeFor returns the probe mount to inject into the service's replicas, or
// nil when the image or the service has its own healthcheck, the service has
// no TCP port, or
// the probe can't run on that server.
func (c *Deployer) probeFor(ctx context.Context, svc domain.Service) (*domain.Mount, error) {
	if svc.Kind != domain.ServiceKindApp || network.ProbePort(svc) == 0 || svc.Healthcheck.Set() {
		return nil, nil
	}
	has, err := imageHealthcheck(ctx, c.mgr.DockerFor(svc.ServerID), svc.Image, svc.Runtime.Platform)
	if err != nil || has {
		return nil, err
	}
	return c.probeMount(ctx, svc.ServerID)
}

// replicaSpec is containerSpec plus the injected health probe, if any.
func replicaSpec(project domain.Project, svc domain.Service, src envSources, deployID string, replica int, probe *domain.Mount, rt network.Routing) domain.ContainerSpec {
	spec := containerSpec(project, svc, src, deployID, replica, rt)
	if probe != nil {
		withProbe(&spec, svc, *probe)
	}
	return spec
}
