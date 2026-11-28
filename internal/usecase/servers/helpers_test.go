package servers

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/domain/notify"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/cron"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/sqlite"
	"github.com/kipit-labs/kipitiny/internal/progress"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
)

// testCore is Servers on a temp SQLite, the rest of the manager faked.
type testCore struct {
	*Servers
	fake *fakeManager
}

func newTestCore(t *testing.T, cfg config.Config) testCore {
	t.Helper()
	st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	bg, cancel := context.WithCancel(context.Background())
	f := &fakeManager{store: st, logDir: t.TempDir()}
	t.Cleanup(func() { cancel(); f.ops.Wait(); st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return testCore{Servers: New(bg, &cfg, st, nil, nil, cron.Cron{}, &progress.Steps{}, &sync.Map{}, f, log), fake: f}
}

// fakeManager is the rest of the manager for the servers tests: background
// work in a wait group, the busy services and running operations as the
// test sets them, notifications recorded, everything else a no-op.
type fakeManager struct {
	store   domain.Store
	logDir  string
	ops     sync.WaitGroup
	running int
	busy    []string
	mu      sync.Mutex
	events  []notify.Event
}

func (f *fakeManager) GoBackground(fn func()) error {
	f.ops.Go(fn)
	return nil
}

func (f *fakeManager) GoLoop(fn func()) error         { return f.GoBackground(fn) }
func (f *fakeManager) RunningOperations() int         { return f.running }
func (f *fakeManager) BusyServices() []string         { return f.busy }
func (f *fakeManager) PruneAudit(context.Context)     {}
func (f *fakeManager) RefreshProxy(context.Context)   {}
func (f *fakeManager) DetectedPublicIP(string) string { return "" }

func (f *fakeManager) Audit(context.Context, string, string, int, error) {}

func (f *fakeManager) RequireOn(context.Context, string, identity.Perm) error { return nil }

func (f *fakeManager) CleanupSecretHelpers(context.Context, domain.ContainerRuntime) error {
	return nil
}
func (f *fakeManager) CleanupRestoreTests(context.Context, domain.ContainerRuntime) error { return nil }

func (f *fakeManager) EnsureTraefik(context.Context, domain.Server) error  { return nil }
func (f *fakeManager) EnsureTunnel(context.Context, domain.Server) error   { return nil }
func (f *fakeManager) EnsureNetworks(context.Context, domain.Server) error { return nil }
func (f *fakeManager) EnsureAnalytics(context.Context) error               { return nil }
func (f *fakeManager) MigrateSMTPSetting(context.Context) error            { return nil }

func (f *fakeManager) SetServerNetwork(ctx context.Context, id string, _ network.ServerNetwork) (domain.Server, error) {
	return f.store.GetServer(ctx, id)
}

func (f *fakeManager) Notify(e notify.Event, _, _ string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
}

func (f *fakeManager) ReconcileService(context.Context, domain.Project, domain.Service) error {
	return nil
}

func (f *fakeManager) TrackHealth(domain.Project, domain.Service, []domain.Container, time.Time) {}
func (f *fakeManager) ForgetHealth(string, map[string]bool)                                      {}

func (f *fakeManager) ActiveContainers(context.Context, domain.Service) ([]domain.Container, error) {
	return nil, nil
}

func (f *fakeManager) WakeFor(context.Context, domain.Service) error { return nil }

func (f *fakeManager) DeployLogPath(serviceID, deployID string) string {
	return filepath.Join(f.logDir, serviceID, deployID+".log")
}

func (f *fakeManager) RegistryAuth(context.Context, string) string { return "" }

func (f *fakeManager) RemoveStaleJobContainers(context.Context, domain.ContainerRuntime, []domain.Container) {
}
