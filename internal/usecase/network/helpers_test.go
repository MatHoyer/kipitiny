package network

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
	cfapi "github.com/kipit-labs/kipitiny/internal/infrastructure/cloudflare"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/sqlite"
)

// fakeManager is the rest of the manager for the unit tests: no Docker, no
// password manager, analytics off, and a reconciler that does nothing.
type fakeManager struct{}

func (fakeManager) DockerFor(string) domain.ContainerRuntime    { return nil }
func (fakeManager) RegistryAuth(context.Context, string) string { return "" }
func (fakeManager) GoLoop(func()) error                         { return nil }
func (fakeManager) Kick()                                       {}
func (fakeManager) AnalyticsDomain(context.Context) string      { return "" }
func (fakeManager) CheckSecretScheme(context.Context, string) error {
	return errors.New("no password manager")
}
func (fakeManager) ProviderToken(context.Context, string) (string, error) { return "", nil }

func (fakeManager) PagesUpstream(context.Context, domain.Server, string) (string, error) {
	return "", nil
}

func (fakeManager) ResolveSecrets(context.Context, []string) (map[string]string, error) {
	return nil, errors.New("no password manager")
}

// newTestNetwork runs the network use cases on a temp SQLite store.
func newTestNetwork(t *testing.T, cfg config.Config) *Network {
	t.Helper()
	st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New(context.Background(), &cfg, st, newDNS, fakeManager{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// routerName is the one router labels define.
func routerName(t *testing.T, labels map[string]string) string {
	t.Helper()
	var out []string
	for k := range labels {
		if r, ok := strings.CutPrefix(k, "traefik.http.routers."); ok && strings.HasSuffix(r, ".rule") {
			out = append(out, strings.TrimSuffix(r, ".rule"))
		}
	}
	if len(out) != 1 {
		t.Fatalf("routers = %v, want one", out)
	}
	return out[0]
}

// newDNS is the real Cloudflare client: tests point cfapi.BaseURL at a fake.
func newDNS(token string) DNS { return cfapi.New(token) }
