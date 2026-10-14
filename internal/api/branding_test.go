package api

import (
	"context"
	"encoding/base64"
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
