// Package core is the service layer. REST handlers and MCP tools are thin
// adapters over it; validation and permissions are enforced here.
package core

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kipit-labs/kipitiny/internal/infrastructure/age"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/cron"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/git"
	"github.com/kipit-labs/kipitiny/internal/usecase/data"
	"github.com/kipit-labs/kipitiny/internal/usecase/deploy"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"
	"github.com/kipit-labs/kipitiny/internal/usecase/monitoring"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
	"github.com/kipit-labs/kipitiny/internal/usecase/projects"
	"github.com/kipit-labs/kipitiny/internal/usecase/servers"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/progress"
)

// The errors use cases share, under the names handlers know them by.
var (
	ErrInvalid      = domain.ErrInvalid
	ErrBusy         = domain.ErrBusy
	ErrShuttingDown = domain.ErrShuttingDown
)

type Core struct {
	// Users, sign-in, tokens, permissions and password managers.
	*identity.Identity
	// Domains, DNS, Traefik, routes, middlewares, ports and tunnels.
	*network.Network
	// Notifications, email, push, uptime checks, analytics and errors.
	*monitoring.Monitoring
	// Databases and the data browser, backups, restores, volumes and files.
	*data.Data
	// Services, compose files, deploys, previews, networks and sleep.
	*deploy.Deployer
	// Servers and their Docker clients, the reconciler, host checks and
	// updates, stats, the cleanup, terminals, logs, the manager's updates.
	*servers.Servers
	// Projects, their git repositories and providers, jobs, templates,
	// tags, the canvas and the branding.
	*projects.Projects

	cfg   config.Config
	store domain.Store
	log   *slog.Logger

	// Background work runs under bg. On Shutdown, operations (deploys,
	// backups) get to finish first; loops (reconciler) just stop with bg.
	bg     context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup // all background work
	ops    sync.WaitGroup // operations only
	// running counts the operations in flight, for the update's preflight.
	running atomic.Int64
	bgMu    sync.Mutex
	closed  bool
	locks   sync.Map // service ID -> *serviceLock
	busy    sync.Map // service ID -> struct{}: its lock is held

	// steps: what the running operations are at (backups and restores in
	// data, the cleanup in servers).
	steps  progress.Steps
	probes sync.Map // server ID -> *domain.Mount (nil: no probe there)
}

// New builds the core around the local Docker client; remote servers are
// connected on demand over SSH.
func New(cfg config.Config, s domain.Store, local *docker.Client, log *slog.Logger) *Core {
	bg, cancel := context.WithCancel(context.Background())
	c := &Core{cfg: cfg, store: s, log: log, bg: bg, cancel: cancel}
	c.Servers = c.newServers(local)
	c.Monitoring = monitoring.New(bg, &c.cfg, s, monitoringPorts{c}, log)
	c.Identity = identity.New(cfg, s, identity.SecretKinds(cfg, c.dockerFor(domain.LocalServerID)), c.Monitoring, log)
	c.Network = network.New(bg, &c.cfg, s, networkPorts{c}, log)
	c.Data = data.New(bg, &c.cfg, s, age.Encryption{}, cron.Cron{}, &c.steps, dataPorts{c}, log)
	c.Deployer = deploy.New(bg, &c.cfg, s, cron.Cron{}, &c.probes, deployPorts{c}, log)
	c.Projects = projects.New(bg, &c.cfg, s, cron.Cron{}, git.Git{}, projectsPorts{c}, log)
	return c
}

// Shutdown refuses new operations, waits until ctx for running ones (a deploy
// cut short by an upgrade would have to be redone), then cancels whatever is
// left and waits briefly for it to wind down.
func (c *Core) Shutdown(ctx context.Context) error {
	c.stopScheduler()
	c.bgMu.Lock()
	c.closed = true
	c.bgMu.Unlock()

	var err error
	select {
	case <-waitDone(&c.ops):
	default:
		c.log.Info("waiting for running operations to finish")
		select {
		case <-waitDone(&c.ops):
		case <-ctx.Done():
			c.log.Warn("cancelling unfinished operations")
			err = ctx.Err()
		}
	}
	c.cancel()
	c.CloseFilesHelpers()
	if cerr := c.CloseSecretProviders(context.WithoutCancel(ctx)); cerr != nil {
		c.log.Warn("removing password manager helpers failed", "err", cerr)
	}
	select {
	case <-waitDone(&c.wg):
	case <-time.After(servers.ShutdownCancelGrace):
		err = errors.New("background work did not stop after cancellation")
	}
	return err
}

func waitDone(wg *sync.WaitGroup) <-chan struct{} {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	return done
}

// dockerFor returns the Docker client of a server ("" or "local": this one).
func (c *Core) dockerFor(serverID string) domain.ContainerRuntime {
	return c.DockerFor(serverID)
}

// newServers builds servers around a pool of Docker clients: local, and
// remote ones over SSH, built on demand from what servers knows of them.
func (c *Core) newServers(local *docker.Client) *servers.Servers {
	var s *servers.Servers
	pool := docker.NewPool(local, func(id string) (*docker.Client, error) {
		r, err := s.RemoteDocker(id)
		if err != nil {
			return nil, err
		}
		return docker.NewSSH(docker.SSHConfig(r))
	})
	s = servers.New(c.bg, &c.cfg, c.store, pool, cron.Cron{}, &c.steps, &c.probes, serversPorts{c}, c.log)
	return s
}

// goBackground runs f as an operation that Shutdown lets finish, unless
// shutdown started.
func (c *Core) goBackground(f func()) error {
	return c.track(f, true)
}

// goLoop runs f until bg is cancelled; Shutdown doesn't wait for it first.
func (c *Core) goLoop(f func()) error {
	return c.track(f, false)
}

func (c *Core) track(f func(), op bool) error {
	c.bgMu.Lock()
	defer c.bgMu.Unlock()
	if c.closed {
		return ErrShuttingDown
	}
	c.wg.Add(1)
	if op {
		c.ops.Add(1)
		c.running.Add(1)
	}
	go func() {
		defer c.wg.Done()
		if op {
			defer c.ops.Done()
			defer c.running.Add(-1)
		}
		f()
	}()
	return nil
}

// serviceLock is one service's lock.
type serviceLock struct {
	mu       sync.Mutex
	released sync.Cond // on mu
	held     bool
	// background: the reconciler holds it, for one short pass users don't see.
	background bool
}

// lockService serializes mutating operations on one service. It never
// waits for another operation (a deploy can take minutes): a second caller
// gets ErrBusy. It only waits out a reconciler pass, which would otherwise
// fail what the user just asked, like a rename right after scaling.
func (c *Core) lockService(id string) (unlock func(), err error) {
	return c.acquireService(id, false)
}

// lockServiceBackground is the reconciler's: it never waits, and gives up on
// a service an operation holds.
func (c *Core) lockServiceBackground(id string) (unlock func(), err error) {
	return c.acquireService(id, true)
}

func (c *Core) acquireService(id string, background bool) (func(), error) {
	m, ok := c.locks.Load(id)
	if !ok {
		l := &serviceLock{}
		l.released.L = &l.mu
		m, _ = c.locks.LoadOrStore(id, l)
	}
	l := m.(*serviceLock)
	l.mu.Lock()
	defer l.mu.Unlock()
	for l.held && l.background && !background {
		l.released.Wait()
	}
	if l.held {
		return nil, ErrBusy
	}
	l.held, l.background = true, background
	c.busy.Store(id, struct{}{})
	return func() {
		l.mu.Lock()
		l.held, l.background = false, false
		c.busy.Delete(id)
		l.mu.Unlock()
		l.released.Broadcast()
	}, nil
}

// SetServerNetwork sets a server's public IP and tunnel, returning the
// server as clients see it (servers; network's returns the bare record).
func (c *Core) SetServerNetwork(ctx context.Context, id string, in network.ServerNetwork) (servers.ServerView, error) {
	return c.Servers.SetServerNetwork(ctx, id, in)
}
