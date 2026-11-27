package git

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

// A forge asking for credentials is a refused repository; other failures
// aren't. CheckAddr sees the address dialed and can refuse it.
func TestRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/private.git/info/refs" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	ctx := context.Background()
	if _, err := (Git{}).Head(ctx, domain.GitRemote{URL: srv.URL + "/private.git", Branch: "main"}); !errors.Is(err, domain.ErrGitRefused) {
		t.Errorf("401: %v", err)
	}
	if _, err := (Git{}).Head(ctx, domain.GitRemote{URL: srv.URL + "/broken.git", Branch: "main"}); err == nil || errors.Is(err, domain.ErrGitRefused) {
		t.Errorf("500: %v", err)
	}
	fresh := httptest.NewServer(http.NotFoundHandler()) // a new connection: pooled ones aren't dialed again
	defer fresh.Close()
	errPrivate := errors.New("private")
	var dialed netip.Addr
	check := func(ip netip.Addr) error { dialed = ip; return errPrivate }
	if _, err := (Git{}).Clone(ctx, t.TempDir(), domain.GitRemote{URL: fresh.URL + "/r.git", Branch: "main", CheckAddr: check}); !errors.Is(err, errPrivate) || !dialed.IsLoopback() {
		t.Errorf("checked dial to %s: %v", dialed, err)
	}
}
