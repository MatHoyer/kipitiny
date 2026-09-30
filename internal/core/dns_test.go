package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/cloudflare"
	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
	"github.com/MatHoyer/kipitiny/internal/store/sqlite"
)

func newTestCore(t *testing.T, cfg config.Config) *Core {
	t.Helper()
	st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New(cfg, st, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestCertResolver(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	if got := c.certResolver(ctx, store.LocalServerID, "shop.example.com"); got != certResolver {
		t.Errorf("not connected: %q", got)
	}
	if got := c.certResolver(ctx, store.LocalServerID, "shop.localhost"); got != "" {
		t.Errorf(".localhost: %q", got)
	}

	s := c.cf(ctx)
	s.token, s.zones = "tok", []cloudflare.Zone{{ID: "z", Name: "example.com"}}
	if got := c.certResolver(ctx, "01REMOTE", "shop.example.com"); got != certResolverDNS {
		t.Errorf("cloudflare zone: %q", got)
	}
	if got := c.certResolver(ctx, "01REMOTE", "shop.other.org"); got != certResolver {
		t.Errorf("other zone: %q", got)
	}

	c.cfg.Tunnel.Token = "t"
	if got := c.certResolver(ctx, store.LocalServerID, "shop.example.com"); got != "" {
		t.Errorf("behind the tunnel: %q", got)
	}
}

func TestZoneNamesSurviveRestart(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	if err := c.store.SetSetting(ctx, cfTokenSetting, "tok"); err != nil {
		t.Fatal(err)
	}
	c.saveZoneNames(ctx, []cloudflare.Zone{{ID: "z", Name: "example.com"}})

	restarted := New(config.Config{}, c.store, nil, c.log)
	if !restarted.onCloudflare(ctx, "shop.example.com") {
		t.Error("certificates would use the wrong challenge until the first sync")
	}
}

func TestTunnelRoutes(t *testing.T) {
	existing := []cloudflare.Ingress{
		{"hostname": "old.example.com", "service": traefikOrigin}, // ours, gone
		{"hostname": "ssh.example.com", "service": "ssh://localhost:22"},
		{"service": "http_status:404"},
	}
	got := tunnelRoutes(existing, []string{"b.example.com", "a.example.com"})
	want := []string{"a.example.com", "b.example.com", "ssh.example.com", ""}
	if len(got) != len(want) {
		t.Fatalf("routes = %v", got)
	}
	for i, h := range want {
		if got[i].Hostname() != h {
			t.Errorf("route %d = %q, want %q", i, got[i].Hostname(), h)
		}
	}
	if got[0].Service() != traefikOrigin || got[3].Service() != "http_status:404" {
		t.Errorf("services = %v", got)
	}
	if last := tunnelRoutes(nil, nil); len(last) != 1 || last[0].Service() != "http_status:404" {
		t.Errorf("empty tunnel needs a catch-all: %v", last)
	}
}

func TestProxiedFor(t *testing.T) {
	ds := []store.Domain{{Name: "example.com", Proxied: true}, {Name: "direct.example.com"}}
	if !proxiedFor(ds, "shop.example.com") || proxiedFor(ds, "api.direct.example.com") || proxiedFor(ds, "other.org") {
		t.Error("the longest listed domain decides")
	}
}

func TestWantedRecords(t *testing.T) {
	ctx := context.Background()
	tok := base64.StdEncoding.EncodeToString([]byte(`{"a":"acc","t":"tid","s":"x"}`))
	c := newTestCore(t, config.Config{Tunnel: config.Tunnel{Token: tok}, Traefik: config.Traefik{Enabled: true}})
	remote, err := c.store.CreateServer(ctx, store.Server{Name: "w1", Kind: store.ServerSSH, Host: "w1", Port: 22, PublicIP: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.CreateDomain(ctx, store.Domain{Name: "example.com", Proxied: true}); err != nil {
		t.Fatal(err)
	}
	local, _ := c.store.CreateProject(ctx, store.Project{Name: "a", ServerID: store.LocalServerID})
	far, _ := c.store.CreateProject(ctx, store.Project{Name: "b", ServerID: remote.ID})
	for _, s := range []store.Service{
		{ProjectID: local.ID, ServerID: store.LocalServerID, Name: "web", Kind: store.ServiceKindApp, Image: "x", Replicas: 1, Domain: "tunnelled.example.com", Port: 80},
		{ProjectID: far.ID, ServerID: remote.ID, Name: "web", Kind: store.ServiceKindApp, Image: "x", Replicas: 1, Domain: "direct.example.com", Port: 80},
		{ProjectID: far.ID, ServerID: remote.ID, Name: "api", Kind: store.ServiceKindApp, Image: "x", Replicas: 1, Domain: "api.elsewhere.net", Port: 80},
	} {
		if _, err := c.store.CreateService(ctx, s); err != nil {
			t.Fatal(err)
		}
	}

	wants, err := c.wantedRecords(ctx, []cloudflare.Zone{{ID: "z", Name: "example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(wants) != 2 {
		t.Fatalf("wants = %+v (a domain outside the zones must be left alone)", wants)
	}
	if w := wants["tunnelled.example.com"]; !w.tunnel || w.record.Type != "CNAME" || w.record.Content != "tid.cfargotunnel.com" || !w.record.Proxied {
		t.Errorf("tunnelled = %+v", w)
	}
	if w := wants["direct.example.com"]; w.tunnel || w.record.Type != "A" || w.record.Content != "203.0.113.9" || !w.record.Proxied {
		t.Errorf("direct = %+v", w)
	}
}

// fakeCloudflare is an in-memory Cloudflare API: one zone, its records and
// one tunnel's configuration. writes counts mutations.
type fakeCloudflare struct {
	mu      sync.Mutex
	records map[string]cloudflare.Record
	ingress []any
	writes  int
	nextID  int
}

func (f *fakeCloudflare) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ok := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, `{"success":true,"result":%s,"result_info":{"page":1,"total_pages":1}}`, b)
	}
	path, q := r.URL.Path, r.URL.Query()
	switch {
	case path == "/zones":
		ok([]cloudflare.Zone{{ID: "z1", Name: "example.com"}})
	case path == "/zones/z1/settings/ssl":
		ok(map[string]string{"value": "full"})
	case path == "/zones/z1/dns_records" && r.Method == http.MethodGet:
		out := []cloudflare.Record{}
		for _, rec := range f.records {
			if (q.Get("name.exact") == "" || rec.Name == q.Get("name.exact")) &&
				(q.Get("comment.exact") == "" || rec.Comment == q.Get("comment.exact")) {
				out = append(out, rec)
			}
		}
		ok(out)
	case path == "/zones/z1/dns_records" && r.Method == http.MethodPost:
		var rec cloudflare.Record
		json.NewDecoder(r.Body).Decode(&rec)
		f.nextID++
		rec.ID = fmt.Sprintf("new%d", f.nextID)
		f.records[rec.ID] = rec
		f.writes++
		ok(rec)
	case strings.HasPrefix(path, "/zones/z1/dns_records/"):
		id := strings.TrimPrefix(path, "/zones/z1/dns_records/")
		f.writes++
		if r.Method == http.MethodDelete {
			delete(f.records, id)
		} else {
			var rec cloudflare.Record
			json.NewDecoder(r.Body).Decode(&rec)
			rec.ID = id
			f.records[id] = rec
		}
		ok(map[string]string{"id": id})
	case path == "/accounts/acc/cfd_tunnel/tid/configurations" && r.Method == http.MethodGet:
		ok(map[string]any{"config": map[string]any{"ingress": f.ingress}})
	case path == "/accounts/acc/cfd_tunnel/tid/configurations" && r.Method == http.MethodPut:
		var body struct {
			Config struct {
				Ingress []any `json:"ingress"`
			} `json:"config"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		f.ingress = body.Config.Ingress
		f.writes++
		ok(map[string]any{})
	default:
		http.Error(w, `{"success":false,"errors":[{"message":"unexpected `+r.Method+" "+path+`"}]}`, http.StatusNotFound)
	}
}

func TestSyncDNS(t *testing.T) {
	ctx := context.Background()
	fake := &fakeCloudflare{
		records: map[string]cloudflare.Record{
			"hand":  {ID: "hand", Type: "A", Name: "handmade.example.com", Content: "192.0.2.1"},
			"stale": {ID: "stale", Type: "A", Name: "stale.example.com", Content: "192.0.2.2", Comment: cloudflare.ManagedComment},
			"old":   {ID: "old", Type: "A", Name: "direct.example.com", Content: "192.0.2.3", Comment: cloudflare.ManagedComment},
			"mx":    {ID: "mx", Type: "MX", Name: "example.com", Content: "mail.example.com"},
		},
		ingress: []any{
			map[string]any{"hostname": "ssh.example.com", "service": "ssh://localhost:22"},
			map[string]any{"service": "http_status:404"},
		},
	}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	defer func(old string) { cloudflare.BaseURL = old }(cloudflare.BaseURL)
	cloudflare.BaseURL = srv.URL

	tok := base64.StdEncoding.EncodeToString([]byte(`{"a":"acc","t":"tid","s":"x"}`))
	c := newTestCore(t, config.Config{Tunnel: config.Tunnel{Token: tok}, Traefik: config.Traefik{Enabled: true}})
	if err := c.store.SetSetting(ctx, cfTokenSetting, "cf-token"); err != nil {
		t.Fatal(err)
	}
	remote, _ := c.store.CreateServer(ctx, store.Server{Name: "w1", Kind: store.ServerSSH, Host: "w1", Port: 22, PublicIP: "203.0.113.9"})
	local, _ := c.store.CreateProject(ctx, store.Project{Name: "a", ServerID: store.LocalServerID})
	far, _ := c.store.CreateProject(ctx, store.Project{Name: "b", ServerID: remote.ID})
	for _, s := range []store.Service{
		{ProjectID: local.ID, ServerID: store.LocalServerID, Name: "web", Kind: store.ServiceKindApp, Image: "x", Replicas: 1, Domain: "tunnelled.example.com", Port: 80},
		{ProjectID: far.ID, ServerID: remote.ID, Name: "web", Kind: store.ServiceKindApp, Image: "x", Replicas: 1, Domain: "direct.example.com", Port: 80},
		{ProjectID: far.ID, ServerID: remote.ID, Name: "hand", Kind: store.ServiceKindApp, Image: "x", Replicas: 1, Domain: "handmade.example.com", Port: 80},
	} {
		if _, err := c.store.CreateService(ctx, s); err != nil {
			t.Fatal(err)
		}
	}

	if err := c.syncDNS(ctx); err != nil {
		t.Fatal(err)
	}

	byName := map[string]cloudflare.Record{}
	for _, r := range fake.records {
		if r.Type != "MX" {
			byName[r.Name] = r
		}
	}
	if r := byName["tunnelled.example.com"]; r.Type != "CNAME" || r.Content != "tid.cfargotunnel.com" || !r.Proxied || !r.Managed() {
		t.Errorf("tunnelled record = %+v", r)
	}
	if r := byName["direct.example.com"]; r.Content != "203.0.113.9" || r.ID != "old" {
		t.Errorf("direct record should be updated in place: %+v", r)
	}
	if r := byName["handmade.example.com"]; r.Content != "192.0.2.1" || r.Managed() {
		t.Errorf("a record kipitiny didn't create was touched: %+v", r)
	}
	if _, ok := byName["stale.example.com"]; ok {
		t.Error("the managed record of a removed domain should be deleted")
	}
	if _, ok := fake.records["mx"]; !ok {
		t.Error("an unrelated record was deleted")
	}
	if st := c.dnsStatus(ctx, "handmade.example.com"); st == nil || st.State != "conflict" {
		t.Errorf("handmade status = %+v", st)
	}
	if st := c.dnsStatus(ctx, "direct.example.com"); st == nil || st.State != "synced" {
		t.Errorf("direct status = %+v", st)
	}

	routes, _ := json.Marshal(fake.ingress)
	want := `[{"hostname":"tunnelled.example.com","originRequest":{"noTLSVerify":true,"originServerName":"tunnelled.example.com"},"service":"https://kipitiny-traefik:443"},{"hostname":"ssh.example.com","service":"ssh://localhost:22"},{"service":"http_status:404"}]`
	if string(routes) != want {
		t.Errorf("tunnel routes = %s", routes)
	}

	fake.writes = 0
	if err := c.syncDNS(ctx); err != nil {
		t.Fatal(err)
	}
	if fake.writes != 0 {
		t.Errorf("a second sync with nothing changed made %d writes", fake.writes)
	}
}
