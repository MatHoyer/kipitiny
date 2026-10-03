package core

import (
	"context"
	"maps"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// Topology is what runs where: per server, how traffic gets in (entrypoints,
// tunnel, Traefik), the networks, and each project's containers with the
// networks they are actually attached to.
type Topology struct {
	Servers []ServerTopology `json:"servers"`
}

type ServerTopology struct {
	ID   string           `json:"id"`
	Name string           `json:"name"`
	Kind store.ServerKind `json:"kind"`
	// Error is set when the server's Docker could not be read; the rest
	// then only holds what the store knows.
	Error string `json:"error,omitempty"`
	// Traefik is false when the manager doesn't run Traefik (then routing
	// is the user's own proxy and Entrypoints is empty).
	Traefik     bool          `json:"traefik"`
	Tunnel      bool          `json:"tunnel"`
	Entrypoints []Entrypoint  `json:"entrypoints"`
	Proxy       *TopoNode     `json:"proxy,omitempty"`
	Cloudflared *TopoNode     `json:"cloudflared,omitempty"`
	Manager     *ManagerNode  `json:"manager,omitempty"`
	Networks    []TopoNetwork `json:"networks"`
	Projects    []TopoProject `json:"projects"`
}

// Entrypoint is a Traefik entrypoint and the host port publishing it ("" when
// behind a tunnel).
type Entrypoint struct {
	Name       string `json:"name"`
	Port       int    `json:"port"`
	HostPort   string `json:"hostPort,omitempty"`
	RedirectTo string `json:"redirectTo,omitempty"`
}

type TopoNetwork struct {
	Name    string `json:"name"`
	Subnet  string `json:"subnet,omitempty"`
	Gateway string `json:"gateway,omitempty"`
	// ProjectID is empty for the proxy network.
	ProjectID string `json:"projectId,omitempty"`
	// Missing: expected (a project exists) but not found on the server.
	Missing bool `json:"missing,omitempty"`
}

// TopoNode is a container with the networks it is attached to.
type TopoNode struct {
	ContainerView
	Endpoints []TopoEndpoint `json:"endpoints"`
}

type TopoEndpoint struct {
	Network string   `json:"network"`
	IP      string   `json:"ip,omitempty"`
	Aliases []string `json:"aliases,omitempty"`
}

type ManagerNode struct {
	Domain string `json:"domain,omitempty"`
	// Upstream is the address Traefik forwards the manager's domain to.
	Upstream string `json:"upstream"`
	// Container is nil when the manager runs on the host.
	Container *TopoNode `json:"container,omitempty"`
}

type TopoProject struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Network  string        `json:"network"`
	Services []TopoService `json:"services"`
}

type TopoService struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Kind     store.ServiceKind `json:"kind"`
	Image    string            `json:"image"`
	Domain   string            `json:"domain,omitempty"`
	Port     int               `json:"port,omitempty"`
	Replicas int               `json:"replicas"`
	Stopped  bool              `json:"stopped,omitempty"`
	// Volume holds a database's data.
	Volume string `json:"volume,omitempty"`
	// Uses are the IDs of the project databases its env references.
	Uses       []string   `json:"uses"`
	Containers []TopoNode `json:"containers"`
}

// Topology maps every server, or only the server and project of projectID.
func (c *Core) Topology(ctx context.Context, projectID string) (Topology, error) {
	var projects []store.Project
	if projectID != "" {
		p, err := c.store.GetProject(ctx, projectID)
		if err != nil {
			return Topology{}, err
		}
		projects = []store.Project{p}
	} else {
		ps, err := c.store.ListProjects(ctx)
		if err != nil {
			return Topology{}, err
		}
		projects = ps
	}
	servers, err := c.store.ListServers(ctx)
	if err != nil {
		return Topology{}, err
	}
	if projectID != "" {
		servers = slices.DeleteFunc(servers, func(sv store.Server) bool { return sv.ID != projects[0].ServerID })
	}
	svcs, err := c.store.ListAllServices(ctx)
	if err != nil {
		return Topology{}, err
	}

	out := Topology{Servers: make([]ServerTopology, len(servers))}
	done := make(chan struct{})
	for i, sv := range servers {
		var mine []store.Project
		for _, p := range projects {
			if p.ServerID == sv.ID {
				mine = append(mine, p)
			}
		}
		go func() {
			defer func() { done <- struct{}{} }()
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			out.Servers[i] = c.serverTopology(ctx, sv, mine, svcs)
		}()
	}
	for range servers {
		<-done
	}
	return out, nil
}

func (c *Core) serverTopology(ctx context.Context, sv store.Server, projects []store.Project, svcs []store.Service) ServerTopology {
	t := ServerTopology{ID: sv.ID, Name: sv.Name, Kind: sv.Kind, Traefik: c.cfg.Traefik.Enabled, Entrypoints: []Entrypoint{}, Networks: []TopoNetwork{}, Projects: []TopoProject{}}
	if t.Traefik {
		t.Tunnel = c.tunnelToken(sv) != ""
		t.Entrypoints = c.entrypoints(t.Tunnel)
	}

	dk := c.dockerFor(sv.ID)
	var cts []container.Summary
	nets := map[string]network.Summary{}
	cts, err := dk.ListContainers(ctx, map[string]string{docker.LabelManaged: "true"})
	if err == nil {
		var ns []network.Summary
		if ns, err = dk.ListNetworks(ctx); err == nil {
			for _, n := range ns {
				nets[n.Name] = n
			}
		}
	}
	if err != nil {
		t.Error = err.Error()
	}

	t.Networks = append(t.Networks, topoNetwork(nets, docker.ProxyNetwork, "", err == nil))
	for _, ct := range cts {
		switch ct.Labels[docker.LabelComponent] {
		case traefikComponent:
			t.Proxy = topoNode(ct, componentView(ct))
		case tunnelComponent:
			t.Cloudflared = topoNode(ct, componentView(ct))
		}
	}
	if sv.Kind == store.ServerLocal {
		t.Manager = c.managerNode(ctx, dk)
	}

	byService := map[string][]container.Summary{}
	for _, ct := range cts {
		if id := ct.Labels[docker.LabelService]; id != "" {
			byService[id] = append(byService[id], ct)
		}
	}
	for _, p := range projects {
		var own []store.Service
		for _, s := range svcs {
			if s.ProjectID == p.ID {
				own = append(own, s)
			}
		}
		netName := docker.ProjectNetwork(p.ID)
		t.Networks = append(t.Networks, topoNetwork(nets, netName, p.ID, err == nil))
		tp := TopoProject{ID: p.ID, Name: p.Name, Network: netName, Services: make([]TopoService, 0, len(own))}
		dbs := databasesOf(own)
		for _, s := range own {
			ts := TopoService{
				ID: s.ID, Name: s.Name, Kind: s.Kind, Image: s.Image, Domain: s.Domain, Port: s.Port,
				Replicas: s.Replicas, Stopped: s.Stopped, Uses: usedDatabases(s, p, dbs), Containers: []TopoNode{},
			}
			ts.Volume = DataVolume(s)
			views := make([]ContainerView, 0, len(byService[s.ID]))
			nodes := map[string]container.Summary{}
			for _, ct := range byService[s.ID] {
				v := containerView(ct, s)
				views = append(views, v)
				nodes[v.ID] = ct
			}
			for _, v := range sortedContainers(views) {
				ts.Containers = append(ts.Containers, *topoNode(nodes[v.ID], v))
			}
			tp.Services = append(tp.Services, ts)
		}
		t.Projects = append(t.Projects, tp)
	}
	return t
}

// entrypoints mirrors the ones traefikSpec declares.
func (c *Core) entrypoints(tunnel bool) []Entrypoint {
	web := Entrypoint{Name: "web", Port: 80, RedirectTo: "websecure"}
	secure := Entrypoint{Name: "websecure", Port: 443}
	if !tunnel {
		web.HostPort = c.cfg.Traefik.HTTPPort
		secure.HostPort = c.cfg.Traefik.HTTPSPort
	}
	return []Entrypoint{web, secure}
}

func (c *Core) managerNode(ctx context.Context, dk *docker.Client) *ManagerNode {
	m := &ManagerNode{}
	if c.cfg.Traefik.Enabled {
		m.Domain = c.cfg.Domain
	}
	_, port, _ := net.SplitHostPort(c.cfg.Addr)
	id := docker.SelfContainerID()
	if id == "" {
		m.Upstream = managerHost + ":" + port
		return m
	}
	m.Upstream = managerAlias + ":" + port
	f := make(client.Filters)
	f.Add("id", id)
	if res, err := dk.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: f}); err == nil && len(res.Items) == 1 {
		m.Container = topoNode(res.Items[0], componentView(res.Items[0]))
	}
	return m
}

func topoNetwork(nets map[string]network.Summary, name, projectID string, known bool) TopoNetwork {
	tn := TopoNetwork{Name: name, ProjectID: projectID}
	n, ok := nets[name]
	if !ok {
		tn.Missing = known
		return tn
	}
	if len(n.IPAM.Config) > 0 {
		cfg := n.IPAM.Config[0]
		if cfg.Subnet.IsValid() {
			tn.Subnet = cfg.Subnet.String()
		}
		if cfg.Gateway.IsValid() {
			tn.Gateway = cfg.Gateway.String()
		}
	}
	return tn
}

func topoNode(ct container.Summary, v ContainerView) *TopoNode {
	n := &TopoNode{ContainerView: v, Endpoints: []TopoEndpoint{}}
	if ct.NetworkSettings != nil {
		for name, ep := range ct.NetworkSettings.Networks {
			e := TopoEndpoint{Network: name}
			if ep != nil {
				if ep.IPAddress.IsValid() {
					e.IP = ep.IPAddress.String()
				}
				e.Aliases = ep.Aliases
			}
			n.Endpoints = append(n.Endpoints, e)
		}
	}
	slices.SortFunc(n.Endpoints, func(a, b TopoEndpoint) int { return strings.Compare(a.Network, b.Network) })
	return n
}

// componentView describes a container that belongs to no service.
func componentView(ct container.Summary) ContainerView {
	v := containerView(ct, store.Service{})
	v.Retired = false
	return v
}

// usedDatabases returns the IDs of the databases svc's env references,
// directly or through a project variable.
func usedDatabases(svc store.Service, p store.Project, dbs map[string]store.Service) []string {
	seen := map[string]bool{}
	var scan func(v string, depth int)
	scan = func(v string, depth int) {
		for _, m := range envRefRe.FindAllStringSubmatch(v, -1) {
			if db, ok := dbs[m[2]]; ok && db.ID != svc.ID {
				seen[db.ID] = true
			}
			if m[1] != "" && depth == 0 {
				scan(p.Env[m[1]], 1)
			}
		}
	}
	for _, v := range svc.Env {
		scan(v, 0)
	}
	return append([]string{}, slices.Sorted(maps.Keys(seen))...)
}
