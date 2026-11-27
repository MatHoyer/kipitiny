package projects

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/age"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/cron"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/sqlite"
	"github.com/kipit-labs/kipitiny/internal/mail"
	"github.com/kipit-labs/kipitiny/internal/mail/mailtest"
	"github.com/kipit-labs/kipitiny/internal/progress"
	"github.com/kipit-labs/kipitiny/internal/usecase/data"
	"github.com/kipit-labs/kipitiny/internal/usecase/deploy"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"
	"github.com/kipit-labs/kipitiny/internal/usecase/monitoring"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
)

// testCore is the manager for the projects tests: the real identity,
// network, monitoring, data and deploy use cases around Projects, on a temp
// SQLite and no Docker daemon. It is every area's Manager, as core is;
// what core's lifecycle and servers do (locks, background work, the
// reconciler, stats) is done the simple way.
type testCore struct {
	*Projects
	*deploy.Deployer
	*identity.Identity
	*network.Network
	*monitoring.Monitoring
	*data.Data

	cfg   *config.Config
	store domain.Store
	ops   sync.WaitGroup
	locks sync.Map
	steps progress.Steps
}

func newTestCore(t *testing.T, cfg config.Config) *testCore {
	t.Helper()
	st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	bg, cancel := context.WithCancel(context.Background())
	c := &testCore{cfg: &cfg, store: st}
	t.Cleanup(func() { cancel(); c.ops.Wait(); st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c.Monitoring = monitoring.New(bg, c.cfg, st, c, log)
	c.Identity = identity.New(cfg, st, nil, c.Monitoring, log)
	c.Network = network.New(bg, c.cfg, st, c, log)
	c.Data = data.New(bg, c.cfg, st, age.Encryption{}, cron.Cron{}, &c.steps, c, log)
	c.Deployer = deploy.New(bg, c.cfg, st, cron.Cron{}, &sync.Map{}, deployManager{c}, log)
	c.Projects = New(bg, c.cfg, st, cron.Cron{}, c, log)
	return c
}

// deployManager is deploy's Manager: its DeleteJob takes the job, not its ID.
type deployManager struct{ *testCore }

func (m deployManager) DeleteJob(ctx context.Context, j domain.Job) error {
	return m.RemoveJob(ctx, j)
}

func (c *testCore) DockerFor(string) *docker.Client { return nil }

func (c *testCore) GoBackground(f func()) error {
	c.ops.Go(f)
	return nil
}

func (c *testCore) GoLoop(f func()) error { return c.GoBackground(f) }

func (c *testCore) ShuttingDown() bool { return false }

func (c *testCore) LockService(id string) (func(), error) {
	if _, held := c.locks.LoadOrStore(id, struct{}{}); held {
		return nil, ErrBusy
	}
	return func() { c.locks.Delete(id) }, nil
}

func (c *testCore) LockServiceBackground(id string) (func(), error) { return c.LockService(id) }

func (c *testCore) Kick() {}

func (c *testCore) NotifyOOM(context.Context, string, string) {}

// OwnImageRef is the image of cfg's version, as servers' for a release.
func (c *testCore) OwnImageRef() string { return c.cfg.Update.Image + ":" + c.cfg.Version }

func (c *testCore) BrandLogo(ctx context.Context) string { return c.Branding(ctx).Logo }

func (c *testCore) ResourceAlerts(context.Context) domain.ResourceAlerts {
	return domain.ResourceAlerts{}
}

func (c *testCore) AnalyticsDomain(context.Context) string { return "" }

// localProvider is an email provider sending to a fake server.
func localProvider(name string, srv *mailtest.Server) monitoring.EmailProviderInput {
	return monitoring.EmailProviderInput{Name: name, Config: mail.Config{Host: "127.0.0.1", Port: srv.Port, Security: mail.None, From: "ops@example.com"}}
}
