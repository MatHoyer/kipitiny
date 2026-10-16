package core

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/moby/moby/api/types/network"

	"github.com/MatHoyer/kipitiny/internal/store"
)

const maxPublishedPorts = 20

// normalizePorts fills what users leave out: TCP, and the container port
// equal to the host port. nil becomes empty.
func normalizePorts(ports []store.PublishedPort) []store.PublishedPort {
	out := make([]store.PublishedPort, 0, len(ports))
	for _, p := range ports {
		p.Protocol = strings.ToLower(strings.TrimSpace(p.Protocol))
		if p.Protocol == "" {
			p.Protocol = "tcp"
		}
		if p.ContainerPort == 0 {
			p.ContainerPort = p.HostPort
		}
		if p.HostIP = strings.Trim(strings.TrimSpace(p.HostIP), "[]"); p.HostIP == "0.0.0.0" {
			p.HostIP = ""
		}
		out = append(out, p)
	}
	return out
}

// validatePorts checks an app's published ports. Two containers can't bind
// the same host port, so an app publishing ports runs a single replica.
func validatePorts(s store.Service) error {
	if len(s.PublishedPorts) == 0 {
		return nil
	}
	if len(s.PublishedPorts) > maxPublishedPorts {
		return fmt.Errorf("%w: at most %d published ports", ErrInvalid, maxPublishedPorts)
	}
	if s.Replicas != 1 {
		return fmt.Errorf("%w: an app with published ports runs a single replica: two containers can't bind the same host port", ErrInvalid)
	}
	for i, p := range s.PublishedPorts {
		if p.Protocol != "tcp" && p.Protocol != "udp" {
			return fmt.Errorf("%w: port protocol must be tcp or udp, not %q", ErrInvalid, p.Protocol)
		}
		if p.HostPort < 1 || p.HostPort > 65535 || p.ContainerPort < 1 || p.ContainerPort > 65535 {
			return fmt.Errorf("%w: published ports must be between 1 and 65535", ErrInvalid)
		}
		if _, err := netip.ParseAddr(p.HostIP); p.HostIP != "" && err != nil {
			return fmt.Errorf("%w: host IP %q is not an IP address", ErrInvalid, p.HostIP)
		}
		if slices.ContainsFunc(s.PublishedPorts[:i], func(o store.PublishedPort) bool { return portsClash(o, p) }) {
			return fmt.Errorf("%w: host port %s is published twice", ErrInvalid, portKey(p))
		}
	}
	return nil
}

// checkPortsFree refuses host ports Traefik or another service of the same
// server already publishes. Docker would only fail at deploy.
func (c *Core) checkPortsFree(ctx context.Context, s store.Service) error {
	if len(s.PublishedPorts) == 0 {
		return nil
	}
	type use struct {
		port store.PublishedPort
		by   string
	}
	var taken []use
	if c.cfg.Traefik.Enabled {
		for _, hp := range []string{c.cfg.Traefik.HTTPPort, c.cfg.Traefik.HTTPSPort} {
			n, _ := strconv.Atoi(hp)
			taken = append(taken, use{store.PublishedPort{HostPort: n, Protocol: "tcp"}, "Traefik"})
		}
	}
	serverID := s.ServerID
	if serverID == "" { // a new service runs on its project's server
		p, err := c.store.GetProject(ctx, s.ProjectID)
		if err != nil {
			return err
		}
		serverID = p.ServerID
	}
	svcs, err := c.store.ListAllServices(ctx)
	if err != nil {
		return err
	}
	for _, o := range svcs {
		if o.ID == s.ID || o.ServerID != serverID {
			continue
		}
		for _, p := range o.PublishedPorts {
			taken = append(taken, use{p, "service " + o.Name})
		}
	}
	for _, p := range s.PublishedPorts {
		for _, t := range taken {
			if portsClash(t.port, p) {
				return fmt.Errorf("%w: host port %s is already published by %s", ErrInvalid, portKey(p), t.by)
			}
		}
	}
	return nil
}

// portsClash reports whether two published ports bind the same host
// socket: same port and protocol, on the same address or on every one.
func portsClash(a, b store.PublishedPort) bool {
	return a.HostPort == b.HostPort && a.Protocol == b.Protocol && (a.HostIP == "" || b.HostIP == "" || a.HostIP == b.HostIP)
}

func portKey(p store.PublishedPort) string {
	key := strconv.Itoa(p.HostPort) + "/" + p.Protocol
	if p.HostIP != "" {
		key = net.JoinHostPort(p.HostIP, key)
	}
	return key
}

// portBindings publishes the app's ports, on every host address unless
// one is given.
func portBindings(ports []store.PublishedPort) (network.PortSet, network.PortMap) {
	if len(ports) == 0 {
		return nil, nil
	}
	exposed, bindings := network.PortSet{}, network.PortMap{}
	for _, p := range ports {
		cp, ok := network.PortFrom(uint16(p.ContainerPort), network.IPProtocol(p.Protocol))
		if !ok {
			continue // validated before
		}
		exposed[cp] = struct{}{}
		b := network.PortBinding{HostPort: strconv.Itoa(p.HostPort)}
		b.HostIP, _ = netip.ParseAddr(p.HostIP) // validated before; empty is every address
		bindings[cp] = append(bindings[cp], b)
	}
	return exposed, bindings
}

// probePort is the container port the health probe connects to: the HTTP
// port, else the first published TCP port; 0 when there is none.
func probePort(svc store.Service) int {
	if svc.Port > 0 {
		return svc.Port
	}
	for _, p := range svc.PublishedPorts {
		if p.Protocol == "tcp" {
			return p.ContainerPort
		}
	}
	return 0
}

// validateHostAccess checks an app's host network and Docker socket. In
// the host's network the app has no project network to be reached on and
// listens on host ports itself: one replica, no domain, nothing to publish.
func validateHostAccess(s store.Service) error {
	if s.DockerSocket != "" && s.DockerSocket != "ro" && s.DockerSocket != "rw" {
		return fmt.Errorf("%w: Docker socket access must be ro or rw", ErrInvalid)
	}
	if !s.HostNetwork {
		return nil
	}
	switch {
	case s.Replicas != 1:
		return fmt.Errorf("%w: an app in the host network runs one replica", ErrInvalid)
	case len(s.PublishedPorts) > 0:
		return fmt.Errorf("%w: an app in the host network listens on host ports itself; remove its published ports", ErrInvalid)
	case s.Domain != "" || !s.Middlewares.IsZero():
		return fmt.Errorf("%w: an app in the host network can't have a domain: Traefik reaches apps on their project network", ErrInvalid)
	case len(s.Networks) > 0:
		return fmt.Errorf("%w: an app in the host network can't join other networks; leave them first", ErrInvalid)
	}
	return nil
}
