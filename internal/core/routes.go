package core

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	maxRoutes     = 20
	maxRoutePaths = 20
	maxRoutePath  = 200
)

// routePathRe is a URL path prefix: no backtick, quote or space can end
// the Traefik rule it goes in.
var routePathRe = regexp.MustCompile(`^/[A-Za-z0-9._~!$&()*+,;=:@%/-]*$`)

// normalizeRoutes trims the paths and drops the default scheme, so equal
// routes compare equal.
func normalizeRoutes(in []store.Route) []store.Route {
	if in == nil {
		return nil
	}
	out := make([]store.Route, 0, len(in))
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

func validateRoutes(s store.Service) error {
	if len(s.Routes) == 0 {
		return nil
	}
	if s.Domain == "" || s.Port < 1 {
		return fmt.Errorf("%w: routes need the domain and the container port", ErrInvalid)
	}
	if len(s.Routes) > maxRoutes {
		return fmt.Errorf("%w: at most %d routes", ErrInvalid, maxRoutes)
	}
	seen := map[string]bool{}
	for _, r := range s.Routes {
		if len(r.Paths) == 0 || len(r.Paths) > maxRoutePaths {
			return fmt.Errorf("%w: a route has 1 to %d paths", ErrInvalid, maxRoutePaths)
		}
		for _, p := range r.Paths {
			if len(p) > maxRoutePath || !routePathRe.MatchString(p) {
				return fmt.Errorf("%w: route path %q must start with / and hold only URL path characters", ErrInvalid, p)
			}
			if seen[p] {
				return fmt.Errorf("%w: route path %s is listed twice", ErrInvalid, p)
			}
			seen[p] = true
		}
		if r.Scheme != "" && r.Scheme != "h2c" {
			return fmt.Errorf("%w: route scheme %q must be http or h2c", ErrInvalid, r.Scheme)
		}
		if r.Port < 0 || r.Port > 65535 {
			return fmt.Errorf("%w: route port out of range", ErrInvalid)
		}
	}
	return nil
}

func sameRoutes(a, b []store.Route) bool {
	return slices.EqualFunc(a, b, func(x, y store.Route) bool {
		return slices.Equal(x.Paths, y.Paths) && x.Scheme == y.Scheme && x.Port == y.Port
	})
}

// routePort is the container port r reaches on svc.
func routePort(svc store.Service, r store.Route) int {
	if r.Port > 0 {
		return r.Port
	}
	return svc.Port
}

const (
	// routePriority puts the routers of a route's paths above a domain's
	// catch-all (whose priority is its rule's length) and below
	// maintenancePriority. Longer paths win: /api/v2 over /api.
	routePriority = 1 << 20
)

// pathPriority is the priority of the app's router for path, or with app
// false of the page shown while the app is down: just below its own, above
// the app routers of shorter paths.
func pathPriority(path string, app bool) int {
	p := routePriority + 2*len(path)
	if app {
		p++
	}
	return p
}

// pathRule matches path on domain.
func pathRule(domain, path string) string {
	return "Host(`" + domain + "`) && PathPrefix(`" + path + "`)"
}

// routeService names the Traefik service of r, after its configuration:
// replicas of two rollouts may carry different ones.
func routeService(name string, svc store.Service, r store.Route) string {
	return name + "-" + specHash(r.Scheme, routePort(svc, r))[:8]
}
