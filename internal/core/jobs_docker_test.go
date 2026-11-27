package core

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/docker"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/sqlite"
	"github.com/kipit-labs/kipitiny/internal/usecase/deploy"
)

// TestScheduledJobsDocker runs jobs of an app on the local Docker daemon:
// its env, the exit code, the timeout and the cleanup of the container.
//
//	KIPITINY_TEST_DOCKER=1 go test ./internal/core/ -run ScheduledJobsDocker
func TestScheduledJobsDocker(t *testing.T) {
	if os.Getenv("KIPITINY_TEST_DOCKER") == "" {
		t.Skip("KIPITINY_TEST_DOCKER not set")
	}
	ctx := context.Background()
	dk, err := docker.New()
	if err != nil {
		t.Fatal(err)
	}
	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c := New(config.Config{DataDir: t.TempDir()}, st, dk, slog.New(slog.NewTextHandler(io.Discard, nil)))

	p, err := st.CreateProject(ctx, domain.Project{Name: "kipitiny-jobtest", ServerID: domain.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dk.RemoveNetwork(context.Background(), domain.ProjectNetwork(p.ID)) })
	svc, err := st.CreateService(ctx, domain.Service{ProjectID: p.ID, Name: "app", Kind: domain.ServiceKindApp, Image: "busybox:stable",
		Replicas: 1, Env: map[string]string{"GREETING": "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := st.CreateDeployment(ctx, domain.Deployment{ServiceID: svc.ID, Status: domain.DeploymentSucceeded, Image: svc.Image, Config: svc})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetCurrentDeployment(ctx, svc.ID, dep.ID); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, command string
		timeout       int
		status        domain.OpStatus
		code          int // -1: none
		log, err      string
	}{
		{"ok", "echo $GREETING from $HOSTNAME", 60, domain.OpSucceeded, 0, "hello from", ""},
		{"fail", "echo oops >&2; exit 3", 60, domain.OpFailed, 3, "oops", "exited with code 3"},
		{"slow", "echo started; sleep 60", 2, domain.OpFailed, -1, "started", "timed out after 2s"},
	} {
		j, err := c.CreateJob(ctx, svc.ID, deploy.JobInput{Name: tc.name, Command: tc.command, Cron: "@yearly", TimeoutSec: tc.timeout})
		if err != nil {
			t.Fatal(err)
		}
		r, err := c.RunJob(ctx, j.ID)
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(time.Minute)
		for r.Status == domain.OpRunning && time.Now().Before(deadline) {
			time.Sleep(200 * time.Millisecond)
			if r, err = st.GetJobRun(ctx, r.ID); err != nil {
				t.Fatal(err)
			}
		}
		if r.Status != tc.status || r.Error != tc.err {
			t.Errorf("%s: %s %q", tc.name, r.Status, r.Error)
		}
		if (tc.code < 0) != (r.ExitCode == nil) || (r.ExitCode != nil && *r.ExitCode != tc.code) {
			t.Errorf("%s: exit code %v", tc.name, r.ExitCode)
		}
		rc, err := c.JobRunLog(ctx, j.ID, r.ID)
		if err != nil {
			t.Fatal(err)
		}
		log, _ := io.ReadAll(rc)
		rc.Close()
		if !strings.Contains(string(log), tc.log) {
			t.Errorf("%s: log %q", tc.name, log)
		}
	}
	// The run's goroutine removes the container once it recorded the run.
	time.Sleep(time.Second)
	if cts, err := dk.ListContainers(ctx, map[string]string{domain.LabelComponent: deploy.JobComponent, domain.LabelProject: p.ID}); err != nil || len(cts) > 0 {
		t.Errorf("job containers left: %d %v", len(cts), err)
	}
}
