package protonpass

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/docker"
)

const (
	// helperName is the container pass-cli runs in while it's used.
	helperName = "kipitiny-protonpass"
	// sessionVolume keeps the CLI session (and the key that encrypts it).
	sessionVolume = "kipitiny-protonpass"
	// Component labels the helper and its volume.
	Component = "secrets"
)

// ContainerRunner runs pass-cli in a helper container started from image on
// the first call, through `docker exec`, so a picker browsing vaults doesn't
// wait for a new container each time. The helper is removed once idle for
// the idle duration: nothing runs between uses.
type ContainerRunner struct {
	dk     *docker.Client
	image  string
	idle   time.Duration
	name   string // of the helper
	volume string // of the session

	mu     sync.Mutex
	id     string // running helper, "" if none
	active int    // calls in flight: the helper stays while any runs
	timer  *time.Timer
}

func NewContainerRunner(dk *docker.Client, image string, idle time.Duration) *ContainerRunner {
	return &ContainerRunner{dk: dk, image: image, idle: idle, name: helperName, volume: sessionVolume}
}

func (r *ContainerRunner) Run(ctx context.Context, cmd, env []string, stdin io.Reader) ([]byte, []byte, error) {
	var in []byte
	if stdin != nil {
		// Kept to run again on a new helper.
		b, err := io.ReadAll(stdin)
		if err != nil {
			return nil, nil, err
		}
		in = b
	}
	id, err := r.acquire(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer r.release()
	stdout, stderr, err := r.exec(ctx, id, cmd, env, in)
	var ee *docker.ExecError
	if err != nil && !errors.As(err, &ee) && ctx.Err() == nil {
		// The helper is gone (removed by hand, daemon restart): once more on
		// a new one.
		if id, err = r.restart(ctx, id); err != nil {
			return nil, nil, err
		}
		stdout, stderr, err = r.exec(ctx, id, cmd, env, in)
	}
	return stdout, stderr, err
}

func (r *ContainerRunner) exec(ctx context.Context, id string, cmd, env []string, in []byte) ([]byte, []byte, error) {
	var stdout bytes.Buffer
	opts := docker.ExecOptions{Cmd: cmd, Env: env, Stdout: &stdout}
	if in != nil {
		opts.Stdin = bytes.NewReader(in)
	}
	err := r.dk.Exec(ctx, id, opts)
	var ee *docker.ExecError
	if errors.As(err, &ee) {
		return stdout.Bytes(), []byte(ee.Stderr), err
	}
	return stdout.Bytes(), nil, err
}

// acquire returns the running helper, starting it if needed, and holds it
// until release.
func (r *ContainerRunner) acquire(ctx context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.timer != nil {
		r.timer.Stop()
	}
	if r.id == "" {
		id, err := r.start(ctx)
		if err != nil {
			return "", err
		}
		r.id = id
	}
	r.active++
	return r.id, nil
}

func (r *ContainerRunner) release() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active--
	if r.active > 0 || r.id == "" {
		return
	}
	if r.timer == nil {
		r.timer = time.AfterFunc(r.idle, r.stopIdle)
	} else {
		r.timer.Reset(r.idle)
	}
}

// restart replaces the helper id with a new one, unless another call already
// did.
func (r *ContainerRunner) restart(ctx context.Context, id string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.id != id && r.id != "" {
		return r.id, nil
	}
	nid, err := r.start(ctx)
	if err != nil {
		r.id = ""
		return "", err
	}
	r.id = nid
	return nid, nil
}

func (r *ContainerRunner) stopIdle() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active > 0 || r.id == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = r.remove(ctx)
	r.id = ""
}

// start pulls the image if missing (a cleanup may have removed it) and
// starts a new helper, replacing any left by a crash. Called with mu held.
func (r *ContainerRunner) start(ctx context.Context) (string, error) {
	if err := r.remove(ctx); err != nil {
		return "", err
	}
	if err := r.dk.EnsureImage(ctx, r.image, ""); err != nil {
		return "", fmt.Errorf("pull %s: %w", r.image, err)
	}
	labels := map[string]string{docker.LabelManaged: "true", docker.LabelComponent: Component}
	// Creating an existing volume returns it.
	if _, err := r.dk.VolumeCreate(ctx, client.VolumeCreateOptions{Name: r.volume, Labels: labels}); err != nil {
		return "", fmt.Errorf("session volume: %w", err)
	}
	id, err := r.dk.Run(ctx, client.ContainerCreateOptions{
		Name: r.name,
		Config: &container.Config{
			Image:      r.image,
			Entrypoint: []string{"sleep", "infinity"},
			Labels:     labels,
		},
		HostConfig: &container.HostConfig{
			Mounts:         []mount.Mount{{Type: mount.TypeVolume, Source: r.volume, Target: "/session"}},
			ReadonlyRootfs: true,
			Tmpfs:          map[string]string{"/tmp": "rw,noexec,nosuid,size=16m"},
			CapDrop:        []string{"ALL"},
			SecurityOpt:    []string{"no-new-privileges"},
			// tini, so sleep stops on SIGTERM.
			Init:      new(true),
			LogConfig: docker.DefaultLogConfig(),
		},
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

// remove removes the helper, by name to catch one left by a crash.
func (r *ContainerRunner) remove(ctx context.Context) error {
	return r.dk.RemoveContainer(ctx, r.name, 0)
}

// Close removes the helper now.
func (r *ContainerRunner) Close(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.timer != nil {
		r.timer.Stop()
	}
	r.id = ""
	return r.remove(ctx)
}

// Reset removes the helper and the session volume: the next call starts
// from scratch.
func (r *ContainerRunner) Reset(ctx context.Context) error {
	if err := r.Close(ctx); err != nil {
		return err
	}
	if _, err := r.dk.VolumeRemove(ctx, r.volume, client.VolumeRemoveOptions{}); err != nil && !cerrdefs.IsNotFound(err) {
		return err
	}
	return nil
}
