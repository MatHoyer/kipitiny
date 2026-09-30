package docker

import (
	"context"
	"fmt"
	"os"
	"regexp"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// Docker bind-mounts /etc/hostname etc. from /var/lib/docker/containers/<id>/.
var mountinfoID = regexp.MustCompile(`/containers/([0-9a-f]{64})/`)

// SelfContainerID returns the ID of the container this process runs in, or ""
// when it runs directly on a host.
func SelfContainerID() string {
	b, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return ""
	}
	return parseMountinfoID(b)
}

func parseMountinfoID(mountinfo []byte) string {
	if m := mountinfoID.FindSubmatch(mountinfo); m != nil {
		return string(m[1])
	}
	return ""
}

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
