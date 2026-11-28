package data

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
	"github.com/kipit-labs/kipitiny/internal/domain/notify"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/age"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/cron"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/sqlite"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/storage"
	"github.com/kipit-labs/kipitiny/internal/progress"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"
)

// fakeManager is the rest of the manager for the data tests: the real
// permission checks, the Docker daemon dk (nil without one), background work
// tracked by ops, services locked like core does.
type fakeManager struct {
	cfg   *config.Config
	id    *identity.Identity
	store domain.Store
	dk    domain.ContainerRuntime
	ops   *sync.WaitGroup
	locks *sync.Map
}

func (m fakeManager) DockerFor(string) domain.ContainerRuntime  { return m.dk }
func (fakeManager) RegistryAuth(context.Context, string) string { return "" }
func (fakeManager) ShuttingDown() bool                          { return false }

// OwnImageRef is the image of cfg's version (core's for a release or a
// dev-<branch> build, which is what the tests set).
func (m fakeManager) OwnImageRef() string {
	if m.cfg.Version == "" {
		return m.cfg.Update.Image + ":dev"
	}
	return m.cfg.Update.Image + ":" + m.cfg.Version
}

func (m fakeManager) GoBackground(f func()) error {
	m.ops.Go(f)
	return nil
}

func (m fakeManager) LockService(id string) (func(), error) {
	if _, held := m.locks.LoadOrStore(id, struct{}{}); held {
		return nil, ErrBusy
	}
	return func() { m.locks.Delete(id) }, nil
}

func (m fakeManager) RequireOn(ctx context.Context, serviceID string, p identity.Perm) error {
	return m.id.RequireOn(ctx, serviceID, p)
}

func (m fakeManager) Audit(ctx context.Context, action, target string, status int, err error) {
	m.id.Audit(ctx, action, target, status, err)
}

func (fakeManager) Notify(notify.Event, string, string) {}

func (m fakeManager) ServiceContainers(ctx context.Context, svc domain.Service) ([]domain.Container, error) {
	return m.dk.ListContainers(ctx, map[string]string{domain.LabelProject: svc.ProjectID, domain.LabelService: svc.ID})
}

// ActiveContainers is core's: the current deployment's, else the running ones.
func (m fakeManager) ActiveContainers(ctx context.Context, svc domain.Service) ([]domain.Container, error) {
	cts, err := m.ServiceContainers(ctx, svc)
	active := cts[:0]
	for _, ct := range cts {
		if svc.CurrentDeploymentID == "" && ct.State == domain.StateRunning || svc.CurrentDeploymentID != "" && ct.Labels[domain.LabelDeploy] == svc.CurrentDeploymentID {
			active = append(active, ct)
		}
	}
	return active, err
}

func (fakeManager) WakeFor(context.Context, domain.Service) error { return nil }

func (m fakeManager) PreviewSettings(ctx context.Context, projectID string) (domain.PreviewSettings, error) {
	ps, err := m.store.GetPreviewSettings(ctx, projectID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.PreviewSettings{ProjectID: projectID, Env: map[string]string{}}, nil
	}
	return ps, err
}

// newTestData runs the data use cases on a temp SQLite store, on the Docker
// daemon dk (nil: none); background work is awaited at cleanup.
func newTestData(t *testing.T, cfg config.Config, dk domain.ContainerRuntime) *Data {
	t.Helper()
	st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := fakeManager{cfg: &cfg, id: identity.New(cfg, st, nil, nil, log), store: st, dk: dk, ops: &sync.WaitGroup{}, locks: &sync.Map{}}
	t.Cleanup(func() {
		m.ops.Wait()
		st.Close()
	})
	open := func(t domain.BackupTarget) (Storage, error) {
		return storage.Open(t, storage.Env{DataDir: m.cfg.DataDir})
	}
	return New(context.Background(), m.cfg, st, age.Encryption{}, cron.Cron{}, open, &progress.Steps{}, m, log)
}
