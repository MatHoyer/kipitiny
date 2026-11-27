package docker

import (
	"context"
	"fmt"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// ConnectNetwork attaches a container to a network under the given aliases,
// unless it is already attached.
func (c *Client) ConnectNetwork(ctx context.Context, name, containerID string, aliases ...string) error {
	res, err := c.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if err != nil {
		return fmt.Errorf("inspect container %s: %w", containerID, err)
	}
	if ns := res.Container.NetworkSettings; ns != nil && ns.Networks[name] != nil {
		return nil
	}
	_, err = c.NetworkConnect(ctx, name, client.NetworkConnectOptions{
		Container:      containerID,
		EndpointConfig: &network.EndpointSettings{Aliases: aliases},
	})
	if err != nil && !cerrdefs.IsConflict(err) {
		return fmt.Errorf("connect %s to %s: %w", containerID, name, err)
	}
	return nil
}
