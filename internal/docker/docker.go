// Package docker wraps the Docker Engine API client and holds the label and
// network conventions shared by everything the manager creates.
package docker

import (
	"context"
	"fmt"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"
)

const (
	// ProxyNetwork is shared by Traefik and every public-facing container.
	ProxyNetwork = "kipitiny-proxy"

	LabelManaged = "kipitiny.managed"
	LabelProject = "kipitiny.project"
	LabelService = "kipitiny.service"
	LabelReplica = "kipitiny.replica"
	LabelDeploy  = "kipitiny.deploy"
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
