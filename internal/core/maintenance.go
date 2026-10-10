package core

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/moby/moby/api/types/container"

	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	maxPageTitle   = 200
	maxPageMessage = 2000
	maxPageHTML    = 64 << 10

	// pagesService is the Traefik service (file provider) reaching the
	// manager's pages; pagesDown the errors middleware every router of
	// websecure goes through.
	pagesService = "kipitiny-pages"
	pagesDown    = "kipitiny-down"
	// PagesPath is where the manager serves maintenance pages and the
	// Traefik configuration that routes to them.
	PagesPath = "/api/pages/"

	// maintenanceReject is the status the maintenance IP allowlist refuses
	// with, turned into the page by an errors middleware. Apps don't send
	// it, unlike 403.
	maintenanceReject = 418
	// Above any router a label defines (their default priority is the
	// rule's length).
	maintenancePriority = 1 << 30

	providerKeySetting = "traefik_provider_key"
)

// MaintenanceInput sets an app's maintenance page and mode; see
// store.Maintenance.
type MaintenanceInput struct {
	Enabled  bool     `json:"enabled"`
	AllowIPs []string `json:"allowIps"`
	Title    string   `json:"title"`
	Message  string   `json:"message"`
	HTML     string   `json:"html"`
}

// SetMaintenance replaces an app's maintenance page and mode. Traefik picks
// it up within seconds; nothing restarts. Not described by compose files, so
// it works on git projects too.
func (c *Core) SetMaintenance(ctx context.Context, serviceID string, in MaintenanceInput) (ServiceView, error) {
	svc, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return ServiceView{}, err
	}
	if svc.Kind != store.ServiceKindApp {
		return ServiceView{}, fmt.Errorf("%w: only apps have a maintenance page", ErrInvalid)
	}
	m := store.Maintenance{
		Enabled: in.Enabled,
		Title:   strings.TrimSpace(in.Title),
		Message: strings.TrimSpace(in.Message),
		HTML:    strings.TrimSpace(in.HTML),
	}
	for _, r := range in.AllowIPs {
		if r = strings.TrimSpace(r); r != "" {
			m.AllowIPs = append(m.AllowIPs, r)
		}
	}
	if err := validateMaintenance(m); err != nil {
		return ServiceView{}, err
	}
	if m.Enabled && !httpRouted(svc) {
		return ServiceView{}, fmt.Errorf("%w: maintenance mode needs a domain and a container port", ErrInvalid)
	}
	if err := c.store.SetServiceMaintenance(ctx, svc.ID, m); err != nil {
		return ServiceView{}, err
	}
	svc.Maintenance = m
	return c.view(ctx, svc)
}

func validateMaintenance(m store.Maintenance) error {
	switch {
	case utf8.RuneCountInString(m.Title) > maxPageTitle:
		return fmt.Errorf("%w: the title is at most %d characters", ErrInvalid, maxPageTitle)
	case utf8.RuneCountInString(m.Message) > maxPageMessage:
		return fmt.Errorf("%w: the message is at most %d characters", ErrInvalid, maxPageMessage)
	case len(m.HTML) > maxPageHTML:
		return fmt.Errorf("%w: the HTML page is at most %d KiB", ErrInvalid, maxPageHTML>>10)
	case !utf8.ValidString(m.HTML):
		return fmt.Errorf("%w: the HTML page must be UTF-8 text", ErrInvalid)
	}
	// The same rules as the IP allowlist middleware.
	return validateMiddlewares(store.Middlewares{IPAllowList: m.AllowIPs})
}

// Page is a maintenance page as served to visitors.
type Page struct {
	// Custom: HTML the user wrote, which may need scripts and other origins.
	Custom bool
	Body   []byte
}

// MaintenancePage is the page of the app with the given ID, or, when id is
// empty, of the app serving rawURL's host (the URL an errors middleware
// passes). Unknown apps get a page without a name.
func (c *Core) MaintenancePage(ctx context.Context, id, rawURL string) Page {
	svc, ok := c.pageService(ctx, id, rawURL)
	if !ok {
		return renderPage(pageData{})
	}
	m := svc.Maintenance
	if m.HTML != "" {
		return Page{Custom: true, Body: []byte(m.HTML)}
	}
	d := pageData{Name: svc.Name, Title: m.Title, Message: m.Message, Maintenance: m.Enabled}
	if p, err := c.store.GetProject(ctx, svc.ProjectID); err == nil {
		d.Name = p.Name // what visitors know the app as, more than "web"
	}
	return renderPage(d)
}

func (c *Core) pageService(ctx context.Context, id, rawURL string) (store.Service, bool) {
	if id != "" {
		svc, err := c.store.GetService(ctx, id)
		return svc, err == nil && svc.Kind == store.ServiceKindApp
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return store.Service{}, false
	}
	svcs, err := c.store.ListAllServices(ctx)
	if err != nil {
		c.log.Warn("maintenance page", "err", err)
		return store.Service{}, false
	}
	for _, s := range svcs {
		if s.Kind == store.ServiceKindApp && strings.EqualFold(s.Domain, u.Hostname()) {
			return s, true
		}
	}
	return store.Service{}, false
}

type pageData struct {
	Name, Title, Message string
	Maintenance          bool
}

var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <meta name="robots" content="noindex">
    <title>{{.Heading}}</title>
    <style>
      :root {
        color-scheme: light dark;
        --bg: #fafafa;
        --fg: #18181b;
        --muted: #71717a;
      }
      @media (prefers-color-scheme: dark) {
        :root {
          --bg: #09090b;
          --fg: #fafafa;
          --muted: #a1a1aa;
        }
      }
      * {
        box-sizing: border-box;
      }
      body {
        margin: 0;
        min-height: 100vh;
        display: grid;
        place-items: center;
        padding: 24px 16px;
        background: var(--bg);
        color: var(--fg);
        font: 16px/1.6 system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
      }
      main {
        max-width: 32rem;
        text-align: center;
      }
      h1 {
        margin: 0 0 12px;
        font-size: clamp(1.5rem, 5vw, 2rem);
        line-height: 1.2;
        font-weight: 600;
        letter-spacing: -0.01em;
      }
      p {
        margin: 0;
        color: var(--muted);
        white-space: pre-line;
      }
    </style>
  </head>
  <body>
    <main>
      <h1>{{.Heading}}</h1>
      <p>{{.Text}}</p>
    </main>
  </body>
</html>
`))

func renderPage(d pageData) Page {
	heading := d.Title
	if heading == "" {
		heading = "We'll be back soon"
	}
	text := d.Message
	switch {
	case text != "":
	case d.Name != "" && d.Maintenance:
		text = d.Name + " is down for maintenance. Please check back in a few minutes."
	case d.Name != "":
		text = d.Name + " is unavailable right now. Please try again in a few minutes."
	default:
		text = "This site is unavailable right now. Please try again in a few minutes."
	}
	var b bytes.Buffer
	_ = pageTmpl.Execute(&b, map[string]string{"Heading": heading, "Text": text})
	return Page{Body: b.Bytes()}
}

// pagesUpstream is the URL a server's Traefik reaches the manager's pages
// at: over the Docker network on the manager's server, through the manager's
// public domain elsewhere ("" when it has none: no pages there). managerURL
// is the manager's upstream when already known.
func (c *Core) pagesUpstream(ctx context.Context, sv store.Server, managerURL string) (string, error) {
	if sv.Kind == store.ServerLocal {
		if managerURL != "" {
			return managerURL, nil
		}
		return c.managerUpstream(ctx, c.dockerFor(sv.ID))
	}
	if c.cfg.Domain == "" || isLocalDomain(c.cfg.Domain) {
		return "", nil
	}
	return "https://" + c.cfg.Domain, nil
}

// providerToken authenticates a server's Traefik when it fetches its
// configuration (TraefikConfig). Stable, since it is in Traefik's command.
func (c *Core) providerToken(ctx context.Context, serverID string) (string, error) {
	key, err := c.store.GetSetting(ctx, providerKeySetting)
	if errors.Is(err, store.ErrNotFound) {
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

// pagesFileConfig is the part of the pages that never changes for a server,
// in Traefik's file provider: if it came from the HTTP provider, a manager
// that is down would take every router using kipitiny-down with it.
func pagesFileConfig(upstream string) map[string]any {
	return map[string]any{
		"middlewares": map[string]any{pagesDown: map[string]any{"errors": map[string]any{
			// Traefik's own failures to reach a replica. Apps' own 503s
			// are theirs; an app without a ready replica has no router,
			// which the fallback router of TraefikConfig catches.
			"status":         []string{"502", "504"},
			"statusRewrites": map[string]int{"502-504": 503},
			"service":        pagesService,
			"query":          PagesPath + "down?url={url}",
			// Not the app's cookies or credentials.
			"errorRequestHeaders": []string{"Accept-Language"},
		}}},
		"services": map[string]any{pagesService: map[string]any{"loadBalancer": map[string]any{
			"servers":        []map[string]string{{"url": upstream}},
			"passHostHeader": false,
		}}},
	}
}

// TraefikConfig is the dynamic configuration a server's Traefik polls (HTTP
// provider): for each app it routes, a fallback router serving the page when
// no replica carries the app's router (stopped, crashed, never ready), and in
// maintenance mode a router above the app's sending visitors to the page,
// and the allowed IPs to the app.
func (c *Core) TraefikConfig(ctx context.Context, serverID, token string) ([]byte, error) {
	want, err := c.providerToken(ctx, serverID)
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(want)) != 1 {
		return nil, ErrUnauthorized
	}
	svcs, err := c.store.ListAllServices(ctx)
	if err != nil {
		return nil, err
	}
	routers, middlewares := map[string]any{}, map[string]any{}
	for _, svc := range svcs {
		if svc.Kind != store.ServiceKindApp || svc.ServerID != serverID || !httpRouted(svc) {
			continue
		}
		name := "kipitiny-" + strings.ToLower(svc.ID)
		rt := c.routeFor(ctx, svc)
		tls := map[string]any{}
		if rt.resolver != "" {
			tls["certResolver"] = rt.resolver
		}
		page := name + "-page"
		middlewares[page] = map[string]any{"replacePath": map[string]string{"path": PagesPath + svc.ID}}
		router := func(priority int, service string, chain ...string) map[string]any {
			return map[string]any{
				"rule": "Host(`" + svc.Domain + "`)", "entryPoints": []string{"websecure"}, "tls": tls,
				"priority": priority, "service": service, "middlewares": chain,
			}
		}
		routers[page] = router(1, pagesService+"@file", page)
		if !svc.Maintenance.Enabled {
			continue
		}
		var appChain []string
		if len(svc.Maintenance.AllowIPs) > 0 {
			if appChain, err = c.routerChain(ctx, svc, name); err != nil {
				c.log.Warn("maintenance allowlist: read the app's router", "service", svc.ID, "err", err)
			}
		}
		if appChain == nil {
			// Nobody gets through: no allowlist, or no replica to reach.
			routers[name+"-maintenance"] = router(maintenancePriority, pagesService+"@file", page)
			continue
		}
		allow := map[string]any{"sourceRange": svc.Maintenance.AllowIPs, "rejectStatusCode": maintenanceReject}
		if rt.behindProxy {
			allow["ipStrategy"] = map[string]int{"depth": 1}
		}
		middlewares[name+"-maintenance-allow"] = map[string]any{"ipAllowList": allow}
		middlewares[name+"-maintenance-page"] = map[string]any{"errors": map[string]any{
			"status":              []string{fmt.Sprint(maintenanceReject)},
			"statusRewrites":      map[string]int{fmt.Sprint(maintenanceReject): 503},
			"service":             pagesService + "@file",
			"query":               PagesPath + svc.ID,
			"errorRequestHeaders": []string{"Accept-Language"},
		}}
		chain := append([]string{name + "-maintenance-page", name + "-maintenance-allow"}, appChain...)
		routers[name+"-maintenance"] = router(maintenancePriority, name+"@docker", chain...)
	}
	return json.Marshal(map[string]any{"http": map[string]any{"routers": routers, "middlewares": middlewares}})
}

// routerChain is the middleware chain of the router a running replica of
// svc defines (named name-<hash>), so allowed IPs in maintenance mode still
// go through the app's basic auth, allowlist and headers. nil when no
// replica runs.
func (c *Core) routerChain(ctx context.Context, svc store.Service, name string) ([]string, error) {
	cts, err := c.serviceContainers(ctx, svc)
	if err != nil {
		return nil, err
	}
	prefix := "traefik.http.routers." + name + "-"
	for _, ct := range cts {
		if ct.State != container.StateRunning {
			continue
		}
		for k, v := range ct.Labels {
			if strings.HasPrefix(k, prefix) && strings.HasSuffix(k, ".middlewares") {
				return strings.Split(v, ","), nil
			}
		}
	}
	return nil, nil
}
