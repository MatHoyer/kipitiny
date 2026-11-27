package network

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

const (
	maxRoutes     = 20
	maxRoutePaths = 20
	maxRoutePath  = 200
)

// routePathRe is a URL path prefix: no backtick, quote or space can end
// the Traefik rule it goes in.
var routePathRe = regexp.MustCompile(`^/[A-Za-z0-9._~!$&()*+,;=:@%/-]*$`)

// NormalizeRoutes trims the paths and drops the default scheme, so equal
// routes compare equal.
func NormalizeRoutes(in []domain.Route) []domain.Route {
	if in == nil {
		return nil
	}
	out := make([]domain.Route, 0, len(in))
	for _, r := range in {
		paths := make([]string, 0, len(r.Paths))
		for _, p := range r.Paths {
			paths = append(paths, strings.TrimSpace(p))
		}
		r.Paths = paths
		r.Scheme = strings.ToLower(strings.TrimSpace(r.Scheme))
		if r.Scheme == "http" {
			r.Scheme = ""
		}
		out = append(out, r)
	}
	return out
}

func ValidateRoutes(s domain.Service) error {
	if len(s.Routes) == 0 {
		return nil
	}
	if s.Domain == "" || s.Port < 1 {
		return fmt.Errorf("%w: routes need the domain and the container port", domain.ErrInvalid)
	}
	if len(s.Routes) > maxRoutes {
		return fmt.Errorf("%w: at most %d routes", domain.ErrInvalid, maxRoutes)
	}
	seen := map[string]bool{}
	for _, r := range s.Routes {
		if len(r.Paths) == 0 || len(r.Paths) > maxRoutePaths {
			return fmt.Errorf("%w: a route has 1 to %d paths", domain.ErrInvalid, maxRoutePaths)
		}
		for _, p := range r.Paths {
			if len(p) > maxRoutePath || !routePathRe.MatchString(p) {
				return fmt.Errorf("%w: route path %q must start with / and hold only URL path characters", domain.ErrInvalid, p)
			}
			if seen[p] {
				return fmt.Errorf("%w: route path %s is listed twice", domain.ErrInvalid, p)
			}
			seen[p] = true
		}
		if r.Scheme != "" && r.Scheme != "h2c" {
			return fmt.Errorf("%w: route scheme %q must be http or h2c", domain.ErrInvalid, r.Scheme)
		}
		if r.Port < 0 || r.Port > 65535 {
			return fmt.Errorf("%w: route port out of range", domain.ErrInvalid)
		}
	}
	return nil
}

func SameRoutes(a, b []domain.Route) bool {
	return slices.EqualFunc(a, b, func(x, y domain.Route) bool {
		return slices.Equal(x.Paths, y.Paths) && x.Scheme == y.Scheme && x.Port == y.Port
	})
}

// routePort is the container port r reaches on svc.
func routePort(svc domain.Service, r domain.Route) int {
	if r.Port > 0 {
		return r.Port
	}
	return svc.Port
}

const (
	// RoutePriority puts the routers of a route's paths above a domain's
	// catch-all (whose priority is its rule's length) and below
	// maintenancePriority. Longer paths win: /api/v2 over /api.
	RoutePriority = 1 << 20
)

// PathPriority is the priority of the app's router for path, or with app
// false of the page shown while the app is down: just below its own, above
// the app routers of shorter paths.
func PathPriority(path string, app bool) int {
	p := RoutePriority + 2*len(path)
	if app {
		p++
	}
	return p
}

// PathRule matches path on domain.
func PathRule(domain, path string) string {
	return "Host(`" + domain + "`) && PathPrefix(`" + path + "`)"
}

// RouteService names the Traefik service of r, after its configuration:
// replicas of two rollouts may carry different ones.
func RouteService(name string, svc domain.Service, r domain.Route) string {
	return name + "-" + SpecHash(r.Scheme, routePort(svc, r))[:8]
}

// CheckDomain checks that s can have its domain next to others, the other
// services as they will be: a domain belongs to one project, whose
// services split it by path, one of them (without routes) taking the rest.
//
// shortcut: check-then-write, so two concurrent writes claiming one domain
// can both pass; add a lock around validate and the store write if that
// ever matters.
func (c *Network) CheckDomain(ctx context.Context, s domain.Service, serverID string, others []domain.Service) error {
	if s.Domain == "" {
		return nil
	}
	var group []domain.Service
	for _, o := range others {
		if o.Domain != s.Domain {
			continue
		}
		if o.ProjectID != s.ProjectID {
			p, err := c.store.GetProject(ctx, o.ProjectID)
			if err != nil {
				return err
			}
			return fmt.Errorf("%w: domain %q is used by %s in project %s", domain.ErrInvalid, s.Domain, o.Name, p.Name)
		}
		group = append(group, o)
	}
	if len(group) == 0 {
		return nil
	}
	owner := map[string]string{}
	for _, o := range group {
		switch {
		case len(o.Routes) == 0 && len(s.Routes) == 0 && HTTPRouted(o) && HTTPRouted(s):
			return fmt.Errorf("%w: %s already takes the whole domain %q: give one of them routes", domain.ErrInvalid, o.Name, s.Domain)
		case o.Analytics && s.Analytics:
			return fmt.Errorf("%w: analytics already count %q through %s", domain.ErrInvalid, s.Domain, o.Name)
		}
		for _, r := range o.Routes {
			for _, p := range r.Paths {
				owner[p] = o.Name
			}
		}
	}
	for _, r := range s.Routes {
		for _, p := range r.Paths {
			if o, ok := owner[p]; ok {
				return fmt.Errorf("%w: path %s of %q is already routed to %s", domain.ErrInvalid, p, s.Domain, o)
			}
		}
	}
	if !c.ViaTunnel(ctx, serverID) {
		return nil
	}
	// The domain's one record points at the server when a service publishes
	// ports, and no HTTP port is open there behind a tunnel.
	for _, o := range group {
		if a, b := s, o; len(a.PublishedPorts) > 0 && HTTPRouted(b) || len(b.PublishedPorts) > 0 && HTTPRouted(a) {
			return fmt.Errorf("%w: behind a Cloudflare tunnel, %s and %s can't share %q: published ports need the record to point at the server, "+
				"which serves no HTTP behind a tunnel", domain.ErrInvalid, s.Name, o.Name, s.Domain)
		}
	}
	return nil
}

// domainDirect reports whether svc's domain points straight at the server,
// never through Cloudflare's proxy: a service on it publishes ports.
func (c *Network) domainDirect(ctx context.Context, svc domain.Service) bool {
	if len(svc.PublishedPorts) > 0 {
		return true
	}
	svcs, err := c.store.ListServices(ctx, svc.ProjectID)
	if err != nil {
		c.log.Warn("list the services sharing a domain", "service", svc.ID, "err", err)
		return false
	}
	return slices.ContainsFunc(svcs, func(o domain.Service) bool { return o.Domain == svc.Domain && len(o.PublishedPorts) > 0 })
}
