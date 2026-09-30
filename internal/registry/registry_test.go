package registry

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestTags(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			if r.URL.Query().Get("scope") != "repository:owner/app:pull" {
				t.Errorf("scope = %q", r.URL.Query().Get("scope"))
			}
			fmt.Fprint(w, `{"token":"anon"}`)
		case r.Header.Get("Authorization") != "Bearer anon":
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="%s/token",service="test",scope="repository:owner/app:pull"`, srv.URL))
			w.WriteHeader(http.StatusUnauthorized)
		case r.URL.Query().Get("last") == "":
			w.Header().Set("Link", `</v2/owner/app/tags/list?n=1000&last=1.0.0>; rel="next"`)
			fmt.Fprint(w, `{"tags":["0.9.0","1.0.0"]}`)
		default:
			fmt.Fprint(w, `{"tags":["1.1.0","latest"]}`)
		}
	}))
	defer srv.Close()

	ref := strings.TrimPrefix(srv.URL, "https://") + "/owner/app"
	tags, err := Tags(context.Background(), srv.Client(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"0.9.0", "1.0.0", "1.1.0", "latest"}; !slices.Equal(tags, want) {
		t.Errorf("tags = %v, want %v", tags, want)
	}
}

func TestTagsPrivate(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="registry"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	if _, err := Tags(context.Background(), srv.Client(), strings.TrimPrefix(srv.URL, "https://")+"/a/b"); err == nil {
		t.Error("expected an error for a registry requiring credentials")
	}
}

func TestSplit(t *testing.T) {
	for ref, want := range map[string][2]string{
		"ghcr.io/mathoyer/kipitiny": {"ghcr.io", "mathoyer/kipitiny"},
		"localhost:5000/app":        {"localhost:5000", "app"},
		"cloudflare/cloudflared":    {"registry-1.docker.io", "cloudflare/cloudflared"},
		"traefik":                   {"registry-1.docker.io", "library/traefik"},
	} {
		if h, r := split(ref); h != want[0] || r != want[1] {
			t.Errorf("split(%q) = %q, %q", ref, h, r)
		}
	}
}
