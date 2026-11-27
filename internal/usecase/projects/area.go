// Package projects is how apps are grouped and fed: projects themselves,
// their git repositories and git providers, scheduled jobs, templates,
// tags, the canvas layout and the manager's branding.
package projects

import (
	"context"
	"log/slog"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/notify"
	"github.com/kipit-labs/kipitiny/internal/usecase/deploy"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"
	"github.com/kipit-labs/kipitiny/internal/usecase/pending"
)

// The errors use cases share, under the names handlers know them by.
var (
	ErrInvalid      = domain.ErrInvalid
	ErrBusy         = domain.ErrBusy
	ErrShuttingDown = domain.ErrShuttingDown
)

type Projects struct {
	// cfg is the manager's configuration, shared with core.
	cfg   *config.Config
	store domain.Store
	// cron reads and runs the jobs' schedules.
	cron domain.Cron
	// git reads the linked repositories.
	git domain.Git
	mgr Manager
	log *slog.Logger
	// bg ends when the manager stops: git syncs and jobs run under it.
	bg context.Context

	sched     scheduler
	gitKick   chan string // project IDs to sync
	gitFlows  pending.Map[gitFlow]
	gitTokens gitTokens
	jobs      jobsState
}

// New builds the projects use cases; bg is the manager's background context.
func New(bg context.Context, cfg *config.Config, store domain.Store, cron domain.Cron, git domain.Git, mgr Manager, log *slog.Logger) *Projects {
	return &Projects{cfg: cfg, store: store, cron: cron, git: git, mgr: mgr, log: log, bg: bg, gitKick: make(chan string, 16)}
}

// Manager is what projects uses from the rest of the manager.
type Manager interface {
	// DockerFor is the Docker client of a server.
	DockerFor(serverID string) *docker.Client
	// GoBackground runs an operation the manager waits for on shutdown,
	// GoLoop a loop that ends with it.
	GoBackground(f func()) error
	GoLoop(f func()) error
	// LockService holds a service for one operation, ErrBusy when another
	// one runs.
	LockService(id string) (unlock func(), err error)

	// Identity: the creator's grant on a new project, and the actor a git
	// sync runs as (the user who linked it, restricted like their token).
	GrantCreator(ctx context.Context, projectID string) error
	UserActor(ctx context.Context, u domain.User) (identity.Actor, error)
	Restricted(ctx context.Context, a identity.Actor, t domain.APIToken) (identity.Actor, error)

	// Monitoring: notifications.
	Notify(e notify.Event, path, key string)

	// Deploy: compose files (a git sync, a template), a job's container, a
	// deleted project's containers and logs.
	ApplyCompose(ctx context.Context, projectID string, data []byte, opts deploy.ApplyOptions) (deploy.ComposePlan, error)
	ApplyGitCompose(ctx context.Context, projectID string, data []byte, opts deploy.ApplyOptions, files func(path string) ([]domain.ServiceFile, error)) (deploy.ComposePlan, map[string]string, error)
	PlanCompose(ctx context.Context, project domain.Project, data []byte, opts deploy.ApplyOptions) (deploy.ComposePlan, error)
	StartJobContainer(ctx context.Context, project domain.Project, svc domain.Service, j domain.Job, runID string, logf func(string, ...any)) (*docker.Client, string, error)
	RemoveServiceContainers(ctx context.Context, svc domain.Service) error
	RemoveDeployLogs(serviceID string)
}
