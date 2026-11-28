package main

// API end to end: the real manager (wire) over HTTP, on a fresh SQLite in a
// temp data dir, deploying on a real Docker daemon. Nothing is faked: the
// journeys need no email and no outside service. They are the safety net for
// refactors: a change that keeps behavior keeps them green without editing
// them.
//
//	KIPITINY_TEST_DOCKER=1 DOCKER_HOST=tcp://127.0.0.1:2375 go test ./cmd/kipitiny/ -run API
//
// Each manager's reconciler removes the kipitiny containers it doesn't know:
// they refuse a daemon that runs any, and `make cover` runs them after the
// other packages' Docker tests, never beside them.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/docker"
	"github.com/kipit-labs/kipitiny/internal/usecase/servers"
)

const (
	setupToken = "api-setup-token"
	password   = "correct horse battery"
	// The test binary can't be the health probe injected into apps (it isn't
	// static under -race): the app opts out, ready once up for 5 s.
	whoami = `{"name": "web", "kind": "app", "image": "traefik/whoami:v1.10", "port": 80, "replicas": 1,
		"healthcheck": {"test": ["NONE"]}}`
)

type testApp struct {
	url string
	dk  *docker.Client
}

// newTestApp starts a manager as `kipitiny` does, configured like the UI
// tests' (Traefik and the update check off), with no user yet.
func newTestApp(t *testing.T) *testApp {
	t.Helper()
	if os.Getenv("KIPITINY_TEST_DOCKER") == "" {
		t.Skip("KIPITINY_TEST_DOCKER not set")
	}
	dk, err := docker.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dk.Close() })
	if cts, err := dk.ListContainers(t.Context(), map[string]string{domain.LabelManaged: "true"}); err != nil {
		t.Fatal(err)
	} else if len(cts) > 0 {
		msg := "this Docker daemon runs kipitiny containers, which this manager's reconciler would remove: point DOCKER_HOST at a throwaway daemon"
		if os.Getenv("CI") != "" {
			t.Fatal(msg)
		}
		t.Skip(msg)
	}

	for k, v := range map[string]string{
		"KIPITINY_DATA_DIR":     t.TempDir(),
		"KIPITINY_SETUP_TOKEN":  setupToken,
		"KIPITINY_TRAEFIK":      "false",
		"KIPITINY_UPDATE_CHECK": "off",
	} {
		t.Setenv(k, v)
	}
	cfg := config.Load()
	cfg.Version = version

	// The manager's log, shown when the test fails.
	logs := &lockedBuffer{}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("manager log:\n%s", logs.String())
		}
	})
	ctx, stop := context.WithCancel(context.Background())
	handler, shutdown, err := wire(ctx, cfg, slog.New(slog.NewTextHandler(logs, nil)))
	if err != nil {
		stop()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop()
		drain, cancel := context.WithTimeout(context.Background(), servers.DrainTimeout)
		defer cancel()
		if err := shutdown(drain); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &testApp{url: srv.URL, dk: dk}
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// client is a browser with its own session cookie, or CI with a token.
type client struct {
	t     *testing.T
	app   *testApp
	http  *http.Client
	token string
}

func (a *testApp) visitor(t *testing.T) *client {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &client{t: t, app: a, http: &http.Client{Jar: jar}}
}

func (a *testApp) withToken(t *testing.T, token string) *client {
	return &client{t: t, app: a, http: &http.Client{}, token: token}
}

// do sends body (JSON: a string is sent as is) and decodes the answer into
// out (when not nil); another status than want fails the test.
func (c *client) do(method, path string, body, out any, want int) {
	c.t.Helper()
	var r io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		r = bytes.NewReader([]byte(b))
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			c.t.Fatal(err)
		}
		r = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, c.app.url+path, r)
	if err != nil {
		c.t.Fatal(err)
	}
	if r != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	} else {
		req.Header.Set("Origin", c.app.url) // as the web app's requests
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		c.t.Fatal(err)
	}
	if res.StatusCode != want {
		c.t.Fatalf("%s %s: status %d, want %d: %s", method, path, res.StatusCode, want, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatalf("%s %s: %v: %s", method, path, err, raw)
		}
	}
}

// eventually polls check until it returns true, failing after timeout.
func eventually(t *testing.T, what string, timeout time.Duration, check func() bool) {
	t.Helper()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	deadline := time.After(timeout)
	for !check() {
		select {
		case <-deadline:
			t.Fatalf("%s: still not after %s", what, timeout)
		case <-tick.C:
		}
	}
}

// admin sets the manager up with its first admin and is signed in as them.
func (a *testApp) admin(t *testing.T) *client {
	t.Helper()
	c := a.visitor(t)
	c.do(http.MethodPost, "/api/auth/setup", map[string]string{"setupToken": setupToken, "username": "admin", "password": password}, nil, http.StatusCreated)
	return c
}

type project struct{ ID, Name string }

// project creates a project, deleted with its containers after the test.
func (c *client) project(name string) project {
	c.t.Helper()
	var p project
	c.do(http.MethodPost, "/api/projects", map[string]string{"name": name}, &p, http.StatusCreated)
	c.t.Cleanup(func() {
		var now project // by its name at the end
		c.do(http.MethodGet, "/api/projects/"+p.ID, nil, &now, http.StatusOK)
		c.do(http.MethodDelete, "/api/projects/"+p.ID+"?confirm="+url.QueryEscape(now.Name), nil, nil, http.StatusNoContent)
	})
	return p
}

type service struct {
	ID         string
	Name       string
	Containers []struct {
		State   string
		Retired bool
	}
}

// running reports whether every current replica of the service runs.
func (s service) running() bool {
	n := 0
	for _, ct := range s.Containers {
		if !ct.Retired {
			if ct.State != "running" {
				return false
			}
			n++
		}
	}
	return n > 0
}

// deployed creates a service as the web app's "Create & deploy" does and
// waits for its deployment to succeed.
func (c *client) deployed(projectID string, input string) service {
	c.t.Helper()
	var svc service
	c.do(http.MethodPost, "/api/projects/"+projectID+"/services", input, &svc, http.StatusCreated)
	var d struct{ ID, Status, Error string }
	c.do(http.MethodPost, "/api/services/"+svc.ID+"/deploy", nil, &d, http.StatusAccepted)
	eventually(c.t, "deployment of "+svc.Name, 5*time.Minute, func() bool {
		c.do(http.MethodGet, "/api/deployments/"+d.ID, nil, &d, http.StatusOK)
		return d.Status != string(domain.DeploymentRunning)
	})
	if d.Status != string(domain.DeploymentSucceeded) {
		c.t.Fatalf("deployment of %s: %s: %s", svc.Name, d.Status, d.Error)
	}
	c.do(http.MethodGet, "/api/services/"+svc.ID, nil, &svc, http.StatusOK)
	return svc
}

func TestAPISetupAndSignIn(t *testing.T) {
	a := newTestApp(t)
	first := a.visitor(t)
	var state struct{ SetupRequired bool }
	first.do(http.MethodGet, "/api/auth/state", nil, &state, http.StatusOK)
	if !state.SetupRequired {
		t.Fatal("a new manager doesn't ask for its setup")
	}
	first.do(http.MethodPost, "/api/auth/setup", map[string]string{"setupToken": "wrong", "username": "admin", "password": password}, nil, http.StatusUnauthorized)
	first.do(http.MethodGet, "/api/account", nil, nil, http.StatusUnauthorized)
	first.do(http.MethodPost, "/api/auth/setup", map[string]string{"setupToken": setupToken, "username": "admin", "password": password}, nil, http.StatusCreated)
	first.do(http.MethodGet, "/api/account", nil, nil, http.StatusOK)

	// Set up once: the token no longer makes an admin.
	other := a.visitor(t)
	other.do(http.MethodPost, "/api/auth/setup", map[string]string{"setupToken": setupToken, "username": "mallory", "password": password}, nil, http.StatusBadRequest)
	other.do(http.MethodGet, "/api/auth/state", nil, &state, http.StatusOK)
	if state.SetupRequired {
		t.Error("still asks for its setup")
	}

	other.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "admin", "password": "not the password"}, nil, http.StatusUnauthorized)
	other.do(http.MethodGet, "/api/account", nil, nil, http.StatusUnauthorized)
	var me struct{ Username, Role string }
	other.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "admin", "password": password}, &me, http.StatusOK)
	if me.Username != "admin" || me.Role != string(domain.RoleAdmin) {
		t.Errorf("signed in as %+v", me)
	}
	other.do(http.MethodPost, "/api/auth/logout", nil, nil, http.StatusNoContent)
	other.do(http.MethodGet, "/api/account", nil, nil, http.StatusUnauthorized)
	first.do(http.MethodGet, "/api/account", nil, nil, http.StatusOK) // another browser's session stays
}

func TestAPIDeployImageApp(t *testing.T) {
	a := newTestApp(t)
	admin := a.admin(t)
	p := admin.project("shop")
	web := admin.deployed(p.ID, whoami)
	if !web.running() {
		t.Fatalf("deployed, but not running: %+v", web.Containers)
	}
	cts, err := a.dk.ListContainers(t.Context(), map[string]string{domain.LabelService: web.ID})
	if err != nil {
		t.Fatal(err)
	}
	// The image as deployed: pinned to the digest pulled.
	if len(cts) != 1 || !strings.HasPrefix(cts[0].Image, "traefik/whoami:v1.10@sha256:") || cts[0].State != "running" {
		t.Errorf("containers on the daemon: %+v", cts)
	}
}

// sql runs a query in the database's console, as the Data tab does.
func (c *client) sql(serviceID, query string, write bool) [][]*string {
	c.t.Helper()
	var res struct{ Rows [][]*string }
	c.do(http.MethodPost, "/api/services/"+serviceID+"/data/console", map[string]any{"query": query, "write": write}, &res, http.StatusOK)
	return res.Rows
}

func cell(rows [][]*string) string {
	if len(rows) != 1 || len(rows[0]) != 1 || rows[0][0] == nil {
		return "<not one cell>"
	}
	return *rows[0][0]
}

func TestAPIDatabaseBackupRestore(t *testing.T) {
	a := newTestApp(t)
	admin := a.admin(t)
	p := admin.project("shop")
	db := admin.deployed(p.ID, `{"name": "db", "kind": "postgres"}`)

	admin.sql(db.ID, "create table orders (item text); insert into orders values ('boots');", true)
	var b struct{ ID, Status, Error string }
	admin.do(http.MethodPost, "/api/services/"+db.ID+"/backups", map[string]string{}, &b, http.StatusAccepted)
	eventually(t, "backup", 2*time.Minute, func() bool {
		admin.do(http.MethodGet, "/api/backups/"+b.ID, nil, &b, http.StatusOK)
		return b.Status != string(domain.OpRunning)
	})
	if b.Status != string(domain.OpSucceeded) {
		t.Fatalf("backup %s: %s", b.Status, b.Error)
	}

	admin.sql(db.ID, "delete from orders;", true)
	if n := cell(admin.sql(db.ID, "select count(*) from orders;", false)); n != "0" {
		t.Fatalf("%s orders left after the delete", n)
	}
	// The console refuses writes unless asked for.
	admin.do(http.MethodPost, "/api/services/"+db.ID+"/data/console", map[string]any{"query": "drop table orders;"}, nil, http.StatusBadRequest)

	admin.do(http.MethodPost, "/api/backups/"+b.ID+"/restore", map[string]string{"confirm": "wrong"}, nil, http.StatusBadRequest)
	var r struct{ ID, Status, Error string }
	admin.do(http.MethodPost, "/api/backups/"+b.ID+"/restore", map[string]string{"confirm": "db"}, &r, http.StatusAccepted)
	eventually(t, "restore", 2*time.Minute, func() bool {
		var rs []struct{ ID, Status, Error string }
		admin.do(http.MethodGet, "/api/services/"+db.ID+"/restores", nil, &rs, http.StatusOK)
		i := slices.IndexFunc(rs, func(x struct{ ID, Status, Error string }) bool { return x.ID == r.ID })
		if i >= 0 {
			r = rs[i]
		}
		return r.Status != string(domain.OpRunning)
	})
	if r.Status != string(domain.OpSucceeded) {
		t.Fatalf("restore %s: %s", r.Status, r.Error)
	}
	if item := cell(admin.sql(db.ID, "select item from orders;", false)); item != "boots" {
		t.Errorf("after the restore, orders hold %q", item)
	}
}

// tokenFor creates an API token restricted to perms on one project.
func (c *client) tokenFor(projectID string, perms ...string) (id, token string) {
	c.t.Helper()
	var tok struct{ ID, Token string }
	c.do(http.MethodPost, "/api/tokens", map[string]any{
		"name": "ci", "restricted": true, "grants": map[string][]string{projectID: perms},
	}, &tok, http.StatusCreated)
	return tok.ID, tok.Token
}

func TestAPIRestrictedToken(t *testing.T) {
	a := newTestApp(t)
	admin := a.admin(t)
	other := admin.project("other")
	shop := admin.project("shop")
	id, token := admin.tokenFor(shop.ID, "view")
	ci := a.withToken(t, token)

	// It sees its one project and may view it, nothing more.
	var projects []project
	ci.do(http.MethodGet, "/api/projects", nil, &projects, http.StatusOK)
	if len(projects) != 1 || projects[0].ID != shop.ID {
		t.Errorf("the token sees %+v", projects)
	}
	ci.do(http.MethodGet, "/api/projects/"+shop.ID, nil, nil, http.StatusOK)
	ci.do(http.MethodPut, "/api/projects/"+shop.ID+"/name", map[string]string{"name": "shop-renamed"}, nil, http.StatusForbidden)
	ci.do(http.MethodGet, "/api/projects/"+other.ID, nil, nil, http.StatusNotFound) // not even shown to exist
	ci.do(http.MethodPost, "/api/projects", map[string]string{"name": "mine"}, nil, http.StatusForbidden)
	ci.do(http.MethodGet, "/api/users", nil, nil, http.StatusForbidden)

	// Given settings too, the rename passes.
	admin.do(http.MethodPut, "/api/tokens/"+id, map[string]any{
		"name": "ci", "restricted": true, "grants": map[string][]string{shop.ID: {"view", "settings"}},
	}, nil, http.StatusOK)
	ci.do(http.MethodPut, "/api/projects/"+shop.ID+"/name", map[string]string{"name": "shop-renamed"}, nil, http.StatusOK)
	admin.do(http.MethodPut, "/api/projects/"+shop.ID+"/name", map[string]string{"name": "shop"}, nil, http.StatusOK)

	admin.do(http.MethodDelete, "/api/tokens/"+id, nil, nil, http.StatusNoContent)
	ci.do(http.MethodGet, "/api/projects/"+shop.ID, nil, nil, http.StatusUnauthorized)
}

// ci runs `kipitiny preview` as a CI job does, failing the test on error.
func ci(t *testing.T, args ...string) {
	t.Helper()
	saved := os.Args
	t.Cleanup(func() { os.Args = saved })
	os.Args = append([]string{"kipitiny", "preview"}, args...)
	if err := previewCmd(); err != nil {
		t.Fatalf("kipitiny preview %v: %v", args, err)
	}
}

func TestAPIPreviewFromCI(t *testing.T) {
	a := newTestApp(t)
	admin := a.admin(t)
	p := admin.project("shop")
	admin.deployed(p.ID, whoami)
	admin.do(http.MethodPut, "/api/projects/"+p.ID+"/preview-settings", map[string]string{"baseDomain": "shop.localhost"}, nil, http.StatusOK)
	_, token := admin.tokenFor(p.ID, "view", "deploy")

	// CI opens pr-1 and waits for its services.
	ci(t, "--url", a.url, "--token", token, "--project", "shop", "--name", "pr-1", "--branch", "feat/checkout", "--commit", "abc1234")
	var previews []struct {
		ID      string
		Preview struct{ Name, Branch string }
	}
	admin.do(http.MethodGet, "/api/projects/"+p.ID+"/previews", nil, &previews, http.StatusOK)
	if len(previews) != 1 || previews[0].Preview.Name != "pr-1" || previews[0].Preview.Branch != "feat/checkout" {
		t.Fatalf("previews: %+v", previews)
	}
	pr := previews[0].ID
	var svcs []service
	admin.do(http.MethodGet, "/api/projects/"+pr+"/services", nil, &svcs, http.StatusOK)
	if len(svcs) != 1 || svcs[0].Name != "web" {
		t.Fatalf("the preview's services: %+v", svcs)
	}
	var web service
	admin.do(http.MethodGet, "/api/services/"+svcs[0].ID, nil, &web, http.StatusOK)
	if !web.running() {
		t.Fatalf("the preview's web isn't running: %+v", web.Containers)
	}

	// Then closes it: its containers go, production's stay.
	ci(t, "--url", a.url, "--token", token, "--project", "shop", "--name", "pr-1", "--close")
	admin.do(http.MethodGet, "/api/projects/"+p.ID+"/previews", nil, &previews, http.StatusOK)
	if len(previews) != 0 {
		t.Errorf("previews after closing: %+v", previews)
	}
	admin.do(http.MethodGet, "/api/projects/"+pr, nil, nil, http.StatusNotFound)
	for project, want := range map[string]int{pr: 0, p.ID: 1} {
		cts, err := a.dk.ListContainers(t.Context(), map[string]string{domain.LabelProject: project})
		if err != nil {
			t.Fatal(err)
		}
		if len(cts) != want {
			t.Errorf("project %s has %d containers, want %d", project, len(cts), want)
		}
	}
}
