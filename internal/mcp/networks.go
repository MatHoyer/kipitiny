package mcp

import (
	"context"
	"fmt"
	"slices"

	"github.com/kipit-labs/kipitiny/internal/usecase/deploy"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kipit-labs/kipitiny/internal/core"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/servers"
)

// NetworkSummary is a network created by hand and the services on it, by the
// name the others reach them at.
type NetworkSummary struct {
	Name       string   `json:"name"`
	Server     string   `json:"server"`
	DockerName string   `json:"dockerName"`
	Services   []string `json:"services" jsonschema:"project/service = alias on the network"`
}

type listNetworksOut struct {
	Networks []NetworkSummary `json:"networks"`
}

func (t *tools) listNetworks(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listNetworksOut, error) {
	if err := identity.Authenticated(ctx); err != nil {
		return nil, listNetworksOut{}, err
	}
	if err := identity.RequireFeature(ctx, identity.FeatureNetworks); err != nil {
		return nil, listNetworksOut{}, err
	}
	nets, err := t.c.ListNetworks(ctx)
	if err != nil {
		return nil, listNetworksOut{}, err
	}
	servers, err := t.serverNames(ctx)
	if err != nil {
		return nil, listNetworksOut{}, err
	}
	out := listNetworksOut{Networks: make([]NetworkSummary, len(nets))}
	for i, n := range nets {
		s := NetworkSummary{Name: n.Name, Server: servers[n.ServerID], DockerName: n.DockerName, Services: []string{}}
		for _, m := range n.Services {
			s.Services = append(s.Services, m.ProjectName+"/"+m.Name+" = "+m.Alias)
		}
		out.Networks[i] = s
	}
	return nil, out, nil
}

type createNetworkIn struct {
	Name   string `json:"name" jsonschema:"lowercase letters, digits and dashes"`
	Server string `json:"server,omitempty" jsonschema:"the server's name; this one by default"`
}

func (t *tools) createNetwork(ctx context.Context, _ *mcp.CallToolRequest, in createNetworkIn) (*mcp.CallToolResult, NetworkSummary, error) {
	out, err := mutate(ctx, t.c, "create_network", in.Name, func() (NetworkSummary, error) {
		if err := identity.RequireAdmin(ctx); err != nil {
			return NetworkSummary{}, err
		}
		serverID, err := t.serverID(ctx, in.Server)
		if err != nil {
			return NetworkSummary{}, err
		}
		n, err := t.c.CreateNetwork(ctx, deploy.NetworkInput{ServerID: serverID, Name: in.Name})
		return NetworkSummary{Name: n.Name, Server: in.Server, DockerName: n.DockerName, Services: []string{}}, err
	})
	return nil, out, err
}

type deleteNetworkIn struct {
	Network string `json:"network" jsonschema:"the network's name"`
	Server  string `json:"server,omitempty" jsonschema:"the server's name, when several have a network by that name"`
}

func (t *tools) deleteNetwork(ctx context.Context, _ *mcp.CallToolRequest, in deleteNetworkIn) (*mcp.CallToolResult, deleted, error) {
	out, err := mutate(ctx, t.c, "delete_network", in.Network, func() (deleted, error) {
		if err := identity.RequireAdmin(ctx); err != nil {
			return deleted{}, err
		}
		n, err := t.findNetwork(ctx, in.Network, in.Server)
		if err != nil {
			return deleted{}, err
		}
		return deleted{Deleted: n.Name}, t.c.DeleteNetwork(ctx, n.ID)
	})
	return nil, out, err
}

type setServiceNetworksIn struct {
	Service  string   `json:"service" jsonschema:"the service as project/service, or its ID"`
	Networks []string `json:"networks" jsonschema:"names of the networks it joins, on its server; replaces the current list, empty leaves them all"`
}

type serviceNetworksOut struct {
	Networks []string `json:"networks"`
	Alias    string   `json:"alias" jsonschema:"the name other services on these networks reach it at"`
}

func (t *tools) setServiceNetworks(ctx context.Context, _ *mcp.CallToolRequest, in setServiceNetworksIn) (*mcp.CallToolResult, serviceNetworksOut, error) {
	out, err := mutate(ctx, t.c, "set_service_networks", in.Service, func() (serviceNetworksOut, error) {
		svc, err := t.service(ctx, in.Service, identity.PermSettings)
		if err != nil {
			return serviceNetworksOut{}, err
		}
		nets, err := t.c.ListNetworks(ctx)
		if err != nil {
			return serviceNetworksOut{}, err
		}
		ids := make([]string, 0, len(in.Networks))
		for _, name := range in.Networks {
			i := slices.IndexFunc(nets, func(n deploy.NetworkView) bool { return n.Name == name && n.ServerID == svc.ServerID })
			if i < 0 {
				return serviceNetworksOut{}, fmt.Errorf("%w: network %s on the service's server", domain.ErrNotFound, name)
			}
			ids = append(ids, nets[i].ID)
		}
		if _, err := t.c.SetServiceNetworks(ctx, svc.ID, ids); err != nil {
			return serviceNetworksOut{}, err
		}
		p, err := t.findProjectByID(ctx, svc.ProjectID)
		if err != nil {
			return serviceNetworksOut{}, err
		}
		return serviceNetworksOut{Networks: in.Networks, Alias: p.Name + "-" + svc.Name}, nil
	})
	return nil, out, err
}

func (t *tools) findNetwork(ctx context.Context, name, server string) (deploy.NetworkView, error) {
	nets, err := t.c.ListNetworks(ctx)
	if err != nil {
		return deploy.NetworkView{}, err
	}
	serverID := ""
	if server != "" {
		if serverID, err = t.serverID(ctx, server); err != nil {
			return deploy.NetworkView{}, err
		}
	}
	var found []deploy.NetworkView
	for _, n := range nets {
		if n.Name == name && (serverID == "" || n.ServerID == serverID) {
			found = append(found, n)
		}
	}
	switch len(found) {
	case 0:
		return deploy.NetworkView{}, fmt.Errorf("%w: network %s", domain.ErrNotFound, name)
	case 1:
		return found[0], nil
	}
	return deploy.NetworkView{}, fmt.Errorf("%w: several servers have a network named %s; give the server", core.ErrInvalid, name)
}

// serverID resolves a server by name; empty is this one.
func (t *tools) serverID(ctx context.Context, name string) (string, error) {
	if name == "" {
		return domain.LocalServerID, nil
	}
	svs, err := t.c.ListServers(ctx)
	if err != nil {
		return "", err
	}
	i := slices.IndexFunc(svs, func(sv servers.ServerView) bool { return sv.Name == name })
	if i < 0 {
		return "", fmt.Errorf("%w: server %s", domain.ErrNotFound, name)
	}
	return svs[i].ID, nil
}

func (t *tools) serverNames(ctx context.Context) (map[string]string, error) {
	svs, err := t.c.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(svs))
	for _, sv := range svs {
		names[sv.ID] = sv.Name
	}
	return names, nil
}

func (t *tools) findProjectByID(ctx context.Context, id string) (domain.Project, error) {
	projects, err := t.c.ListProjects(ctx)
	if err != nil {
		return domain.Project{}, err
	}
	i := slices.IndexFunc(projects, func(p domain.Project) bool { return p.ID == id })
	if i < 0 {
		return domain.Project{}, fmt.Errorf("%w: project %s", domain.ErrNotFound, id)
	}
	return projects[i], nil
}
