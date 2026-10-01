package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestSetCleanupValidates(t *testing.T) {
	c := newTestCore(t, config.Config{})
	ok := defaultCleanupSettings()
	tests := []struct {
		name    string
		mutate  func(*CleanupSettings)
		wantErr bool
	}{
		{"defaults", func(*CleanupSettings) {}, false},
		{"descriptor", func(s *CleanupSettings) { s.Cron = "@weekly" }, false},
		{"keep all deployments", func(s *CleanupSettings) { s.KeepDeployments = 0 }, false},
		{"keep some deployments", func(s *CleanupSettings) { s.KeepDeployments = 20 }, false},
		{"bad cron", func(s *CleanupSettings) { s.Cron = "every day" }, true},
		{"no minimum age", func(s *CleanupSettings) { s.MinAgeHours = 0 }, true},
		{"unknown image mode", func(s *CleanupSettings) { s.Images = "all" }, true},
		{"dangling volumes", func(s *CleanupSettings) { s.Volumes = CleanDangling }, true},
		{"too few deployments", func(s *CleanupSettings) { s.KeepDeployments = 3 }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := ok
			tt.mutate(&s)
			_, err := c.SetCleanup(context.Background(), s)
			if (err != nil) != tt.wantErr {
				t.Fatalf("got %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalid) {
				t.Fatalf("error should wrap ErrInvalid: %v", err)
			}
		})
	}
}

func TestCleanupSettingsPersist(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	if got := c.Cleanup(ctx).Settings; got != defaultCleanupSettings() {
		t.Fatalf("defaults: got %+v", got)
	}
	s := defaultCleanupSettings()
	s.Enabled, s.Images, s.KeepDeployments = true, CleanUnused, 15
	if _, err := c.SetCleanup(ctx, s); err != nil {
		t.Fatal(err)
	}
	if got := c.Cleanup(ctx).Settings; got != s {
		t.Fatalf("got %+v, want %+v", got, s)
	}
}

func TestTrimDeployments(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{DataDir: t.TempDir()})
	p, err := c.store.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := c.store.CreateService(ctx, store.Service{ProjectID: p.ID, ServerID: p.ServerID, Name: "web", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 1})
	if err != nil {
		t.Fatal(err)
	}
	var deps []store.Deployment // oldest first
	for i := range 6 {
		status := store.DeploymentSucceeded
		if i == 1 {
			status = store.DeploymentRunning
		}
		d, err := c.store.CreateDeployment(ctx, store.Deployment{ServiceID: svc.ID, Status: status, Image: "nginx"})
		if err != nil {
			t.Fatal(err)
		}
		f, err := c.createDeployLog(svc.ID, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
		deps = append(deps, d)
	}
	// The oldest is current, e.g. after a rollback to it.
	if err := c.store.SetCurrentDeployment(ctx, svc.ID, deps[0].ID); err != nil {
		t.Fatal(err)
	}
	svc.CurrentDeploymentID = deps[0].ID

	if n := c.trimDeployments(ctx, []store.Service{svc}, 2); n != 2 {
		t.Fatalf("trimmed %d, want 2", n)
	}
	left, err := c.store.ListDeployments(ctx, svc.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{deps[5].ID, deps[4].ID, deps[1].ID, deps[0].ID}
	if len(left) != len(want) {
		t.Fatalf("kept %d deployments, want %d", len(left), len(want))
	}
	for i, d := range left {
		if d.ID != want[i] {
			t.Fatalf("kept[%d] = %s, want %s", i, d.ID, want[i])
		}
	}
	for _, d := range deps[2:4] {
		if _, err := os.Stat(filepath.Join(c.deployLogDir(svc.ID), d.ID+".log")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("log of trimmed deployment %s still there: %v", d.ID, err)
		}
	}
}
