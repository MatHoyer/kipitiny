package monitoring

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
	notifych "github.com/kipit-labs/kipitiny/internal/infrastructure/notify"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/smtp"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/sqlite"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"
)

// fakeManager is the rest of the manager for the unit tests: no Docker,
// the default brand, background work tracked by ops.
type fakeManager struct {
	store domain.Store
	ops   *sync.WaitGroup
}

func (fakeManager) DockerFor(string) domain.ContainerRuntime    { return nil }
func (fakeManager) RegistryAuth(context.Context, string) string { return "" }
func (fakeManager) OwnImageRef() string                         { return "kipitiny:dev" }
func (fakeManager) GoLoop(func()) error                         { return errors.New("no background loops in tests") }

func (m fakeManager) GoBackground(f func()) error {
	m.ops.Go(f)
	return nil
}

func (fakeManager) BrandName(context.Context) string   { return domain.DefaultBrandName }
func (fakeManager) BrandLogo(context.Context) string   { return "" }
func (fakeManager) BrandAccent(context.Context) string { return "" }

func (m fakeManager) CurrentUser(ctx context.Context) (domain.User, error) {
	a, ok := identity.ActorFrom(ctx)
	if !ok || a.UserID == "" {
		return domain.User{}, identity.ErrUnauthorized
	}
	return m.store.GetUser(ctx, a.UserID)
}

func (fakeManager) CheckGitOwned(context.Context, string) error { return nil }
func (fakeManager) ResourceAlerts(context.Context) domain.ResourceAlerts {
	return domain.ResourceAlerts{}
}
func (fakeManager) CertResolver(context.Context, string, string) string { return "" }
func (fakeManager) KickDNS()                                            {}

// testMonitoring is the monitoring use cases with what tests wait on: ops
// holds the notifications being sent.
type testMonitoring struct {
	*Monitoring
	ops *sync.WaitGroup
}

// newTestMonitoring runs the monitoring use cases on a temp SQLite store.
func newTestMonitoring(t *testing.T, cfg config.Config) testMonitoring {
	t.Helper()
	st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ops := &sync.WaitGroup{}
	m := New(context.Background(), &cfg, st, smtp.Client{}, notifych.Channels{}, fakeManager{store: st, ops: ops}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return testMonitoring{Monitoring: m, ops: ops}
}
