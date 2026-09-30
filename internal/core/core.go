// Package core is the service layer. REST handlers and MCP tools are thin
// adapters over it; validation and permissions are enforced here.
package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sync"
	"time"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

var (
	ErrInvalid = errors.New("invalid input")
	// ErrBusy means another operation (usually a deploy) holds the service.
	ErrBusy = errors.New("operation in progress")
	// ErrShuttingDown is returned for work submitted during shutdown.
	ErrShuttingDown = errors.New("manager is shutting down")
)

// Names end up in container, network and DNS names, so keep them DNS-safe.
var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)

type Core struct {
	cfg   config.Config
	store store.Store
	pool  *docker.Pool
	log   *slog.Logger

	// Background work (deploys) runs under bg and is awaited on Shutdown.
	bg     context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	bgMu   sync.Mutex
	closed bool
	locks  sync.Map // service ID -> *sync.Mutex

	setupMu    sync.Mutex
	setupToken string // set while no admin exists

	sched         scheduler
	verifySem     chan struct{}
	reconcileKick chan struct{}
	probes        sync.Map // server ID -> *mount.Mount (nil: no probe there)
}

// New builds the core around the local Docker client; remote servers are
// connected on demand over SSH.
func New(cfg config.Config, s store.Store, local *docker.Client, log *slog.Logger) *Core {
	bg, cancel := context.WithCancel(context.Background())
	c := &Core{cfg: cfg, store: s, log: log, bg: bg, cancel: cancel, verifySem: make(chan struct{}, 1), reconcileKick: make(chan struct{}, 1)}
	c.pool = docker.NewPool(local, c.connectServer)
	return c
}

// Bootstrap prepares host-level resources the manager relies on.
func (c *Core) Bootstrap(ctx context.Context) error {
	if n, err := c.store.FailRunningDeployments(ctx, "interrupted by manager restart"); err != nil {
		return err
	} else if n > 0 {
		c.log.Warn("marked interrupted deployments as failed", "count", n)
	}
	if err := c.store.FailRunningOperations(ctx, "interrupted by manager restart"); err != nil {
		return err
	}
	servers, err := c.store.ListServers(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, sv := range servers {
		if err := c.bootstrapServer(ctx, sv); err != nil {
			errs = append(errs, fmt.Errorf("server %s: %w", sv.Name, err))
		}
	}
	return errors.Join(errs...)
}

// bootstrapServer prepares a Docker host: proxy network, Traefik, and
// cleanup of throwaway containers left by a crash.
func (c *Core) bootstrapServer(ctx context.Context, sv store.Server) error {
	dk := c.dockerFor(sv.ID)
	if err := dk.EnsureNetwork(ctx, docker.ProxyNetwork); err != nil {
		return err
	}
	if err := c.cleanupRestoreTests(ctx, dk); err != nil {
		return err
	}
	if c.cfg.Traefik.Enabled {
		if err := c.ensureTraefik(ctx, sv); err != nil {
			return fmt.Errorf("traefik: %w", err)
		}
	}
	return nil
}

// Shutdown cancels background work and waits for it to wind down.
func (c *Core) Shutdown(ctx context.Context) error {
	c.stopScheduler()
	c.bgMu.Lock()
	c.closed = true
	c.bgMu.Unlock()
	c.cancel()
	done := make(chan struct{})
	go func() { c.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// dockerFor returns the Docker client of a server ("" or "local": this one).
func (c *Core) dockerFor(serverID string) *docker.Client {
	return c.pool.For(serverID)
}

// goBackground runs f as tracked background work, unless shutdown started.
func (c *Core) goBackground(f func()) error {
	c.bgMu.Lock()
	defer c.bgMu.Unlock()
	if c.closed {
		return ErrShuttingDown
	}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		f()
	}()
	return nil
}

// lockService serializes mutating operations on one service. It never
// blocks: a second caller gets ErrBusy.
func (c *Core) lockService(id string) (unlock func(), err error) {
	m, _ := c.locks.LoadOrStore(id, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	if !mu.TryLock() {
		return nil, ErrBusy
	}
	return mu.Unlock, nil
}

type Status struct {
	Docker      *docker.Info `json:"docker"`
	DockerError string       `json:"dockerError,omitempty"`
}

func (c *Core) Status(ctx context.Context) Status {
	info, err := c.dockerFor(store.LocalServerID).Info(ctx)
	if err != nil {
		return Status{DockerError: err.Error()}
	}
	return Status{Docker: &info}
}

func (c *Core) ListProjects(ctx context.Context) ([]store.Project, error) {
	return c.store.ListProjects(ctx)
}

// CreateProject creates a project on a server (this one when serverID is empty).
func (c *Core) CreateProject(ctx context.Context, name, serverID string) (store.Project, error) {
	if !nameRe.MatchString(name) {
		return store.Project{}, fmt.Errorf("%w: name must be lowercase letters, digits and dashes (max 40)", ErrInvalid)
	}
	if serverID == "" {
		serverID = store.LocalServerID
	}
	if _, err := c.store.GetServer(ctx, serverID); errors.Is(err, store.ErrNotFound) {
		return store.Project{}, fmt.Errorf("%w: unknown server", ErrInvalid)
	} else if err != nil {
		return store.Project{}, err
	}
	p, err := c.store.CreateProject(ctx, store.Project{Name: name, ServerID: serverID})
	if err != nil {
		return store.Project{}, err
	}
	if err := c.dockerFor(p.ServerID).EnsureNetwork(ctx, docker.ProjectNetwork(p.ID)); err != nil {
		return p, fmt.Errorf("project created but network setup failed: %w", err)
	}
	return p, nil
}

func (c *Core) GetProject(ctx context.Context, id string) (store.Project, error) {
	return c.store.GetProject(ctx, id)
}

// DeleteProject removes every container and database volume of the project,
// its record and its private network. The caller must confirm with the name.
func (c *Core) DeleteProject(ctx context.Context, id, confirm string) error {
	p, err := c.store.GetProject(ctx, id)
	if err != nil {
		return err
	}
	if confirm != p.Name {
		return fmt.Errorf("%w: confirm with the project name", ErrInvalid)
	}
	svcs, err := c.store.ListServices(ctx, id)
	if err != nil {
		return err
	}
	// Hold every service so nothing (deploy, backup, reconciler) recreates
	// containers while the project goes away.
	for _, s := range svcs {
		unlock, err := c.lockService(s.ID)
		if err != nil {
			return fmt.Errorf("%s: %w", s.Name, err)
		}
		defer unlock()
	}
	for _, s := range svcs {
		if err := c.removeServiceContainers(ctx, s); err != nil {
			return err
		}
	}
	if err := c.store.DeleteProject(ctx, id); err != nil {
		return err
	}
	for _, s := range svcs {
		c.removeDeployLogs(s.ID)
	}
	return c.dockerFor(p.ServerID).RemoveNetwork(ctx, docker.ProjectNetwork(id))
}

const stopTimeout = 10 * time.Second
