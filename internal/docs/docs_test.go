package docs

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fixture serves the site (when up) and GitHub's raw files and contents API.
func fixture(t *testing.T, siteUp bool) *Fetcher {
	t.Helper()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !siteUp {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		switch r.URL.Path {
		case "/llms.txt":
			_, _ = w.Write([]byte("# kipitiny\n\n- [Compose reference](/docs/compose/llms.txt)\n"))
		case "/docs/compose/llms.txt":
			_, _ = w.Write([]byte("# Compose reference\n\nfrom the site\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/raw/1.2.3/site/docs/compose.md":
			_, _ = w.Write([]byte("---\ntitle: Compose reference\norder: 6\n---\n\nfrom 1.2.3\n"))
		case r.URL.Path == "/raw/main/site/docs/backups.md":
			_, _ = w.Write([]byte("---\ntitle: Backups\n---\n\nfrom main\n"))
		case r.URL.Path == "/api/site/docs" && r.URL.Query().Get("ref") == "1.2.3":
			_, _ = w.Write([]byte(`[{"name":"compose.md","type":"file"},{"name":"img","type":"dir"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(site.Close)
	t.Cleanup(github.Close)
	return &Fetcher{Client: site.Client(), Site: site.URL, Raw: github.URL + "/raw", API: github.URL + "/api", Ref: "1.2.3"}
}

func TestGetFromSite(t *testing.T) {
	f := fixture(t, true)
	p, err := f.Get(context.Background(), "compose")
	if err != nil || p.Content != "# Compose reference\n\nfrom the site\n" || !strings.HasSuffix(p.Source, "/docs/compose/llms.txt") {
		t.Fatalf("Get = %+v, %v", p, err)
	}
	idx, err := f.Get(context.Background(), "")
	if err != nil || !strings.Contains(idx.Content, "/docs/compose/llms.txt") {
		t.Fatalf("index = %+v, %v", idx, err)
	}
}

func TestGetFallsBackToGitHub(t *testing.T) {
	f := fixture(t, false)
	p, err := f.Get(context.Background(), "compose")
	if err != nil || p.Content != "# Compose reference\n\nfrom 1.2.3\n" {
		t.Fatalf("Get = %+v, %v", p, err)
	}
	// A page newer than the manager's release is read from main.
	p, err = f.Get(context.Background(), "backups")
	if err != nil || p.Content != "# Backups\n\nfrom main\n" {
		t.Fatalf("Get(backups) = %+v, %v", p, err)
	}
	idx, err := f.Get(context.Background(), "")
	if err != nil || idx.Content != "# kipitiny\n\n## Docs\n\n- compose\n" {
		t.Fatalf("index = %q, %v", idx.Content, err)
	}
}

func TestGetUnknown(t *testing.T) {
	f := fixture(t, false)
	for _, slug := range []string{"nope", "../etc/passwd", "Compose"} {
		if _, err := f.Get(context.Background(), slug); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get(%q) err = %v, want ErrNotFound", slug, err)
		}
	}
}

func TestNewRef(t *testing.T) {
	if r := New("0.9.1").Ref; r != "0.9.1" {
		t.Errorf("release ref = %q", r)
	}
	if r := New("0.9.1-3-gabc-dirty").Ref; r != "main" {
		t.Errorf("dev ref = %q", r)
	}
}
