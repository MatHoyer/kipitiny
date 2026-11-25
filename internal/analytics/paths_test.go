package analytics

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestSitePaths(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "a.db")
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	pub := Public(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := st.SetSites(ctx, []string{"shop.example.com", "blog.example.com"},
		map[string]Paths{"Shop.example.com": {Script: "/js/k.js", Hit: "/api/k"}, "gone.example.com": {Script: "/x.js", Hit: "/x"}}); err != nil {
		t.Fatal(err)
	}
	get := func(host, path string) (int, string) {
		req := httptest.NewRequest("GET", path, nil)
		req.Host = host
		rec := httptest.NewRecorder()
		pub.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	if code, js := get("shop.example.com:443", "/js/k.js"); code != 200 || !strings.Contains(js, `new URL("/api/k", s.src)`) {
		t.Errorf("own script: %d %s", code, js)
	}
	// The analytics domain's paths keep working, and other sites have no own paths.
	if code, js := get("stats.example.com", "/a.js"); code != 200 || !strings.Contains(js, `new URL("/e", s.src)`) {
		t.Errorf("/a.js: %d", code)
	}
	if code, _ := get("blog.example.com", "/js/k.js"); code != 404 {
		t.Errorf("another site's path: %d", code)
	}
	req := httptest.NewRequest("POST", "/api/k", strings.NewReader(`{"u":"https://shop.example.com/pricing"}`))
	req.Host = "shop.example.com"
	req.Header.Set("User-Agent", chromeUA)
	rec := httptest.NewRecorder()
	pub.ServeHTTP(rec, req)
	var n int
	st.db.NewRaw("SELECT COUNT(*) FROM events WHERE path = '/pricing'").Scan(ctx, &n)
	if rec.Code != 204 || n != 1 {
		t.Errorf("hit on the own path: %d, %d events", rec.Code, n)
	}
	st.Close()
	// Kept across a restart: the manager only sends them when they change.
	st2, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if p, ok := st2.sitePaths("shop.example.com"); !ok || p.Hit != "/api/k" {
		t.Errorf("reopened: %+v %v", p, ok)
	}
	if _, ok := st2.sitePaths("gone.example.com"); ok {
		t.Error("paths of an untracked host kept")
	}
}
