package core

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestApplyJobInput(t *testing.T) {
	ok := JobInput{Name: "nightly", Command: "echo hi", Cron: "0 3 * * *", Enabled: true}
	var j store.Job
	if err := applyJobInput(&j, ok); err != nil {
		t.Fatal(err)
	}
	if j.TimeoutSec != defaultJobTimeout {
		t.Errorf("default timeout: %d", j.TimeoutSec)
	}
	for name, in := range map[string]JobInput{
		"name":         {Name: "Nightly", Command: "x", Cron: "@daily"},
		"no command":   {Name: "x", Command: "  ", Cron: "@daily"},
		"cron":         {Name: "x", Command: "x", Cron: "every day"},
		"long timeout": {Name: "x", Command: "x", Cron: "@daily", TimeoutSec: maxJobTimeout + 1},
		"neg timeout":  {Name: "x", Command: "x", Cron: "@daily", TimeoutSec: -1},
	} {
		if err := applyJobInput(&j, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestJobs(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{DataDir: t.TempDir()})
	p, err := c.store.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	app, err := c.store.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "web", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 1})
	if err != nil {
		t.Fatal(err)
	}
	db, err := c.store.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "db", Kind: store.ServiceKindRedis, Image: "redis", Replicas: 1})
	if err != nil {
		t.Fatal(err)
	}
	in := JobInput{Name: "nightly", Command: "echo hi", Cron: "@daily", Enabled: true}
	if _, err := c.CreateJob(ctx, db.ID, in); !errors.Is(err, ErrInvalid) {
		t.Errorf("job on a database: %v", err)
	}
	j, err := c.CreateJob(ctx, app.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if j.NextRun == nil {
		t.Error("no next run")
	}
	if _, err := c.CreateJob(ctx, app.ID, in); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "named nightly") {
		t.Errorf("same name: %v", err)
	}
	in.Enabled = false
	if j, err = c.UpdateJob(ctx, j.ID, in); err != nil || j.NextRun != nil {
		t.Errorf("disabled: %v %v", j.NextRun, err)
	}
	if _, err := c.RunJob(ctx, j.ID); !errors.Is(err, ErrInvalid) {
		t.Errorf("run before a deploy: %v", err)
	}

	// One run at a time.
	c.jobs.running = map[string]jobRunning{j.ID: {projectID: p.ID, cancel: func() {}}}
	if err := c.store.SetCurrentDeployment(ctx, app.ID, "01DEPLOY"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RunJob(ctx, j.ID); !errors.Is(err, ErrBusy) {
		t.Errorf("run while running: %v", err)
	}
	if !c.jobRunningIn(p.ID) || c.jobRunningIn("other") {
		t.Error("jobRunningIn")
	}

	if err := c.DeleteJob(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	if js, err := c.ListJobs(ctx, app.ID); err != nil || len(js) != 0 {
		t.Errorf("after delete: %v %v", js, err)
	}
}
