// Package core is the service layer. REST handlers and MCP tools are thin
// adapters over it; validation and permissions are enforced here.
package core

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

var ErrInvalid = errors.New("invalid input")

// Names end up in container, network and DNS names, so keep them DNS-safe.
var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)

type Core struct {
	store  store.Store
	docker *docker.Client
}

func New(s store.Store, d *docker.Client) *Core {
	return &Core{store: s, docker: d}
}

// Bootstrap prepares host-level resources the manager relies on.
func (c *Core) Bootstrap(ctx context.Context) error {
	return c.docker.EnsureNetwork(ctx, docker.ProxyNetwork)
}

type Status struct {
	Docker      *docker.Info `json:"docker"`
	DockerError string       `json:"dockerError,omitempty"`
}

func (c *Core) Status(ctx context.Context) Status {
	info, err := c.docker.Info(ctx)
	if err != nil {
		return Status{DockerError: err.Error()}
	}
	return Status{Docker: &info}
}

func (c *Core) ListProjects(ctx context.Context) ([]store.Project, error) {
	return c.store.ListProjects(ctx)
}

func (c *Core) CreateProject(ctx context.Context, name string) (store.Project, error) {
	if !nameRe.MatchString(name) {
		return store.Project{}, fmt.Errorf("%w: name must be lowercase letters, digits and dashes (max 40)", ErrInvalid)
	}
	p, err := c.store.CreateProject(ctx, store.Project{Name: name})
	if err != nil {
		return store.Project{}, err
	}
	if err := c.docker.EnsureNetwork(ctx, docker.ProjectNetwork(p.ID)); err != nil {
		return p, fmt.Errorf("project created but network setup failed: %w", err)
	}
	return p, nil
}

func (c *Core) GetProject(ctx context.Context, id string) (store.Project, error) {
	return c.store.GetProject(ctx, id)
}

// DeleteProject removes the project record and its private network.
// Container teardown comes with the reconciler.
func (c *Core) DeleteProject(ctx context.Context, id string) error {
	if err := c.store.DeleteProject(ctx, id); err != nil {
		return err
	}
	return c.docker.RemoveNetwork(ctx, docker.ProjectNetwork(id))
}
