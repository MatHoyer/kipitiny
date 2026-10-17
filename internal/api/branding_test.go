package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/core"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestBrandingLogo(t *testing.T) {
	m := newMembers(t)
	srv := httptest.NewServer(m.api)
	t.Cleanup(srv.Close)
	get := func() *http.Response {
		resp, err := http.Get(srv.URL + "/api/branding/logo?v=1")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}
	if resp := get(); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("no logo: %d", resp.StatusCode)
	}

	svg := `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`
	admin := core.WithActor(context.Background(), core.Actor{Kind: "user", Name: "admin", Role: store.RoleAdmin})
	logo := base64.StdEncoding.EncodeToString([]byte(svg))
	if _, err := m.c.SetBranding(admin, core.BrandingInput{Logo: &logo}); err != nil {
		t.Fatal(err)
	}
	resp := get() // without a session
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != svg {
		t.Fatalf("logo: %d %q", resp.StatusCode, body)
	}
	if typ := resp.Header.Get("Content-Type"); typ != "image/svg+xml" {
		t.Errorf("content type %q", typ)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") || !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("CSP %q", csp)
	}
	if !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Errorf("cache %q", resp.Header.Get("Cache-Control"))
	}
}

func TestManifest(t *testing.T) {
	m := newMembers(t)
	srv := httptest.NewServer(m.api)
	t.Cleanup(srv.Close)
	get := func() core.Manifest {
		resp, err := http.Get(srv.URL + "/api/manifest.webmanifest") // without a session
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/manifest+json" {
			t.Fatalf("manifest: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
		}
		var mf core.Manifest
		if err := json.NewDecoder(resp.Body).Decode(&mf); err != nil {
			t.Fatal(err)
		}
		return mf
	}
	mf := get()
	if mf.Name != "kipitiny" || mf.Display != "standalone" || mf.ThemeColor != "" || len(mf.Icons) != 3 {
		t.Fatalf("default manifest: %+v", mf)
	}

	admin := core.WithActor(context.Background(), core.Actor{Kind: "user", Name: "admin", Role: store.RoleAdmin})
	logo := base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`))
	if _, err := m.c.SetBranding(admin, core.BrandingInput{Name: "Acme", Theme: core.Theme{Accent: "#4f46e5"}, Logo: &logo}); err != nil {
		t.Fatal(err)
	}
	mf = get()
	if mf.Name != "Acme" || mf.ThemeColor != "#4f46e5" || len(mf.Icons) != 1 ||
		!strings.HasPrefix(mf.Icons[0].Src, "/api/branding/logo?v=") || mf.Icons[0].Type != "image/svg+xml" {
		t.Fatalf("branded manifest: %+v", mf)
	}
}
