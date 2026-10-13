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
	"slices"
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

// MaintenanceInput sets a project's maintenance page and mode; see
// store.Maintenance.
type MaintenanceInput struct {
	Enabled  bool     `json:"enabled"`
	AllowIPs []string `json:"allowIps"`
	Title    string   `json:"title"`
	Message  string   `json:"message"`
	HTML     string   `json:"html"`
	// Starting is the page of the project waking from sleep.
	Starting store.StartingPage `json:"starting"`
}

// SetMaintenance replaces a project's maintenance page and mode, for all its
// apps. Traefik picks it up within seconds; nothing restarts. Not described
// by compose files, so it works on git projects too.
func (c *Core) SetMaintenance(ctx context.Context, projectID string, in MaintenanceInput) (store.Project, error) {
	p, err := c.store.GetProject(ctx, projectID)
	if err != nil {
		return store.Project{}, err
	}
	m := store.Maintenance{
		Enabled: in.Enabled,
		Title:   strings.TrimSpace(in.Title),
		Message: strings.TrimSpace(in.Message),
		HTML:    strings.TrimSpace(in.HTML),
		Starting: store.StartingPage{
			Title:   strings.TrimSpace(in.Starting.Title),
			Message: strings.TrimSpace(in.Starting.Message),
			HTML:    strings.TrimSpace(in.Starting.HTML),
		},
	}
	for _, r := range in.AllowIPs {
		if r = strings.TrimSpace(r); r != "" {
			m.AllowIPs = append(m.AllowIPs, r)
		}
	}
	if err := validateMaintenance(m); err != nil {
		return store.Project{}, err
	}
	if m.Enabled {
		svcs, err := c.store.ListServices(ctx, projectID)
		if err != nil {
			return store.Project{}, err
		}
		if !slices.ContainsFunc(svcs, httpRouted) {
			return store.Project{}, fmt.Errorf("%w: maintenance mode needs an app with a domain and a container port", ErrInvalid)
		}
	}
	if err := c.store.SetProjectMaintenance(ctx, projectID, m); err != nil {
		return store.Project{}, err
	}
	p.Maintenance = m
	return maskedProject(p), nil
}

func validateMaintenance(m store.Maintenance) error {
	s := m.Starting
	switch {
	case utf8.RuneCountInString(s.Title) > maxPageTitle, utf8.RuneCountInString(s.Message) > maxPageMessage:
		return fmt.Errorf("%w: the starting page's title is at most %d characters, its message %d", ErrInvalid, maxPageTitle, maxPageMessage)
	case len(s.HTML) > maxPageHTML || !utf8.ValidString(s.HTML):
		return fmt.Errorf("%w: the starting page's HTML is UTF-8 text of at most %d KiB", ErrInvalid, maxPageHTML>>10)
	}
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
	// Starting: the app is waking from sleep; the page reloads itself.
	Starting bool
	Body     []byte
}

// MaintenancePage is the page of the app or the project with the given ID,
// or, when id is empty, of the app serving rawURL's host (the URL an errors
// middleware passes). Unknown apps get a page without a name. The page is
// its project's, named after it: what visitors know the app as, more than
// "web".
func (c *Core) MaintenancePage(ctx context.Context, id, rawURL string) Page {
	svc, p, ok := c.pageTarget(ctx, id, rawURL)
	if !ok {
		return renderPage(pageData{})
	}
	m := p.Maintenance
	if svc.ID != "" && c.starting(svc, m.Enabled) {
		return renderStarting(p.Name, m.Starting)
	}
	if m.HTML != "" {
		return Page{Custom: true, Body: []byte(m.HTML)}
	}
	return renderPage(pageData{Name: p.Name, Title: m.Title, Message: m.Message, Maintenance: m.Enabled})
}

// pageTarget finds whose page to serve: the app with the given ID, the
// project with it (to see its page from the settings), or, when id is empty,
// the app on rawURL's host. svc is zero for a project.
func (c *Core) pageTarget(ctx context.Context, id, rawURL string) (svc store.Service, p store.Project, ok bool) {
	switch {
	case id != "":
		var err error
		if svc, err = c.store.GetService(ctx, id); errors.Is(err, store.ErrNotFound) {
			p, err = c.store.GetProject(ctx, id)
			return store.Service{}, p, err == nil
		} else if err != nil || svc.Kind != store.ServiceKindApp {
			return store.Service{}, store.Project{}, false
		}
	default:
		u, err := url.Parse(rawURL)
		if err != nil || u.Hostname() == "" {
			return store.Service{}, store.Project{}, false
		}
		svcs, err := c.store.ListAllServices(ctx)
		if err != nil {
			c.log.Warn("maintenance page", "err", err)
			return store.Service{}, store.Project{}, false
		}
		i := slices.IndexFunc(svcs, func(s store.Service) bool {
			return s.Kind == store.ServiceKindApp && strings.EqualFold(s.Domain, u.Hostname())
		})
		if i < 0 {
			return store.Service{}, store.Project{}, false
		}
		svc = svcs[i]
	}
	p, err := c.store.GetProject(ctx, svc.ProjectID)
	return svc, p, err == nil
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
    <title>{{.Heading}}</title>{{if .Starting}}
    <meta http-equiv="refresh" content="3">{{end}}
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
      .spinner {
        width: 28px;
        height: 28px;
        margin: 0 auto 20px;
        border: 3px solid color-mix(in srgb, var(--muted) 30%, transparent);
        border-top-color: var(--fg);
        border-radius: 50%;
        animation: spin 0.9s linear infinite;
      }
      @keyframes spin {
        to {
          transform: rotate(360deg);
        }
      }
      @media (prefers-reduced-motion: reduce) {
        .spinner {
          animation-duration: 3s;
        }
      }
    </style>
  </head>
  <body>
    <main>{{if .Starting}}
      <div class="spinner" role="status" aria-label="Loading"></div>{{end}}
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
	_ = pageTmpl.Execute(&b, map[string]any{"Heading": heading, "Text": text})
	return Page{Body: b.Bytes()}
}

// StartingPage is the page of the app or project with the given ID while it
// wakes from sleep, without waking it (to see it from the settings).
func (c *Core) StartingPage(ctx context.Context, id string) Page {
	_, p, ok := c.pageTarget(ctx, id, "")
	if !ok {
		return renderStarting("", store.StartingPage{})
	}
	return renderStarting(p.Name, p.Maintenance.Starting)
}

// renderStarting is the page of an app waking from sleep: it reloads every
// few seconds (a Refresh header, so custom HTML does too; no script: pages
// are sandboxed) until the app's own router answers instead. After
// coldStart the usual page replaces it.
func renderStarting(name string, s store.StartingPage) Page {
	if s.HTML != "" {
		return Page{Custom: true, Starting: true, Body: []byte(s.HTML)}
	}
	heading, text := s.Title, s.Message
	if heading == "" {
		heading = "Starting…"
	}
	if text == "" && name != "" {
		text = name + " is starting. This page reloads by itself in a few seconds."
	} else if text == "" {
		text = "This site is starting. This page reloads by itself in a few seconds."
	}
	var b bytes.Buffer
	_ = pageTmpl.Execute(&b, map[string]any{"Heading": heading, "Text": text, "Starting": true})
	return Page{Starting: true, Body: b.Bytes()}
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
			// Not the app's cookies or credentials. Accept tells a page
			// from an API call (an app waking from sleep).
			"errorRequestHeaders": []string{"Accept", "Accept-Language"},
		}}},
		"services": map[string]any{pagesService: map[string]any{"loadBalancer": map[string]any{
			"servers":        []map[string]string{{"url": upstream}},
			"passHostHeader": false,
		}}},
	}
}

// TraefikConfig is the dynamic configuration a server's Traefik polls (HTTP
// provider): for each app it routes, a fallback router serving the page when
// no replica carries the app's router (stopped, crashed, never ready), and
// while its project is in maintenance mode a router above the app's sending
// visitors to the page, and the allowed IPs to the app.
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
	projects, err := c.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	maintenance := map[string]store.Maintenance{}
	for _, p := range projects {
		maintenance[p.ID] = p.Maintenance
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
			routers[name+"-maintenance"] = router(maintenancePriority, pagesService+"@file", page)
			continue
		}
		allow := map[string]any{"sourceRange": m.AllowIPs, "rejectStatusCode": maintenanceReject}
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
