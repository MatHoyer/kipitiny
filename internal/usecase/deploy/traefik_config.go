package deploy

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
)

// PagesUpstream is the URL a server's Traefik reaches the manager's pages
// at: over the Docker network on the manager's server, through the manager's
// public domain elsewhere ("" when it has none: no pages there). managerURL
// is the manager's upstream when already known.
func (c *Deployer) PagesUpstream(ctx context.Context, sv domain.Server, managerURL string) (string, error) {
	if sv.Kind == domain.ServerLocal {
		if managerURL != "" {
			return managerURL, nil
		}
		return c.mgr.ManagerUpstream(ctx, c.mgr.DockerFor(sv.ID))
	}
	if c.cfg.Domain == "" || network.IsLocalDomain(c.cfg.Domain) {
		return "", nil
	}
	return "https://" + c.cfg.Domain, nil
}

// ProviderToken authenticates a server's Traefik when it fetches its
// configuration (TraefikConfig). Stable, since it is in Traefik's command.
func (c *Deployer) ProviderToken(ctx context.Context, serverID string) (string, error) {
	key, err := c.store.GetSetting(ctx, providerKeySetting)
	if errors.Is(err, domain.ErrNotFound) {
		b := make([]byte, 32)
		_, _ = rand.Read(b)
		key = hex.EncodeToString(b)
		err = c.store.SetSetting(ctx, providerKeySetting, key)
	}
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte("traefik-provider:" + serverID))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// TraefikConfig is the dynamic configuration a server's Traefik polls (HTTP
// provider): for each app it routes, a fallback router serving the page when
// no replica carries the app's router (stopped, crashed, never ready), and
// while its project is in maintenance mode a router above the app's sending
// visitors to the page, and the allowed IPs to the app.
func (c *Deployer) TraefikConfig(ctx context.Context, serverID, token string) ([]byte, error) {
	want, err := c.ProviderToken(ctx, serverID)
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(want)) != 1 {
		return nil, identity.ErrUnauthorized
	}
	svcs, err := c.store.ListAllServices(ctx)
	if err != nil {
		return nil, err
	}
	projects, err := c.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	maintenance := map[string]domain.Maintenance{}
	for _, p := range projects {
		maintenance[p.ID] = p.Maintenance
	}
	routers, middlewares := map[string]any{}, map[string]any{}
	for _, svc := range svcs {
		if svc.Kind != domain.ServiceKindApp || svc.ServerID != serverID || !network.HTTPRouted(svc) {
			continue
		}
		name := "kipitiny-" + strings.ToLower(svc.ID)
		rt := c.mgr.RouteFor(ctx, svc)
		tls := map[string]any{}
		if rt.Resolver != "" {
			tls["certResolver"] = rt.Resolver
		}
		page := name + "-page"
		middlewares[page] = map[string]any{"replacePath": map[string]string{"path": network.PagesPath + svc.ID}}
		router := func(rule string, priority int, service string, chain ...string) map[string]any {
			return map[string]any{
				"rule": rule, "entryPoints": []string{"websecure"}, "tls": tls,
				"priority": priority, "service": service, "middlewares": chain,
			}
		}
		// paths are the rules svc's routers match, with their service
		// and scheme; a service without routes has the whole domain.
		type pathRoute struct {
			rule, service string
			priority      int
			h2c           bool
		}
		host := "Host(`" + svc.Domain + "`)"
		paths := []pathRoute{{rule: host, service: name}}
		if len(svc.Routes) > 0 {
			paths = paths[:0]
			for _, r := range svc.Routes {
				for _, p := range r.Paths {
					paths = append(paths, pathRoute{network.PathRule(svc.Domain, p), network.RouteService(name, svc, r), len(p), r.Scheme == "h2c"})
				}
			}
		}
		suffix := func(i int) string {
			if len(svc.Routes) == 0 {
				return ""
			}
			return fmt.Sprintf("-p%d", i)
		}
		for i, p := range paths {
			if len(svc.Routes) == 0 {
				routers[page] = router(p.rule, 1, network.PagesService+"@file", page)
			} else {
				routers[page+suffix(i)] = router(p.rule, network.RoutePriority+2*p.priority, network.PagesService+"@file", page)
			}
		}
		m := maintenance[svc.ProjectID]
		if !m.Enabled {
			continue
		}
		var appChain []string
		if len(m.AllowIPs) > 0 {
			if appChain, err = c.routerChain(ctx, svc, name); err != nil {
				c.log.Warn("maintenance allowlist: read the app's router", "service", svc.ID, "err", err)
			}
		}
		if appChain == nil {
			// Nobody gets through: no allowlist, or no replica to reach.
			for i, p := range paths {
				routers[name+"-maintenance"+suffix(i)] = router(p.rule, maintenancePriority+p.priority, network.PagesService+"@file", page)
			}
			continue
		}
		allow := map[string]any{"sourceRange": m.AllowIPs, "rejectStatusCode": maintenanceReject}
		if rt.BehindProxy {
			allow["ipStrategy"] = map[string]int{"depth": 1}
		}
		middlewares[name+"-maintenance-allow"] = map[string]any{"ipAllowList": allow}
		middlewares[name+"-maintenance-page"] = map[string]any{"errors": map[string]any{
			"status":              []string{fmt.Sprint(maintenanceReject)},
			"statusRewrites":      map[string]int{fmt.Sprint(maintenanceReject): 503},
			"service":             network.PagesService + "@file",
			"query":               network.PagesPath + svc.ID,
			"errorRequestHeaders": []string{"Accept-Language"},
		}}
		// The app's chain without its retry, which h2c routes go without.
		retry := name + "-retry@docker"
		appChain = slices.DeleteFunc(appChain, func(m string) bool { return m == retry })
		for i, p := range paths {
			chain := append([]string{name + "-maintenance-page", name + "-maintenance-allow"}, appChain...)
			if !p.h2c {
				chain = append(chain, retry)
			}
			routers[name+"-maintenance"+suffix(i)] = router(p.rule, maintenancePriority+p.priority, p.service+"@docker", chain...)
		}
	}
	return json.Marshal(map[string]any{"http": map[string]any{"routers": routers, "middlewares": middlewares}})
}

// routerChain is the middleware chain of the router a running replica of
// svc defines (named name-<hash>), so allowed IPs in maintenance mode still
// go through the app's basic auth, allowlist and headers. nil when no
// replica runs.
func (c *Deployer) routerChain(ctx context.Context, svc domain.Service, name string) ([]string, error) {
	cts, err := c.ServiceContainers(ctx, svc)
	if err != nil {
		return nil, err
	}
	prefix := "traefik.http.routers." + name + "-"
	for _, ct := range cts {
		if ct.State != domain.StateRunning {
			continue
		}
		for k := range ct.Labels {
			if r, ok := strings.CutSuffix(k, ".rule"); ok && strings.HasPrefix(k, prefix) {
				// An h2c route's router may have no middlewares.
				chain := []string{}
				if v := ct.Labels[r+".middlewares"]; v != "" {
					chain = strings.Split(v, ",")
				}
				return chain, nil
			}
		}
	}
	return nil, nil
}
