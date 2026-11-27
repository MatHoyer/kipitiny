// Package docker wraps the Docker Engine API client and holds the label and
// network conventions shared by everything the manager creates.
package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/registry"
	"github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

// The label and network conventions are domain's; these names stay until
// the use cases use domain's.
const (
	ProxyNetwork    = domain.ProxyNetwork
	LabelManaged    = domain.LabelManaged
	LabelComponent  = domain.LabelComponent
	LabelProject    = domain.LabelProject
	LabelService    = domain.LabelService
	LabelReplica    = domain.LabelReplica
	LabelDeploy     = domain.LabelDeploy
	LabelJob        = domain.LabelJob
	LabelConfigHash = domain.LabelConfigHash
)

var (
	ProjectNetwork      = domain.ProjectNetwork
	ProjectNetworkNamed = domain.ProjectNetworkNamed
	CustomNetwork       = domain.CustomNetwork
	IsCustomNetwork     = domain.IsCustomNetwork
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

type Info = domain.DockerInfo

func (c *Client) Info(ctx context.Context) (Info, error) {
	v, err := c.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil {
		return Info{}, err
	}
	return Info{Version: v.Version, APIVersion: v.APIVersion, OS: v.Os, Arch: v.Arch}, nil
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
		Labels: map[string]string{LabelManaged: "true"},
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

// ListSummaries returns all containers (running or not) matching every label.
func (c *Client) ListSummaries(ctx context.Context, labels map[string]string) ([]container.Summary, error) {
	f := make(client.Filters)
	for k, v := range labels {
		f.Add("label", k+"="+v)
	}
	res, err := c.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: f})
	if err != nil {
		return nil, err
	}
	return res.Items, nil
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
	if p := Platform(platform); p != nil {
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
	if _, err := c.InspectImageMoby(ctx, ref, platform); err == nil {
		return nil
	} else if !cerrdefs.IsNotFound(err) {
		return err
	}
	return c.PullImage(ctx, ref, auth, platform, io.Discard)
}

// InspectImageMoby inspects platform's image of ref ("" is the server's).
func (c *Client) InspectImageMoby(ctx context.Context, ref, platform string) (client.ImageInspectResult, error) {
	p := Platform(platform)
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
func Platform(s string) *ocispec.Platform {
	if s == "" {
		return nil
	}
	parts := append(strings.SplitN(s, "/", 3), "", "")
	return &ocispec.Platform{OS: parts[0], Architecture: parts[1], Variant: parts[2]}
}

// RegistryAuth encodes a registry credential for PullImage.
func RegistryAuth(host, username, password string) string {
	b, _ := json.Marshal(registry.AuthConfig{Username: username, Password: password, ServerAddress: host})
	return base64.URLEncoding.EncodeToString(b)
}

// Login checks a registry credential: the daemon signs in to the registry.
func (c *Client) Login(ctx context.Context, server, username, password string) error {
	_, err := c.RegistryLogin(ctx, client.RegistryLoginOptions{ServerAddress: server, Username: username, Password: password})
	return err
}

// File is written into a container before it starts.
type File = domain.ContainerFile

// RunMoby creates and starts a container, first writing files into it. If
// anything fails the container is removed.
func (c *Client) RunMoby(ctx context.Context, opts client.ContainerCreateOptions, files ...File) (string, error) {
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
func tarFiles(files []File) (io.Reader, error) {
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
// without a healthcheck count as healthy once running.
func (c *Client) WaitHealthy(ctx context.Context, id string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		res, err := c.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
		if err != nil {
			return err
		}
		st := res.Container.State
		switch {
		case st == nil:
		case !st.Running && st.Status != container.StateCreated:
			return fmt.Errorf("container exited (code %d)", st.ExitCode)
		case st.Health == nil && st.Running:
			return nil
		case st.Health != nil && st.Health.Status == container.Healthy:
			return nil
		case st.Health != nil && st.Health.Status == container.Unhealthy:
			return errors.New("container is unhealthy")
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("not healthy after %s", timeout)
		case <-tick.C:
		}
	}
}

// RemoveContainerAndVolumes force-removes a container with its anonymous volumes.
func (c *Client) RemoveContainerAndVolumes(ctx context.Context, id string) error {
	_, err := c.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
	if err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("remove container: %w", err)
	}
	return nil
}

// ListNetworksMoby returns the networks the manager created.
func (c *Client) ListNetworksMoby(ctx context.Context) ([]network.Summary, error) {
	f := make(client.Filters)
	f.Add("label", LabelManaged+"=true")
	res, err := c.NetworkList(ctx, client.NetworkListOptions{Filters: f})
	if err != nil {
		return nil, err
	}
	return res.Items, nil
}
