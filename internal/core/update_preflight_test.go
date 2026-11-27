package core

import (
	"context"
	"slices"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/data"
)

func TestRunningTasks(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	p, err := c.store.CreateProject(ctx, domain.Project{Name: "shop", ServerID: domain.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := c.store.CreateService(ctx, domain.Service{ProjectID: p.ID, Name: "web", Kind: domain.ServiceKindApp, Image: "nginx", Replicas: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.runningTasks(ctx); len(got) != 0 {
		t.Fatalf("idle: %q", got)
	}

	unlock, err := c.lockService(svc.ID)
	if err != nil {
		t.Fatal(err)
	}
	c.steps.Set("verify/B1", data.VerifyRestore)
	c.cleaning.Store(true)
	if got, want := c.runningTasks(ctx), []string{"shop/web", "a restore test", "the cleanup"}; !slices.Equal(got, want) {
		t.Errorf("busy: %q, want %q", got, want)
	}

	unlock()
	c.steps.Done("verify/B1")
	c.cleaning.Store(false)
	// Other operations only show as a count.
	c.running.Add(2)
	if got, want := c.runningTasks(ctx), []string{"2 background tasks"}; !slices.Equal(got, want) {
		t.Errorf("background: %q, want %q", got, want)
	}
	c.running.Add(-2)
	if got := c.runningTasks(ctx); len(got) != 0 {
		t.Errorf("done: %q", got)
	}
}
