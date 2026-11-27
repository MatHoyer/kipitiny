// Package data is what apps keep: databases (PostgreSQL, MySQL, MongoDB,
// Redis) and the data browser, backups with their targets, schedules,
// restores, restore tests and anonymized copies, volumes and their files.
package data

import (
	"context"
	"log/slog"
	"sync"

	"github.com/moby/moby/api/types/container"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/notify"
	"github.com/kipit-labs/kipitiny/internal/progress"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"
)

// The errors use cases share, under the names handlers know them by.
var (
	ErrInvalid      = domain.ErrInvalid
	ErrBusy         = domain.ErrBusy
	ErrShuttingDown = domain.ErrShuttingDown
)

// Manager is what data uses from the rest of the manager.
type Manager interface {
	// DockerFor is the Docker client of a server; RegistryAuth the
	// credential to pull image with, "" for none.
	DockerFor(serverID string) *docker.Client
	RegistryAuth(ctx context.Context, image string) string
	// OwnImageRef is the manager's own image, which runs the SQLite helper.
	OwnImageRef() string
	// GoBackground runs an operation the manager waits for on shutdown;
	// ShuttingDown reports that it refuses new ones.
	GoBackground(f func()) error
	ShuttingDown() bool
	// LockService holds a service for one operation, ErrBusy when another
	// one runs.
	LockService(id string) (unlock func(), err error)
	// RequireOn checks the actor's permission on a service's project; Audit
	// records what it did.
	RequireOn(ctx context.Context, serviceID string, p identity.Perm) error
	Audit(ctx context.Context, action, target string, status int, err error)
	// Notify sends a notification to the channels that want it.
	Notify(e notify.Event, path, key string)
	// ServiceContainers are a service's containers, ActiveContainers those
	// of the deployment serving traffic; WakeFor wakes a sleeping service's
	// project.
	ServiceContainers(ctx context.Context, svc domain.Service) ([]container.Summary, error)
	ActiveContainers(ctx context.Context, svc domain.Service) ([]container.Summary, error)
	WakeFor(ctx context.Context, svc domain.Service) error
	// PreviewSettings are a project's preview settings, its anonymization
	// rules among them.
	PreviewSettings(ctx context.Context, projectID string) (domain.PreviewSettings, error)
}

type Data struct {
	// cfg is the manager's configuration, shared with core.
	cfg   *config.Config
	store domain.Store
	// crypt seals backups for targets with a key.
	crypt domain.Encryption
	// cron reads and runs the backup schedules.
	cron domain.Cron
	mgr  Manager
	log  *slog.Logger
	// bg ends when the manager stops: scheduled backups run under it.
	bg context.Context
	// steps is where backups, restores and restore tests report their step,
	// shared with core (the cleanup reports there, the update preflight reads it).
	steps *progress.Steps

	sched     scheduler
	verifySem chan struct{}
	// anonymizeMu makes starting an anonymized copy check-then-create.
	anonymizeMu sync.Mutex
	readUsers   sync.Map // container ID -> struct{}: the data browser's read-only user exists in that database
	files       filesState
}

// New builds the data use cases; bg is the manager's background context.
func New(bg context.Context, cfg *config.Config, store domain.Store, crypt domain.Encryption, cron domain.Cron, steps *progress.Steps, mgr Manager, log *slog.Logger) *Data {
	return &Data{cfg: cfg, store: store, crypt: crypt, cron: cron, mgr: mgr, log: log, bg: bg, steps: steps, verifySem: make(chan struct{}, 1)}
}
