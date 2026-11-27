package projects

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

// A member's fetch is refused when it connects to a private address,
// whatever the name resolved to when the repository was linked.
func TestRepoDialGuard(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	c := newTestCore(t, config.Config{DataDir: t.TempDir()})
	g := domain.ProjectGit{RepoURL: srv.URL + "/r.git", Branch: "main", Path: "compose.yaml"}
	member := identity.WithActor(context.Background(), identity.Actor{Kind: "user", Name: "alice", Role: domain.RoleMember})
	if _, err := c.gitHead(member, g); !errors.Is(err, identity.ErrForbidden) {
		t.Errorf("member fetching from loopback: %v", err)
	}
	admin := identity.WithActor(context.Background(), identity.Actor{Kind: "user", Name: "root", Role: domain.RoleAdmin})
	if _, err := c.gitHead(admin, g); err == nil || errors.Is(err, identity.ErrForbidden) {
		t.Errorf("admin fetching from loopback reaches the server: %v", err)
	}
	for addr, private := range map[string]bool{"127.0.0.1": true, "10.1.2.3": true, "169.254.169.254": true, "100.100.1.1": true,
		"0.1.2.3": true, "64:ff9b::a00:1": true, "::ffff:192.168.0.1": true, "fd00::1": true, "1.1.1.1": false, "2606:4700::1111": false} {
		if got := privateAddr(netip.MustParseAddr(addr)); got != private {
			t.Errorf("privateAddr(%s) = %v", addr, got)
		}
	}
}
