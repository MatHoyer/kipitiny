package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/moby/moby/api/types/container"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// Everything Docker holds for a project or a service is keyed by ID (labels,
// network, volumes): a rename only changes the record, the containers' names
// and the service's alias on the project network. Nothing restarts.

// RenameProject changes a project's name and renames its containers.
func (c *Core) RenameProject(ctx context.Context, id, name string) (store.Project, error) {
	name = strings.TrimSpace(name)
	if !nameRe.MatchString(name) {
		return store.Project{}, fmt.Errorf("%w: name must be lowercase letters, digits and dashes (max 40)", ErrInvalid)
	}
	p, err := c.store.GetProject(ctx, id)
	if err != nil {
		return store.Project{}, err
	}
	if p.Name == name {
		return maskedProject(p), nil
	}
	svcs, err := c.store.ListServices(ctx, id)
	if err != nil {
		return store.Project{}, err
	}
	// Hold every service so no deploy creates a container under the old name.
	for _, s := range svcs {
		unlock, err := c.lockService(s.ID)
		if err != nil {
			return store.Project{}, fmt.Errorf("%s: %w", s.Name, err)
		}
		defer unlock()
	}
	if err := c.store.RenameProject(ctx, id, name); err != nil {
		return store.Project{}, err
	}
	var errs []error
	old := p
	p.Name = name
	for _, s := range svcs {
		if err := c.renameContainers(ctx, s, old.Name+"-"+s.Name+"-", name+"-"+s.Name+"-", nil); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.Name, err))
		}
		if err := c.renameOnNetworks(ctx, p, s); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.Name, err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return maskedProject(p), fmt.Errorf("project renamed but its containers keep their old names until the next deploy: %w", err)
	}
	return maskedProject(p), nil
}

// RenameService changes a service's name. Its running containers answer to
// both names on the project network until its next deploy. Renaming a
// database rewrites the references to it and redeploys the apps using it.
func (c *Core) RenameService(ctx context.Context, id, name string) (ServiceView, error) {
	name = strings.TrimSpace(name)
	if !nameRe.MatchString(name) {
		return ServiceView{}, fmt.Errorf("%w: name must be lowercase letters, digits and dashes (max 40)", ErrInvalid)
	}
	svc, err := c.store.GetService(ctx, id)
	if err != nil {
		return ServiceView{}, err
	}
	if svc.Name == name {
		return c.view(ctx, svc)
	}
	if err := c.checkGitOwned(ctx, svc.ProjectID); err != nil {
		return ServiceView{}, err
	}
	project, err := c.store.GetProject(ctx, svc.ProjectID)
	if err != nil {
		return ServiceView{}, err
	}
	others, err := c.store.ListServices(ctx, svc.ProjectID)
	if err != nil {
		return ServiceView{}, err
	}
	unlock, err := c.lockService(id)
	if err != nil {
		return ServiceView{}, err
	}
	defer unlock()

	old := svc.Name
	if err := c.store.RenameService(ctx, id, name); err != nil {
		return ServiceView{}, err
	}
	svc.Name = name

	var dependents []string
	if svc.Kind.IsDatabase() {
		for _, s := range others {
			if s.ID == id || !usesDatabase(s.Env, old) {
				continue
			}
			s.Env = renameDatabaseRefs(s.Env, old, name)
			if _, err := c.store.UpdateService(ctx, s); err != nil {
				return ServiceView{}, fmt.Errorf("update references in %s: %w", s.Name, err)
			}
			if s.Kind == store.ServiceKindApp && !s.Stopped && s.CurrentDeploymentID != "" {
				dependents = append(dependents, s.ID)
			}
		}
	}

	var aliases []string
	if !svc.HostNetwork {
		aliases = []string{name, old}
	}
	renameErr := errors.Join(
		c.renameContainers(ctx, svc, project.Name+"-"+old+"-", project.Name+"-"+name+"-", aliases),
		c.renameOnNetworks(ctx, project, svc),
	)

	// The apps' running containers still reach the database by its old
	// alias, which its next deploy drops: move them to the new name now.
	if len(dependents) > 0 {
		if err := c.goBackground(func() { c.deployInOrder(nil, dependents, "") }); err != nil {
			return ServiceView{}, err
		}
	}
	if renameErr != nil {
		return ServiceView{}, fmt.Errorf("service renamed but its containers keep their old names until the next deploy: %w", renameErr)
	}
	return c.view(ctx, svc)
}

// renameContainers swaps the name prefix of the service's containers and,
// with aliases, sets the running ones' aliases on the project network.
func (c *Core) renameContainers(ctx context.Context, svc store.Service, oldPrefix, newPrefix string, aliases []string) error {
	cts, err := c.serviceContainers(ctx, svc)
	if err != nil {
		return err
	}
	dk := c.dockerFor(svc.ServerID)
	netName := docker.ProjectNetwork(svc.ProjectID)
	var errs []error
	for _, ct := range cts {
		if len(ct.Names) > 0 {
			if cur := strings.TrimPrefix(ct.Names[0], "/"); strings.HasPrefix(cur, oldPrefix) {
				if err := dk.RenameContainer(ctx, ct.ID, newPrefix+strings.TrimPrefix(cur, oldPrefix)); err != nil {
					errs = append(errs, err)
				}
			}
		}
		if aliases != nil && ct.State == container.StateRunning && onNetwork(ct, netName) {
			if err := dk.SetAliases(ctx, netName, ct.ID, aliases); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// renameOnNetworks moves a renamed service's alias on the networks created by
// hand (<project>-<service>) to its new name.
func (c *Core) renameOnNetworks(ctx context.Context, project store.Project, svc store.Service) error {
	if len(svc.Networks) == 0 {
		return nil
	}
	cts, err := c.serviceContainers(ctx, svc)
	if err != nil {
		return err
	}
	return c.syncNetworks(ctx, project, svc, cts, true)
}

func onNetwork(ct container.Summary, name string) bool {
	if ct.NetworkSettings == nil {
		return false
	}
	_, ok := ct.NetworkSettings.Networks[name]
	return ok
}

// renameDatabaseRefs points the {{ db.<old>.FIELD }} references of env to new.
func renameDatabaseRefs(env map[string]string, old, new string) map[string]string {
	out := make(map[string]string, len(env))
	for k, v := range env {
		out[k] = envRefRe.ReplaceAllStringFunc(v, func(m string) string {
			sub := envRefRe.FindStringSubmatch(m)
			if sub[2] != old {
				return m
			}
			return "{{ db." + new + "." + sub[3] + " }}"
		})
	}
	return out
}
