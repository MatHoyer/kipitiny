package projects

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/deploy"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"
)

func (c *Projects) ListProjects(ctx context.Context) ([]domain.Project, error) {
	ps, err := c.store.ListProjects(ctx)
	ps = slices.DeleteFunc(ps, func(p domain.Project) bool { return !identity.Visible(ctx, p.ID) })
	for i := range ps {
		ps[i] = deploy.MaskedProject(ps[i])
	}
	return ps, err
}

// CreateProject creates a project on a server (this one when serverID is empty).
func (c *Projects) CreateProject(ctx context.Context, name, serverID string) (domain.Project, error) {
	if err := identity.CheckFeature(ctx, identity.FeatureCreateProjects); err != nil {
		return domain.Project{}, err
	}
	if !domain.ValidName(name) {
		return domain.Project{}, fmt.Errorf("%w: name must be lowercase letters, digits and dashes (max 40)", ErrInvalid)
	}
	if serverID == "" {
		serverID = domain.LocalServerID
	}
	if _, err := c.store.GetServer(ctx, serverID); errors.Is(err, domain.ErrNotFound) {
		return domain.Project{}, fmt.Errorf("%w: unknown server", ErrInvalid)
	} else if err != nil {
		return domain.Project{}, err
	}
	p, err := c.store.CreateProject(ctx, domain.Project{Name: name, ServerID: serverID})
	if err != nil {
		return domain.Project{}, err
	}
	if err := c.mgr.GrantCreator(ctx, p.ID); err != nil {
		return p, err
	}
	if err := c.mgr.DockerFor(p.ServerID).EnsureNetwork(ctx, docker.ProjectNetwork(p.ID)); err != nil {
		return p, fmt.Errorf("project created but network setup failed: %w", err)
	}
	return p, nil
}

func (c *Projects) GetProject(ctx context.Context, id string) (domain.Project, error) {
	p, err := c.store.GetProject(ctx, id)
	return deploy.MaskedProject(p), err
}

// DeleteProject removes every container and volume of the project's services,
// its record and its private network. The caller must confirm with the name.
func (c *Projects) DeleteProject(ctx context.Context, id, confirm string) error {
	p, err := c.store.GetProject(ctx, id)
	if err != nil {
		return err
	}
	if confirm != p.Name {
		return fmt.Errorf("%w: confirm with the project name", ErrInvalid)
	}
	return c.RemoveProject(ctx, p)
}

// RemoveProject is DeleteProject without the confirmation; a project's
// previews go first.
func (c *Projects) RemoveProject(ctx context.Context, p domain.Project) error {
	id := p.ID
	all, err := c.store.ListProjects(ctx)
	if err != nil {
		return err
	}
	for _, child := range all {
		if child.ParentID == id {
			if err := c.RemoveProject(ctx, child); err != nil {
				return fmt.Errorf("preview %s: %w", child.Preview.Name, err)
			}
		}
	}
	svcs, err := c.store.ListServices(ctx, id)
	if err != nil {
		return err
	}
	// Hold every service so nothing (deploy, backup, reconciler) recreates
	// containers while the project goes away.
	for _, s := range svcs {
		unlock, err := c.mgr.LockService(s.ID)
		if err != nil {
			return fmt.Errorf("%s: %w", s.Name, err)
		}
		defer unlock()
	}
	for _, s := range svcs {
		if err := c.mgr.RemoveServiceContainers(ctx, s); err != nil {
			return err
		}
	}
	if err := c.store.DeleteProject(ctx, id); err != nil {
		return err
	}
	for _, s := range svcs {
		c.mgr.RemoveDeployLogs(s.ID)
		c.ForgetServiceJobs(s.ID)
	}
	dk := c.mgr.DockerFor(p.ServerID)
	nets, err := dk.ListNetworksMoby(ctx)
	if err != nil {
		return err
	}
	for _, n := range nets { // the networks of its compose file
		if strings.HasPrefix(n.Name, docker.ProjectNetwork(id)+"-") {
			if err := dk.RemoveNetwork(ctx, n.Name); err != nil {
				return err
			}
		}
	}
	return dk.RemoveNetwork(ctx, docker.ProjectNetwork(id))
}
