package deploy

import (
	"context"
	"maps"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/kipit-labs/kipitiny/internal/usecase/data"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/selfcontainer"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
)

// Topology is what runs where: per server, how traffic gets in (entrypoints,
// tunnel, Traefik), the networks, and each project's containers with the
// networks they are actually attached to.
type Topology struct {
	Servers []ServerTopology `json:"servers"`
}

type ServerTopology struct {
	ID   string            `json:"id"`
	Name string            `json:"name"`
	Kind domain.ServerKind `json:"kind"`
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
	// ProjectID is empty for the proxy network and the ones created by hand.
	ProjectID string `json:"projectId,omitempty"`
	// Custom is the name of a network created by hand, and CustomID its ID.
	Custom   string `json:"custom,omitempty"`
	CustomID string `json:"customId,omitempty"`
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
	ID      string `json:"id"`
	Name    string `json:"name"`
	Network string `json:"network"`
	// GitPath is the compose file a project linked to git follows: its
	// services are changed there, not from the map.
	GitPath string `json:"gitPath,omitempty"`
	// ParentID is set on a preview: the project it copies.
	ParentID string        `json:"parentId,omitempty"`
	Services []TopoService `json:"services"`
}

type TopoService struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Kind     domain.ServiceKind `json:"kind"`
	Image    string             `json:"image"`
	Icon     string             `json:"icon,omitempty"`
	Domain   string             `json:"domain,omitempty"`
	Port     int                `json:"port,omitempty"`
	Replicas int                `json:"replicas"`
	Stopped  bool               `json:"stopped,omitempty"`
	Sleeping bool               `json:"sleeping,omitempty"`
	// Preview is "only" or "skip" (domain.Service.Preview).
	Preview string `json:"preview,omitempty"`
	// Volume holds a database's data.
	Volume string `json:"volume,omitempty"`
	// Uses are the IDs of the project databases its env references.
	Uses []string `json:"uses"`
	// Networks are the IDs of the networks created by hand it joins.
	Networks    []string `json:"networks"`
	HostNetwork bool     `json:"hostNetwork,omitempty"`
	// PublishedPorts are host ports published straight to the service.
	PublishedPorts []domain.PublishedPort `json:"publishedPorts,omitempty"`
	// RoutePending: the domain or port changed since the running deployment.
	RoutePending bool `json:"routePending,omitempty"`
	// NetworkIssues are the networks its running containers miss, as the
	// service view reports them.
	NetworkIssues []string `json:"networkIssues,omitempty"`
	// DNS is set when the manager manages the domain's Cloudflare record.
	DNS        *network.DNSStatus `json:"dns,omitempty"`
	Containers []TopoNode         `json:"containers"`
}

// Topology maps every server, or only the server and project of projectID.
func (c *Deployer) Topology(ctx context.Context, projectID string) (Topology, error) {
	var projects []domain.Project
	if projectID != "" {
		p, err := c.store.GetProject(ctx, projectID)
		if err != nil {
			return Topology{}, err
		}
		projects = []domain.Project{p}
	} else {
		ps, err := c.store.ListProjects(ctx)
		if err != nil {
			return Topology{}, err
		}
		projects = slices.DeleteFunc(ps, func(p domain.Project) bool { return !identity.Visible(ctx, p.ID) })
	}
	servers, err := c.store.ListServers(ctx)
	if err != nil {
		return Topology{}, err
	}
	if projectID != "" {
		servers = slices.DeleteFunc(servers, func(sv domain.Server) bool { return sv.ID != projects[0].ServerID })
	}
	svcs, err := c.store.ListAllServices(ctx)
	if err != nil {
		return Topology{}, err
	}
	links, err := c.store.ListProjectGit(ctx)
	if err != nil {
		return Topology{}, err
	}
	gitPaths := make(map[string]string, len(links))
	for _, l := range links {
		gitPaths[l.ProjectID] = l.Path
	}

	out := Topology{Servers: make([]ServerTopology, len(servers))}
	done := make(chan struct{})
	for i, sv := range servers {
		var mine []domain.Project
		for _, p := range projects {
			if p.ServerID == sv.ID {
				mine = append(mine, p)
			}
		}
		go func() {
			defer func() { done <- struct{}{} }()
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			out.Servers[i] = c.serverTopology(ctx, sv, mine, svcs, gitPaths)
		}()
	}
	for range servers {
		<-done
	}
	return out, nil
}

func (c *Deployer) serverTopology(ctx context.Context, sv domain.Server, projects []domain.Project, svcs []domain.Service, gitPaths map[string]string) ServerTopology {
	t := ServerTopology{ID: sv.ID, Name: sv.Name, Kind: sv.Kind, Traefik: c.cfg.Traefik.Enabled, Entrypoints: []Entrypoint{}, Networks: []TopoNetwork{}, Projects: []TopoProject{}}
	if t.Traefik {
		t.Tunnel = c.mgr.TunnelToken(sv) != ""
		t.Entrypoints = c.entrypoints(t.Tunnel)
	}

	dk := c.mgr.DockerFor(sv.ID)
	var cts []domain.Container
	nets := map[string]domain.DockerNetwork{}
	cts, err := dk.ListContainers(ctx, map[string]string{domain.LabelManaged: "true"})
	if err == nil {
		var ns []domain.DockerNetwork
		if ns, err = dk.ListNetworks(ctx); err == nil {
			for _, n := range ns {
				nets[n.Name] = n
			}
		}
	}
	if err != nil {
		t.Error = err.Error()
	}

	t.Networks = append(t.Networks, topoNetwork(nets, domain.ProxyNetwork, "", err == nil))
	if custom, cerr := c.store.ListNetworks(ctx); cerr == nil {
		for _, n := range custom {
			if n.ServerID == sv.ID {
				tn := topoNetwork(nets, domain.CustomNetwork(n.ID), "", err == nil)
				tn.Custom, tn.CustomID = n.Name, n.ID
				t.Networks = append(t.Networks, tn)
			}
		}
	}
	for _, ct := range cts {
		switch ct.Labels[domain.LabelComponent] {
		case network.TraefikComponent:
			t.Proxy = topoNode(ct, componentView(ct))
		case network.TunnelComponent:
			t.Cloudflared = topoNode(ct, componentView(ct))
		}
	}
	if sv.Kind == domain.ServerLocal {
		t.Manager = c.managerNode(ctx, dk)
	}

	byService := map[string][]domain.Container{}
	for _, ct := range cts {
		if id := ct.Labels[domain.LabelService]; id != "" {
			byService[id] = append(byService[id], ct)
		}
	}
	for _, p := range projects {
		var own []domain.Service
		for _, s := range svcs {
			if s.ProjectID == p.ID {
				own = append(own, s)
			}
		}
		netName := domain.ProjectNetwork(p.ID)
		t.Networks = append(t.Networks, topoNetwork(nets, netName, p.ID, err == nil))
		tp := TopoProject{ID: p.ID, Name: p.Name, Network: netName, GitPath: gitPaths[p.ID], ParentID: p.ParentID, Services: make([]TopoService, 0, len(own))}
		dbs := databasesOf(own)
		for _, s := range own {
			ts := TopoService{
				ID: s.ID, Name: s.Name, Kind: s.Kind, Image: s.Image, Icon: s.Icon, Domain: s.Domain, Port: s.Port,
				Replicas: s.Replicas, Stopped: s.Stopped, Sleeping: s.Sleeping, Preview: s.Preview, Uses: usedDatabases(s, p, dbs), Networks: s.Networks, HostNetwork: s.HostNetwork,
				PublishedPorts: s.PublishedPorts, RoutePending: c.routePending(ctx, s), Containers: []TopoNode{},
				NetworkIssues: networkIssues(p, s, byService[s.ID]),
			}
			if s.Domain != "" {
				ts.DNS = c.mgr.DNSStatusOf(ctx, s.Domain)
			}
			if ts.Networks == nil {
				ts.Networks = []string{}
			}
			ts.Volume = data.DataVolume(s)
			views := make([]ContainerView, 0, len(byService[s.ID]))
			nodes := map[string]domain.Container{}
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
func (c *Deployer) entrypoints(tunnel bool) []Entrypoint {
	web := Entrypoint{Name: "web", Port: 80, RedirectTo: "websecure"}
	secure := Entrypoint{Name: "websecure", Port: 443}
	if !tunnel {
		web.HostPort = c.cfg.Traefik.HTTPPort
		secure.HostPort = c.cfg.Traefik.HTTPSPort
	}
	return []Entrypoint{web, secure}
}

func (c *Deployer) managerNode(ctx context.Context, dk domain.ContainerRuntime) *ManagerNode {
	m := &ManagerNode{}
	if c.cfg.Traefik.Enabled {
		m.Domain = c.cfg.Domain
	}
	_, port, _ := net.SplitHostPort(c.cfg.Addr)
	id := selfcontainer.ID()
	if id == "" {
		m.Upstream = network.ManagerHost + ":" + port
		return m
	}
	m.Upstream = network.ManagerAlias + ":" + port
	if cts, err := dk.FindContainers(ctx, true, domain.Filter{}.Add("id", id)); err == nil && len(cts) == 1 {
		m.Container = topoNode(cts[0], componentView(cts[0]))
	}
	return m
}

func topoNetwork(nets map[string]domain.DockerNetwork, name, projectID string, known bool) TopoNetwork {
	tn := TopoNetwork{Name: name, ProjectID: projectID}
	n, ok := nets[name]
	if !ok {
		tn.Missing = known
		return tn
	}
	if len(n.Subnets) > 0 {
		tn.Subnet, tn.Gateway = n.Subnets[0].Subnet, n.Subnets[0].Gateway
	}
	return tn
}

func topoNode(ct domain.Container, v ContainerView) *TopoNode {
	n := &TopoNode{ContainerView: v, Endpoints: []TopoEndpoint{}}
	for name, ep := range ct.Networks {
		e := TopoEndpoint{Network: name, Aliases: ep.Aliases}
		if ep.IP.IsValid() {
			e.IP = ep.IP.String()
		}
		n.Endpoints = append(n.Endpoints, e)
	}
	slices.SortFunc(n.Endpoints, func(a, b TopoEndpoint) int { return strings.Compare(a.Network, b.Network) })
	return n
}

// componentView describes a container that belongs to no service.
func componentView(ct domain.Container) ContainerView {
	v := containerView(ct, domain.Service{})
	v.Retired = false
	return v
}

// usedDatabases returns the IDs of the databases svc's env references,
// directly or through a project variable.
func usedDatabases(svc domain.Service, p domain.Project, dbs map[string]domain.Service) []string {
	seen := map[string]bool{}
	var scan func(v string, depth int)
	scan = func(v string, depth int) {
		for _, m := range domain.EnvRefRe.FindAllStringSubmatch(v, -1) {
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
