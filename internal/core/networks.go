package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/moby/moby/api/types/container"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// Networks created by hand sit next to the automatic ones (proxy, one per
// project): services of any project on the same server join them to reach
// each other, as <project>-<service>. Membership is applied to running
// containers right away, and the reconciler keeps it in line.

type NetworkInput struct {
	ServerID string `json:"serverId"`
	Name     string `json:"name"`
}

type NetworkView struct {
	store.Network
	// DockerName is the network's name on the server.
	DockerName string          `json:"dockerName"`
	Services   []NetworkMember `json:"services"`
}

// NetworkMember is a service that joins a network, and the name the others
// reach it by.
type NetworkMember struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Kind        store.ServiceKind `json:"kind"`
	ProjectID   string            `json:"projectId"`
	ProjectName string            `json:"projectName"`
	Alias       string            `json:"alias"`
}

// networkAlias names a service on the networks created by hand, which
// services of several projects share.
func networkAlias(project store.Project, svc store.Service) string {
	return project.Name + "-" + svc.Name
}

func (c *Core) ListNetworks(ctx context.Context) ([]NetworkView, error) {
	nets, err := c.store.ListNetworks(ctx)
	if err != nil {
		return nil, err
	}
	projects, err := c.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[string]store.Project{}
	for _, p := range projects {
		byID[p.ID] = p
	}
	svcs, err := c.store.ListAllServices(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]NetworkView, 0, len(nets))
	for _, n := range nets {
		v := NetworkView{Network: n, DockerName: docker.CustomNetwork(n.ID), Services: []NetworkMember{}}
		for _, s := range svcs {
			if !slices.Contains(s.Networks, n.ID) {
				continue
			}
			p := byID[s.ProjectID]
			v.Services = append(v.Services, NetworkMember{
				ID: s.ID, Name: s.Name, Kind: s.Kind, ProjectID: p.ID, ProjectName: p.Name, Alias: networkAlias(p, s),
			})
		}
		slices.SortFunc(v.Services, func(a, b NetworkMember) int { return strings.Compare(a.Alias, b.Alias) })
		out = append(out, v)
	}
	return out, nil
}

// CreateNetwork records a network and creates it on its server.
func (c *Core) CreateNetwork(ctx context.Context, in NetworkInput) (NetworkView, error) {
	name := strings.TrimSpace(in.Name)
	if !nameRe.MatchString(name) {
		return NetworkView{}, fmt.Errorf("%w: name must be lowercase letters, digits and dashes (max 40)", ErrInvalid)
	}
	if in.ServerID == "" {
		in.ServerID = store.LocalServerID
	}
	if _, err := c.store.GetServer(ctx, in.ServerID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return NetworkView{}, fmt.Errorf("%w: unknown server", ErrInvalid)
		}
		return NetworkView{}, err
	}
	n, err := c.store.CreateNetwork(ctx, store.Network{ServerID: in.ServerID, Name: name})
	if errors.Is(err, store.ErrConflict) {
		return NetworkView{}, fmt.Errorf("%w: this server already has a network named %s", ErrInvalid, name)
	}
	if err != nil {
		return NetworkView{}, err
	}
	v := NetworkView{Network: n, DockerName: docker.CustomNetwork(n.ID), Services: []NetworkMember{}}
	if err := c.dockerFor(n.ServerID).EnsureNetwork(ctx, v.DockerName); err != nil {
		return v, fmt.Errorf("%w: network saved but not created on the server yet (retried in the background): %v", ErrInvalid, err)
	}
	return v, nil
}

// DeleteNetwork detaches its services and removes it. Their own project
// network is untouched, so nothing restarts.
func (c *Core) DeleteNetwork(ctx context.Context, id string) error {
	n, err := c.store.GetNetwork(ctx, id)
	if err != nil {
		return err
	}
	svcs, err := c.store.ListAllServices(ctx)
	if err != nil {
		return err
	}
	dk := c.dockerFor(n.ServerID)
	name := docker.CustomNetwork(n.ID)
	var errs []error
	for _, s := range svcs {
		if !slices.Contains(s.Networks, id) {
			continue
		}
		rest := slices.DeleteFunc(slices.Clone(s.Networks), func(x string) bool { return x == id })
		if err := c.store.SetServiceNetworks(ctx, s.ID, rest); err != nil {
			return err
		}
		cts, err := c.serviceContainers(ctx, s)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, ct := range cts {
			if onNetwork(ct, name) {
				errs = append(errs, dk.DisconnectNetwork(ctx, name, ct.ID))
			}
		}
	}
	// Containers kipitiny doesn't manage keep the network in use.
	if err := dk.RemoveNetwork(ctx, name); err != nil {
		return fmt.Errorf("%w: services detached, but the server kept the network (other containers on it?): %v", ErrInvalid, errors.Join(append(errs, err)...))
	}
	if err := c.store.DeleteNetwork(ctx, id); err != nil {
		return err
	}
	return errors.Join(errs...)
}

// SetServiceNetworks replaces the networks a service joins and attaches or
// detaches its containers now. It works on git projects too: a compose
// file doesn't describe these networks.
func (c *Core) SetServiceNetworks(ctx context.Context, serviceID string, networkIDs []string) (ServiceView, error) {
	svc, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return ServiceView{}, err
	}
	ids := []string{}
	for _, id := range networkIDs {
		if slices.Contains(ids, id) {
			continue
		}
		n, err := c.store.GetNetwork(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			return ServiceView{}, fmt.Errorf("%w: unknown network %s", ErrInvalid, id)
		}
		if err != nil {
			return ServiceView{}, err
		}
		if n.ServerID != svc.ServerID {
			return ServiceView{}, fmt.Errorf("%w: network %s is on another server than the service", ErrInvalid, n.Name)
		}
		ids = append(ids, id)
	}
	if svc.HostNetwork && len(ids) > 0 {
		return ServiceView{}, fmt.Errorf("%w: an app in the host network can't join other networks", ErrInvalid)
	}
	if err := c.store.SetServiceNetworks(ctx, svc.ID, ids); err != nil {
		return ServiceView{}, err
	}
	svc.Networks = ids
	project, err := c.store.GetProject(ctx, svc.ProjectID)
	if err != nil {
		return ServiceView{}, err
	}
	cts, err := c.serviceContainers(ctx, svc)
	if err == nil {
		err = c.syncNetworks(ctx, project, svc, cts, false)
	}
	if err != nil {
		return ServiceView{}, fmt.Errorf("%w: networks saved, but not applied to the containers yet (retried in the background): %v", ErrInvalid, err)
	}
	return c.view(ctx, svc)
}

// syncNetworks attaches the service's active containers to the networks
// created by hand it joins and detaches them from the others. renamed also
// moves the containers already attached to the current alias (which resets
// their connections there); container lists don't report aliases.
func (c *Core) syncNetworks(ctx context.Context, project store.Project, svc store.Service, cts []container.Summary, renamed bool) error {
	if svc.HostNetwork {
		return nil
	}
	dk := c.dockerFor(svc.ServerID)
	alias := networkAlias(project, svc)
	var errs []error
	for _, ct := range cts {
		if !isActive(ct, svc) || ct.NetworkSettings == nil {
			continue
		}
		for name := range ct.NetworkSettings.Networks {
			if !docker.IsCustomNetwork(name) {
				continue
			}
			switch {
			case !slices.ContainsFunc(svc.Networks, func(id string) bool { return docker.CustomNetwork(id) == name }):
				errs = append(errs, dk.DisconnectNetwork(ctx, name, ct.ID))
			case renamed:
				errs = append(errs, dk.SetAliases(ctx, name, ct.ID, []string{alias}))
			}
		}
		for _, id := range svc.Networks {
			name := docker.CustomNetwork(id)
			if onNetwork(ct, name) {
				continue
			}
			if err := dk.EnsureNetwork(ctx, name); err != nil {
				errs = append(errs, err)
				continue
			}
			errs = append(errs, dk.ConnectNetwork(ctx, name, ct.ID, alias))
		}
	}
	return errors.Join(errs...)
}

// ensureNetworks creates a server's networks that are missing, e.g. after
// the server was reinstalled.
func (c *Core) ensureNetworks(ctx context.Context, sv store.Server) error {
	nets, err := c.store.ListNetworks(ctx)
	if err != nil {
		return err
	}
	dk := c.dockerFor(sv.ID)
	var errs []error
	for _, n := range nets {
		if n.ServerID == sv.ID {
			errs = append(errs, dk.EnsureNetwork(ctx, docker.CustomNetwork(n.ID)))
		}
	}
	return errors.Join(errs...)
}
