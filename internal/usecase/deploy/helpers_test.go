package deploy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/sqlite"
	"github.com/kipit-labs/kipitiny/internal/progress"
	"github.com/kipit-labs/kipitiny/internal/usecase/data"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"
	"github.com/kipit-labs/kipitiny/internal/usecase/monitoring"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
)

// testCore is the manager for the deploy tests: the real identity, network,
// monitoring and data use cases around the Deployer, on a temp SQLite and
// the Docker daemon dk (nil without one). It is every area's Manager, as
// core is; what core's lifecycle does (locks, background work, git, jobs)
// is done the simple way.
type testCore struct {
	*Deployer
	*identity.Identity
	*network.Network
	*monitoring.Monitoring
	*data.Data

	cfg   *config.Config
	store domain.Store
	dk    *docker.Client
	bg    context.Context
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
	c := &testCore{cfg: &cfg, store: st, bg: bg}
	t.Cleanup(func() { cancel(); c.ops.Wait(); st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c.Monitoring = monitoring.New(bg, c.cfg, st, c, log)
	c.Identity = identity.New(cfg, st, nil, c.Monitoring, log)
	c.Network = network.New(bg, c.cfg, st, c, log)
	c.Data = data.New(bg, c.cfg, st, &c.steps, c, log)
	c.Deployer = New(bg, c.cfg, st, &sync.Map{}, c, log)
	return c
}

func (c *testCore) DockerFor(string) *docker.Client { return c.dk }

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

func (c *testCore) RemoveProject(ctx context.Context, p domain.Project) error {
	return c.store.DeleteProject(ctx, p.ID)
}

// CheckGitOwned is core's, without git syncs (which run as the owner).
func (c *testCore) CheckGitOwned(ctx context.Context, projectID string) error {
	if _, err := c.store.GetProjectGit(ctx, projectID); errors.Is(err, domain.ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	return domain.ErrGitManaged
}

// WithGitFile reads the compose file in the directory g.RepoURL names,
// without git: the deploy tests check what is done with the file, core's
// tests the clone.
func (c *testCore) WithGitFile(_ context.Context, g domain.ProjectGit, fn func([]byte, string, func(string) ([]domain.ServiceFile, error)) error) error {
	data, err := os.ReadFile(filepath.Join(g.RepoURL, g.Path))
	if err != nil {
		return err
	}
	return fn(data, "", func(string) ([]domain.ServiceFile, error) {
		return nil, errors.New("no repository files in the deploy tests")
	})
}

func (c *testCore) DeleteJob(ctx context.Context, j domain.Job) error {
	return c.store.DeleteJob(ctx, j.ID)
}
func (c *testCore) ReloadJobs(context.Context) error          { return nil }
func (c *testCore) ForgetServiceJobs(string)                  {}
func (c *testCore) JobRunningIn(string) bool                  { return false }
func (c *testCore) NotifyOOM(context.Context, string, string) {}

func (c *testCore) RemoveJobContainers(context.Context, *docker.Client, string) error { return nil }

// OwnImageRef is the image of cfg's version, as core's for a release.
func (c *testCore) OwnImageRef() string { return c.cfg.Update.Image + ":" + c.cfg.Version }

func (c *testCore) BrandName(context.Context) string   { return "kipitiny" }
func (c *testCore) BrandLogo(context.Context) string   { return "" }
func (c *testCore) BrandAccent(context.Context) string { return "" }

func (c *testCore) ResourceAlerts(context.Context) domain.ResourceAlerts {
	return domain.ResourceAlerts{}
}

func (c *testCore) AnalyticsDomain(context.Context) string { return "" }

// The database services of the spec tests (the data tests have their own).
func pgService() domain.Service {
	return domain.Service{
		ID: "01DB", ProjectID: "P1", Name: "db", Kind: domain.ServiceKindPostgres,
		Image: data.DefaultPostgresImage, Replicas: 1, MemoryMB: 512, Env: data.NewPostgresEnv(),
	}
}

func redisService() domain.Service {
	return domain.Service{
		ID: "01CACHE", ProjectID: "P1", Name: "cache", Kind: domain.ServiceKindRedis,
		Image: data.DefaultRedisImage, Replicas: 1, MemoryMB: 256, Env: data.NewRedisEnv(),
	}
}

func mysqlService(kind domain.ServiceKind) domain.Service {
	return domain.Service{
		ID: "01SQL", ProjectID: "P1", Name: "sql", Kind: kind,
		Image: data.MySQLImageFor(kind), Replicas: 1, MemoryMB: 512, Env: data.NewMySQLEnv(),
	}
}

func mongoService() domain.Service {
	return domain.Service{
		ID: "01DOCS", ProjectID: "P1", Name: "docs", Kind: domain.ServiceKindMongoDB,
		Image: data.DefaultMongoImage, Replicas: 1, MemoryMB: 1024, Env: data.NewMongoEnv(),
	}
}

func servicesByName(t *testing.T, c *testCore, projectID string) map[string]domain.Service {
	t.Helper()
	svcs, err := c.store.ListServices(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]domain.Service{}
	for _, s := range svcs {
		out[s.Name] = s
	}
	return out
}

const webhook = "https://discord.com/api/webhooks/1/secret"

// redirect sends every request to srv, whatever its URL.
type redirect struct{ srv *httptest.Server }

func (r redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	u, _ := url.Parse(r.srv.URL)
	req.URL.Scheme, req.URL.Host = u.Scheme, u.Host
	return r.srv.Client().Transport.RoundTrip(req)
}
