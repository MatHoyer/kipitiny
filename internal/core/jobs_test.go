package core

import (
	"context"
	"errors"
	"slices"
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

func TestComposeJobs(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{DataDir: t.TempDir()})
	p, err := c.store.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	src := `
services:
  web:
    image: ghcr.io/me/shop:1
    x-kipitiny:
      jobs:
        purge: {command: echo $HOME, schedule: "*/15 * * * *"}
        report: {command: ./report, schedule: "@daily", timeout: 10m, paused: true}
`
	if _, err := c.ApplyCompose(ctx, p.ID, []byte(src), ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	svcs, _ := c.store.ListServices(ctx, p.ID)
	js, _ := c.store.ListJobs(ctx, svcs[0].ID)
	if len(js) != 2 || js[0].Name != "purge" || js[0].Command != "echo $HOME" || js[0].TimeoutSec != defaultJobTimeout || !js[0].Enabled ||
		js[1].TimeoutSec != 600 || js[1].Enabled {
		t.Fatalf("jobs = %+v", js)
	}

	// The export applies as is.
	out, err := c.ExportCompose(ctx, p.ID, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := c.ApplyCompose(ctx, p.ID, out.Compose, ApplyOptions{DryRun: true})
	if err != nil || len(plan.Update) != 0 {
		t.Fatalf("round trip: %+v %v\n%s", plan, err, out.Compose)
	}

	changed := strings.Replace(src, "./report", "./report --all", 1)
	changed = strings.Replace(changed, "        purge: {command: echo $HOME, schedule: \"*/15 * * * *\"}\n", "", 1)
	plan, err = c.ApplyCompose(ctx, p.ID, []byte(changed), ApplyOptions{})
	if err != nil || len(plan.Update) != 1 || !slices.Equal(plan.Update[0].Fields, []string{"jobs"}) {
		t.Fatalf("change: %+v %v", plan, err)
	}
	after, _ := c.store.ListJobs(ctx, svcs[0].ID)
	if len(after) != 1 || after[0].ID != js[1].ID || after[0].Command != "./report --all" {
		t.Errorf("after change: %+v", after)
	}

	// A git project's file owns them.
	if _, err := c.store.SaveProjectGit(ctx, store.ProjectGit{ProjectID: p.ID, RepoURL: "https://example.com/r.git", Branch: "main",
		Path: "compose.yaml"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateJob(ctx, svcs[0].ID, JobInput{Name: "x", Command: "x", Cron: "@daily"}); !errors.Is(err, ErrGitManaged) {
		t.Errorf("create in a git project: %v", err)
	}
	if err := c.DeleteJob(ctx, after[0].ID); !errors.Is(err, ErrGitManaged) {
		t.Errorf("delete in a git project: %v", err)
	}

	bad := `
services:
  cache:
    image: redis:7
    x-kipitiny: {jobs: {x: {command: x, schedule: "@daily"}}}
`
	if _, err := c.ApplyCompose(ctx, p.ID, []byte(bad), ApplyOptions{DryRun: true}); !errors.Is(err, ErrInvalid) {
		t.Errorf("job on a database: %v", err)
	}
}
