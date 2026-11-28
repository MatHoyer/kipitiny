// Package docker wraps the Docker Engine API client: domain.ContainerRuntime
// on a local or remote (SSH) daemon.
package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

type Client struct {
	*client.Client
	closer interface{ Close() error } // SSH connection, for remote clients
}

// Close releases the client and, for remote servers, its SSH connection.
func (c *Client) Close() error {
	err := c.Client.Close()
	if c.closer != nil {
		if cerr := c.closer.Close(); err == nil {
			err = cerr
		}
	}
	return err
}

// New connects using DOCKER_HOST et al., defaulting to the local socket.
func New() (*Client, error) {
	c, err := client.New(client.FromEnv)
	if err != nil {
		return nil, err
	}
	return &Client{Client: c}, nil
}

func (c *Client) Info(ctx context.Context) (domain.DockerInfo, error) {
	v, err := c.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil {
		return domain.DockerInfo{}, err
	}
	return domain.DockerInfo{Version: v.Version, APIVersion: v.APIVersion, OS: v.Os, Arch: v.Arch}, nil
}

// EnsureNetwork creates a bridge network if it does not exist yet.
func (c *Client) EnsureNetwork(ctx context.Context, name string) error {
	_, err := c.NetworkInspect(ctx, name, client.NetworkInspectOptions{})
	if err == nil {
		return nil
	}
	if !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("inspect network %s: %w", name, err)
	}
	_, err = c.NetworkCreate(ctx, name, client.NetworkCreateOptions{
		Driver: "bridge",
		Labels: map[string]string{domain.LabelManaged: "true"},
	})
	if err != nil && !cerrdefs.IsConflict(err) {
		return fmt.Errorf("create network %s: %w", name, err)
	}
	return nil
}

// RemoveNetwork deletes a network, ignoring ones that are already gone.
func (c *Client) RemoveNetwork(ctx context.Context, name string) error {
	_, err := c.NetworkRemove(ctx, name, client.NetworkRemoveOptions{})
	if err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("remove network %s: %w", name, err)
	}
	return nil
}

// DisconnectNetwork detaches a container from a network, ignoring a
// container or network that is already gone.
func (c *Client) DisconnectNetwork(ctx context.Context, name, containerID string) error {
	_, err := c.NetworkDisconnect(ctx, name, client.NetworkDisconnectOptions{Container: containerID})
	if err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("disconnect %s from %s: %w", containerID, name, err)
	}
	return nil
}

// RemoveContainer stops a container gracefully (SIGTERM, then SIGKILL after
// timeout) and removes it. Missing containers are not an error.
func (c *Client) RemoveContainer(ctx context.Context, id string, timeout time.Duration) error {
	secs := int(timeout.Seconds())
	if _, err := c.ContainerStop(ctx, id, client.ContainerStopOptions{Timeout: &secs}); err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("stop container: %w", err)
	}
	if _, err := c.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true}); err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("remove container: %w", err)
	}
	return nil
}

// PullImage pulls ref with auth (an encoded registry credential, or "" for
// none) for platform ("" is the server's, see Platform), writing one line per
// status change (not per progress tick) to w.
func (c *Client) PullImage(ctx context.Context, ref, auth, platform string, w io.Writer) error {
	opts := client.ImagePullOptions{RegistryAuth: auth}
	if p := ociPlatform(platform); p != nil {
		opts.Platforms = []ocispec.Platform{*p}
	}
	resp, err := c.ImagePull(ctx, ref, opts)
	if err != nil {
		return err
	}
	for msg, err := range resp.JSONMessages(ctx) {
		if err != nil {
			return err
		}
		if msg.Error != nil {
			return errors.New(msg.Error.Message)
		}
		if msg.Progress != nil && msg.Progress.Total > 0 {
			continue // byte counters; too noisy for a log file
		}
		if msg.ID != "" {
			fmt.Fprintf(w, "%s: %s\n", msg.ID, msg.Status)
		} else if msg.Status != "" {
			fmt.Fprintln(w, msg.Status)
		}
	}
	return nil
}

// EnsureImage pulls ref (with auth and platform, see PullImage) only if it
// is not present locally.
func (c *Client) EnsureImage(ctx context.Context, ref, auth, platform string) error {
	if _, err := c.inspectImage(ctx, ref, platform); err == nil {
		return nil
	} else if !cerrdefs.IsNotFound(err) {
		return err
	}
	return c.PullImage(ctx, ref, auth, platform, io.Discard)
}

// inspectImage inspects platform's image of ref ("" is the server's).
func (c *Client) inspectImage(ctx context.Context, ref, platform string) (client.ImageInspectResult, error) {
	p := ociPlatform(platform)
	if p == nil {
		return c.ImageInspect(ctx, ref)
	}
	res, err := c.ImageInspect(ctx, ref, client.ImageInspectWithPlatform(p))
	if err != nil && !cerrdefs.IsNotFound(err) {
		// shortcut: daemons before API 1.49 can't pick a platform; the tag's image is then the one pulled last.
		return c.ImageInspect(ctx, ref)
	}
	return res, err
}

// Platform parses os/arch[/variant]; "" is nil, the server's own.
func ociPlatform(s string) *ocispec.Platform {
	if s == "" {
		return nil
	}
	parts := append(strings.SplitN(s, "/", 3), "", "")
	return &ocispec.Platform{OS: parts[0], Architecture: parts[1], Variant: parts[2]}
}

// Login checks a registry credential: the daemon signs in to the registry.
func (c *Client) Login(ctx context.Context, server, username, password string) error {
	_, err := c.RegistryLogin(ctx, client.RegistryLoginOptions{ServerAddress: server, Username: username, Password: password})
	return err
}

// run creates and starts a container, first writing files into it. If
// anything fails the container is removed.
func (c *Client) run(ctx context.Context, opts client.ContainerCreateOptions, files ...domain.ContainerFile) (string, error) {
	res, err := c.ContainerCreate(ctx, opts)
	if err != nil {
		return "", fmt.Errorf("create container %s: %w", opts.Name, err)
	}
	remove := func() {
		_, _ = c.ContainerRemove(context.WithoutCancel(ctx), res.ID, client.ContainerRemoveOptions{Force: true})
	}
	if len(files) > 0 {
		archive, err := tarFiles(files)
		if err == nil {
			_, err = c.Client.CopyToContainer(ctx, res.ID, client.CopyToContainerOptions{DestinationPath: "/", Content: archive})
		}
		if err != nil {
			remove()
			return "", fmt.Errorf("copy files into %s: %w", opts.Name, err)
		}
	}
	if _, err := c.ContainerStart(ctx, res.ID, client.ContainerStartOptions{}); err != nil {
		remove()
		return "", fmt.Errorf("start container %s: %w", opts.Name, err)
	}
	return res.ID, nil
}

// tarFiles archives files relative to "/". It has no entries for their
// parent directories: Docker creates the missing ones, and an entry would
// reset an existing one's owner and mode (/tmp would lose its sticky bit).
func tarFiles(files []domain.ContainerFile) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, f := range files {
		rel := strings.TrimPrefix(path.Clean(f.Path), "/")
		mode := f.Mode
		if mode == 0 {
			mode = 0o644
		}
		if err := tw.WriteHeader(&tar.Header{Name: rel, Mode: mode, Size: int64(len(f.Content))}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(f.Content); err != nil {
			return nil, err
		}
	}
	return &buf, tw.Close()
}

// DefaultLogConfig caps on-disk container logs (local driver is compressed).
func DefaultLogConfig() container.LogConfig {
	return container.LogConfig{
		Type:   "local",
		Config: map[string]string{"max-size": "10m", "max-file": "3"},
	}
}

// RenameContainer gives a container a new name, ignoring ones that are
// already gone.
func (c *Client) RenameContainer(ctx context.Context, id, name string) error {
	_, err := c.ContainerRename(ctx, id, client.ContainerRenameOptions{NewName: name})
	if err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("rename container %s: %w", id, err)
	}
	return nil
}

// SetAliases replaces a container's DNS aliases on a network it is attached
// to, by reconnecting it: open connections through that network are reset.
func (c *Client) SetAliases(ctx context.Context, networkName, id string, aliases []string) error {
	if _, err := c.NetworkDisconnect(ctx, networkName, client.NetworkDisconnectOptions{Container: id}); err != nil {
		return fmt.Errorf("disconnect %s from %s: %w", id, networkName, err)
	}
	_, err := c.NetworkConnect(ctx, networkName, client.NetworkConnectOptions{
		Container:      id,
		EndpointConfig: &network.EndpointSettings{Aliases: aliases},
	})
	if err != nil {
		return fmt.Errorf("connect %s to %s: %w", id, networkName, err)
	}
	return nil
}

// RemoveVolume deletes a volume, ignoring ones that are already gone.
func (c *Client) RemoveVolume(ctx context.Context, name string) error {
	_, err := c.VolumeRemove(ctx, name, client.VolumeRemoveOptions{})
	if err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("remove volume %s: %w", name, err)
	}
	return nil
}

// WaitHealthy polls a container until its healthcheck passes. Containers
// without a healthcheck count as healthy once running. One restart by its
// restart policy is waited through (Docker reports it unhealthy meanwhile);
// a second one means it keeps crashing.
func (c *Client) WaitHealthy(ctx context.Context, id string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, errWaitTimeout)
	defer cancel()
	// timedOut tells the timeout, even mid-call, from the caller cancelling.
	timedOut := func(err error) error {
		if !errors.Is(context.Cause(ctx), errWaitTimeout) {
			return err
		}
		return fmt.Errorf("not healthy after %s%s", timeout, c.lastLogLines(ctx, id))
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		res, err := c.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
		if err != nil {
			return timedOut(err)
		}
		st := res.Container.State
		switch {
		case st == nil:
		case res.Container.RestartCount > 1:
			return fmt.Errorf("container keeps restarting%s", c.lastLogLines(ctx, id))
		case st.Restarting:
		case !st.Running && st.Status != container.StateCreated:
			return fmt.Errorf("container exited (code %d)%s", st.ExitCode, c.lastLogLines(ctx, id))
		case st.Health == nil && st.Running:
			return nil
		case st.Health != nil && st.Health.Status == container.Healthy:
			return nil
		case st.Health != nil && st.Health.Status == container.Unhealthy:
			return fmt.Errorf("container is unhealthy%s%s", lastHealthcheck(st.Health), c.lastLogLines(ctx, id))
		}
		select {
		case <-ctx.Done():
			return timedOut(ctx.Err())
		case <-tick.C:
		}
	}
}

var errWaitTimeout = errors.New("wait timeout")

// lastHealthcheck is the output of a container's last healthcheck, as an
// error's suffix: the probe often says what is wrong.
func lastHealthcheck(h *container.Health) string {
	if len(h.Log) == 0 || h.Log[len(h.Log)-1] == nil {
		return ""
	}
	s := strings.TrimSpace(h.Log[len(h.Log)-1].Output)
	if s == "" {
		return ""
	}
	return ", last healthcheck output:\n" + s
}

// lastLogLines is the end of a container's logs, as an error's suffix: a
// container that exits on start says why there.
func (c *Client) lastLogLines(ctx context.Context, id string) string {
	rc, err := c.Logs(context.WithoutCancel(ctx), id, domain.LogOptions{Tail: "20"})
	if err != nil {
		return ""
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	const maxLen = 8 << 10 // lines can be long (MongoDB logs JSON)
	if len(b) > maxLen {
		b = b[len(b)-maxLen:]
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return ""
	}
	return ", last log lines:\n" + s
}

// RemoveContainerAndVolumes force-removes a container with its anonymous volumes.
func (c *Client) RemoveContainerAndVolumes(ctx context.Context, id string) error {
	_, err := c.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
	if err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("remove container: %w", err)
	}
	return nil
}

// listNetworks returns the networks the manager created.
func (c *Client) listNetworks(ctx context.Context) ([]network.Summary, error) {
	f := make(client.Filters)
	f.Add("label", domain.LabelManaged+"=true")
	res, err := c.NetworkList(ctx, client.NetworkListOptions{Filters: f})
	if err != nil {
		return nil, err
	}
	return res.Items, nil
}
