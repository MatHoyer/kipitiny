package http

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/projects"
)

func TestBrandingLogo(t *testing.T) {
	m := newMembers(t)
	srv := httptest.NewServer(m.api)
	t.Cleanup(srv.Close)
	get := func() (int, http.Header, []byte) {
		resp, err := http.Get(srv.URL + "/api/branding/logo?v=1")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, resp.Header, body
	}
	if status, _, _ := get(); status != http.StatusNotFound {
		t.Fatalf("no logo: %d", status)
	}

	svg := `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`
	admin := identity.WithActor(context.Background(), identity.Actor{Kind: "user", Name: "admin", Role: domain.RoleAdmin})
	logo := base64.StdEncoding.EncodeToString([]byte(svg))
	if _, err := m.c.SetBranding(admin, projects.BrandingInput{Logo: &logo}); err != nil {
		t.Fatal(err)
	}
	status, header, body := get() // without a session
	if status != http.StatusOK || string(body) != svg {
		t.Fatalf("logo: %d %q", status, body)
	}
	if typ := header.Get("Content-Type"); typ != "image/svg+xml" {
		t.Errorf("content type %q", typ)
	}
	if csp := header.Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") || !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("CSP %q", csp)
	}
	if !strings.Contains(header.Get("Cache-Control"), "immutable") {
		t.Errorf("cache %q", header.Get("Cache-Control"))
	}
}

func TestManifest(t *testing.T) {
	m := newMembers(t)
	srv := httptest.NewServer(m.api)
	t.Cleanup(srv.Close)
	get := func() projects.Manifest {
		resp, err := http.Get(srv.URL + "/api/manifest.webmanifest") // without a session
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/manifest+json" {
			t.Fatalf("manifest: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
		}
		var mf projects.Manifest
		if err := json.NewDecoder(resp.Body).Decode(&mf); err != nil {
			t.Fatal(err)
		}
		return mf
	}
	mf := get()
	if mf.Name != "kipitiny" || mf.Display != "standalone" || mf.ThemeColor != "" || len(mf.Icons) != 3 {
		t.Fatalf("default manifest: %+v", mf)
	}

	admin := identity.WithActor(context.Background(), identity.Actor{Kind: "user", Name: "admin", Role: domain.RoleAdmin})
	logo := base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`))
	if _, err := m.c.SetBranding(admin, projects.BrandingInput{Name: "Acme", Theme: projects.Theme{Accent: "#4f46e5"}, Logo: &logo}); err != nil {
		t.Fatal(err)
	}
	mf = get()
	if mf.Name != "Acme" || mf.ThemeColor != "#4f46e5" || len(mf.Icons) != 1 ||
		!strings.HasPrefix(mf.Icons[0].Src, "/api/branding/logo?v=") || mf.Icons[0].Type != "image/svg+xml" {
		t.Fatalf("branded manifest: %+v", mf)
	}
}
