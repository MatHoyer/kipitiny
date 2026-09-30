package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/core"
	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store/sqlite"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	ctx := context.Background()
	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	dc, err := docker.New() // lazily connects; auth tests never call Docker
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := core.New(config.Config{SetupToken: "setup-secret", DataDir: t.TempDir()}, st, dc, log)
	if err := c.InitAuth(ctx); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(c, log))
	t.Cleanup(srv.Close)
	return srv
}

type client struct {
	t   *testing.T
	srv *httptest.Server
	hc  *http.Client
}

func newClient(t *testing.T, srv *httptest.Server) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, srv: srv, hc: &http.Client{Jar: jar}}
}

func (c *client) do(method, path, body string, header ...string) int {
	c.t.Helper()
	req, _ := http.NewRequest(method, c.srv.URL+path, strings.NewReader(body))
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	res, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

func expect(t *testing.T, what string, got, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: status %d, want %d", what, got, want)
	}
}

func TestAuthFlow(t *testing.T) {
	srv := newServer(t)
	c := newClient(t, srv)

	expect(t, "health is public", c.do("GET", "/api/health", ""), 200)
	expect(t, "projects need auth", c.do("GET", "/api/projects", ""), 401)
	expect(t, "login before setup", c.do("POST", "/api/auth/login", `{"username":"admin","password":"correct-horse"}`), 401)

	expect(t, "setup with wrong token",
		c.do("POST", "/api/auth/setup", `{"setupToken":"nope","username":"admin","password":"correct-horse"}`), 401)
	expect(t, "setup with short password",
		c.do("POST", "/api/auth/setup", `{"setupToken":"setup-secret","username":"admin","password":"short"}`), 400)
	expect(t, "setup",
		c.do("POST", "/api/auth/setup", `{"setupToken":"setup-secret","username":"admin","password":"correct-horse"}`), 201)
	expect(t, "setup twice",
		c.do("POST", "/api/auth/setup", `{"setupToken":"setup-secret","username":"eve","password":"correct-horse"}`), 400)

	expect(t, "setup logs in", c.do("GET", "/api/projects", ""), 200)
	expect(t, "cross-origin mutation", c.do("POST", "/api/projects", `{"name":"x"}`, "Origin", "https://evil.example"), 403)

	expect(t, "logout", c.do("POST", "/api/auth/logout", ""), 204)
	expect(t, "session gone after logout", c.do("GET", "/api/projects", ""), 401)

	other := newClient(t, srv)
	expect(t, "wrong password", other.do("POST", "/api/auth/login", `{"username":"admin","password":"wrong-password"}`), 401)
	expect(t, "login", other.do("POST", "/api/auth/login", `{"username":"admin","password":"correct-horse"}`), 200)
	expect(t, "logged in", other.do("GET", "/api/projects", ""), 200)
}

func TestLoginRateLimit(t *testing.T) {
	srv := newServer(t)
	c := newClient(t, srv)
	expect(t, "setup",
		c.do("POST", "/api/auth/setup", `{"setupToken":"setup-secret","username":"admin","password":"correct-horse"}`), 201)

	for range 10 {
		expect(t, "bad login", c.do("POST", "/api/auth/login", `{"username":"admin","password":"wrong-password"}`), 401)
	}
	expect(t, "locked out even with the right password",
		c.do("POST", "/api/auth/login", `{"username":"admin","password":"correct-horse"}`), 429)
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name, remote, xff, cf, want string
		tunnel                      bool
	}{
		{"direct", "203.0.113.7:5000", "", "", "203.0.113.7", false},
		{"public peer can't spoof", "203.0.113.7:5000", "198.51.100.1", "", "203.0.113.7", false},
		{"behind traefik", "172.18.0.2:5000", "198.51.100.1", "", "198.51.100.1", false},
		{"last hop wins", "172.18.0.2:5000", "10.0.0.1, 198.51.100.1", "", "198.51.100.1", false},
		{"garbage header", "172.18.0.2:5000", "nope", "", "172.18.0.2", false},
		{"loopback without header", "127.0.0.1:5000", "", "", "127.0.0.1", false},
		{"tunnel uses cloudflare header", "172.18.0.2:5000", "172.18.0.9", "198.51.100.1", "198.51.100.1", true},
		{"cloudflare header ignored without tunnel", "172.18.0.2:5000", "198.51.100.2", "198.51.100.1", "198.51.100.2", false},
		{"public peer can't spoof cloudflare", "203.0.113.7:5000", "", "198.51.100.1", "203.0.113.7", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/login", nil)
			r.RemoteAddr = tt.remote
			if tt.xff != "" {
				r.Header.Set("X-Forwarded-For", tt.xff)
			}
			if tt.cf != "" {
				r.Header.Set("CF-Connecting-IP", tt.cf)
			}
			if got := clientIP(r, tt.tunnel); got != tt.want {
				t.Errorf("clientIP = %q, want %q", got, tt.want)
			}
		})
	}
}
