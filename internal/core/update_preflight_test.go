package core

import (
	"context"
	"slices"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestRunningTasks(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	p, err := c.store.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := c.store.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "web", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 1})
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
	c.steps.set(verifyKey("B1"), VerifyRestore)
	c.cleaning.Store(true)
	if got, want := c.runningTasks(ctx), []string{"shop/web", "a restore test", "the cleanup"}; !slices.Equal(got, want) {
		t.Errorf("busy: %q, want %q", got, want)
	}

	unlock()
	c.steps.done(verifyKey("B1"))
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
