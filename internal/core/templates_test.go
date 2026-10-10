package core

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestInstallTemplateDryRun(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})

	// A new project: planned without being created.
	res, err := c.InstallTemplate(ctx, "beszel-hub", TemplateInstall{
		NewProject: &NewProject{Name: "beszel"}, Values: map[string]string{"DOMAIN": "beszel.example.com"}, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Plan.Create, []string{"hub"}) || res.ProjectID != "" {
		t.Fatalf("plan = %+v", res)
	}
	if ps, _ := c.store.ListProjects(ctx); len(ps) != 0 {
		t.Fatal("dry run created a project")
	}

	// Defaults fill what isn't given; a published port and a DNS-only domain.
	res, err = c.InstallTemplate(ctx, "minecraft", TemplateInstall{
		NewProject: &NewProject{Name: "minecraft"}, Values: map[string]string{"EULA": "true", "DOMAIN": "mc.example.com"}, DryRun: true,
	})
	if err != nil || !slices.Equal(res.Plan.Create, []string{"server"}) {
		t.Fatalf("minecraft plan = %+v, %v", res, err)
	}
	if _, err := c.InstallTemplate(ctx, "minecraft", TemplateInstall{NewProject: &NewProject{Name: "minecraft"}, DryRun: true}); !errors.Is(err, ErrInvalid) {
		t.Errorf("EULA not accepted: %v", err)
	}

	// An existing project.
	p, err := c.store.CreateProject(ctx, store.Project{Name: "ops", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	res, err = c.InstallTemplate(ctx, "beszel-agent", TemplateInstall{ProjectID: p.ID, Values: map[string]string{"KEY": "ssh-ed25519 AAAA"}, DryRun: true})
	if err != nil || !slices.Equal(res.Plan.Create, []string{"agent"}) {
		t.Fatalf("plan = %+v, %v", res, err)
	}

	// Only adds: a service of the same name is refused, not changed.
	if _, err := c.store.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "agent", Kind: store.ServiceKindApp, Image: "nginx:1", Replicas: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.InstallTemplate(ctx, "beszel-agent", TemplateInstall{ProjectID: p.ID, Values: map[string]string{"KEY": "k"}, DryRun: true}); !errors.Is(err, ErrInvalid) {
		t.Errorf("name taken: %v", err)
	}

	for name, in := range map[string]TemplateInstall{
		"no input":       {NewProject: &NewProject{Name: "beszel"}, DryRun: true},
		"bad domain":     {NewProject: &NewProject{Name: "beszel"}, Values: map[string]string{"DOMAIN": "nope"}, DryRun: true},
		"bad name":       {NewProject: &NewProject{Name: "Beszel"}, Values: map[string]string{"DOMAIN": "beszel.example.com"}, DryRun: true},
		"both targets":   {ProjectID: p.ID, NewProject: &NewProject{Name: "beszel"}, Values: map[string]string{"DOMAIN": "beszel.example.com"}},
		"unknown server": {NewProject: &NewProject{Name: "beszel", ServerID: "01NOPE"}, Values: map[string]string{"DOMAIN": "beszel.example.com"}},
	} {
		if _, err := c.InstallTemplate(ctx, "beszel-hub", in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := c.InstallTemplate(ctx, "nope", TemplateInstall{ProjectID: p.ID}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown template: %v", err)
	}
}
