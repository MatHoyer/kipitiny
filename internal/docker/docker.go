// Package docker wraps the Docker Engine API client and holds the label and
// network conventions shared by everything the manager creates.
package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

const (
	// ProxyNetwork is shared by Traefik and every public-facing container.
	ProxyNetwork = "kipitiny-proxy"

	LabelManaged   = "kipitiny.managed"
	LabelComponent = "kipitiny.component"
	LabelProject   = "kipitiny.project"
	LabelService   = "kipitiny.service"
	LabelReplica   = "kipitiny.replica"
	LabelDeploy    = "kipitiny.deploy"
	// LabelConfigHash lets the manager detect when a container it owns needs
	// to be recreated because its desired configuration changed.
	LabelConfigHash = "kipitiny.config-hash"
)

// ProjectNetwork is the private network shared by all containers of a project.
func ProjectNetwork(projectID string) string {
	return "kipitiny-" + projectID
}

type Client struct {
	*client.Client
}

// New connects using DOCKER_HOST et al., defaulting to the local socket.
func New() (*Client, error) {
	c, err := client.New(client.FromEnv)
	if err != nil {
		return nil, err
	}
	return &Client{c}, nil
}

type Info struct {
	Version    string `json:"version"`
	APIVersion string `json:"apiVersion"`
	OS         string `json:"os"`
	Arch       string `json:"arch"`
}

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

// ListContainers returns all containers (running or not) matching every label.
func (c *Client) ListContainers(ctx context.Context, labels map[string]string) ([]container.Summary, error) {
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

// PullImage pulls ref, writing one line per status change (not per progress
// tick) to w.
func (c *Client) PullImage(ctx context.Context, ref string, w io.Writer) error {
	resp, err := c.ImagePull(ctx, ref, client.ImagePullOptions{})
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

// EnsureImage pulls ref only if it is not present locally.
func (c *Client) EnsureImage(ctx context.Context, ref string) error {
	if _, err := c.ImageInspect(ctx, ref); err == nil {
		return nil
	} else if !cerrdefs.IsNotFound(err) {
		return err
	}
	return c.PullImage(ctx, ref, io.Discard)
}

// Run creates and starts a container. If start fails the container is removed.
func (c *Client) Run(ctx context.Context, opts client.ContainerCreateOptions) (string, error) {
	res, err := c.ContainerCreate(ctx, opts)
	if err != nil {
		return "", fmt.Errorf("create container %s: %w", opts.Name, err)
	}
	if _, err := c.ContainerStart(ctx, res.ID, client.ContainerStartOptions{}); err != nil {
		_, _ = c.ContainerRemove(context.WithoutCancel(ctx), res.ID, client.ContainerRemoveOptions{Force: true})
		return "", fmt.Errorf("start container %s: %w", opts.Name, err)
	}
	return res.ID, nil
}

// DefaultLogConfig caps on-disk container logs (local driver is compressed).
func DefaultLogConfig() container.LogConfig {
	return container.LogConfig{
		Type:   "local",
		Config: map[string]string{"max-size": "10m", "max-file": "3"},
	}
}
