package core

// The flows a running server must never lose, on a real Docker daemon:
// deploying, rolling out and back, the reconciler after a restart, compose,
// and a database's backup, verify and restore.
//
//	KIPITINY_TEST_DOCKER=1 go test ./internal/core/ -run FlowDocker
//
// These deploy through core, so their containers carry kipitiny.managed: on
// a daemon that runs a manager, each reconciler would remove the other's.
// They refuse such a daemon; point DOCKER_HOST at a throwaway one (CI's is).

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/store"
	"github.com/kipit-labs/kipitiny/internal/store/sqlite"
)

type flow struct {
	c      *Core
	ctx    context.Context
	dk     *docker.Client
	dbPath string
	cfg    config.Config
}

// newFlow is a core on a fresh database and the daemon, as a manager runs.
func newFlow(t *testing.T) flow {
	t.Helper()
	if os.Getenv("KIPITINY_TEST_DOCKER") == "" {
		t.Skip("KIPITINY_TEST_DOCKER not set")
	}
	ctx := context.Background()
	dk, err := docker.New()
	if err != nil {
		t.Fatal(err)
	}
	if cts, err := dk.ListContainers(ctx, map[string]string{docker.LabelManaged: "true"}); err != nil {
		t.Fatal(err)
	} else if len(cts) > 0 {
		msg := "this Docker daemon runs kipitiny containers, which these tests' reconciler would remove: point DOCKER_HOST at a throwaway daemon"
		if os.Getenv("CI") != "" {
			t.Fatal(msg)
		}
		t.Skip(msg)
	}
	if err := dk.EnsureNetwork(ctx, docker.ProxyNetwork); err != nil {
		t.Fatal(err)
	}
	f := flow{dk: dk, dbPath: filepath.Join(t.TempDir(), "t.db"), cfg: config.Config{DataDir: t.TempDir()}}
	f.c = f.core(t)
	f.ctx = WithActor(ctx, Actor{Kind: "user", Name: "test", Role: store.RoleAdmin})
	return f
}

// core opens a manager on the flow's database: a second call is the same
// server after a restart.
func (f flow) core(t *testing.T) *Core {
	t.Helper()
	st, err := sqlite.Open(context.Background(), f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	c := New(f.cfg, st, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.pool = docker.NewPool(f.dk, c.connectServer)
	// The injected health probe is the test binary, which can't probe: apps
	// are ready on their own healthcheck, or once up for a few seconds.
	c.probes.Store(store.LocalServerID, (*mount.Mount)(nil))
	t.Cleanup(func() { c.cancel(); st.Close() })
	return c
}

// project creates a project removed with its containers after the test.
func (f flow) project(t *testing.T, name string) store.Project {
	t.Helper()
	p, err := f.c.CreateProject(f.ctx, name, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.c.DeleteProject(f.ctx, p.ID, p.Name) })
	return p
}

func (f flow) service(t *testing.T, projectID string, in ServiceInput) store.Service {
	t.Helper()
	if in.Replicas == 0 {
		in.Replicas = 1
	}
	v, err := f.c.CreateService(f.ctx, projectID, in)
	if err != nil {
		t.Fatal(err)
	}
	return v.Service
}

// deployed waits for deployment id to finish and returns it.
func (f flow) deployed(t *testing.T, id string) store.Deployment {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Minute); ; time.Sleep(time.Second) {
		d, err := f.c.store.GetDeployment(f.ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if d.Status != store.DeploymentRunning {
			return d
		}
		if time.Now().After(deadline) {
			t.Fatalf("deployment %s still running after 5 minutes", id)
		}
	}
}

// ok waits for a deployment Deploy or Rollback started, and fails with its
// log unless it succeeded: f.ok(t)(f.c.Deploy(...)).
func (f flow) ok(t *testing.T) func(store.Deployment, error) store.Deployment {
	return func(d store.Deployment, err error) store.Deployment {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		if d = f.deployed(t, d.ID); d.Status != store.DeploymentSucceeded {
			f.log(t, d.ID)
			t.Fatalf("deployment %s: %s", d.ID, d.Status)
		}
		return d
	}
}

func (f flow) log(t *testing.T, deploymentID string) {
	t.Helper()
	if rc, err := f.c.DeploymentLog(f.ctx, deploymentID); err == nil {
		b, _ := io.ReadAll(rc)
		rc.Close()
		t.Logf("deploy log:\n%s", b)
	}
}

// running lists the deployments of svc's running containers.
func (f flow) running(t *testing.T, svc store.Service) []string {
	t.Helper()
	cts, err := f.c.serviceContainers(f.ctx, svc)
	if err != nil {
		t.Fatal(err)
	}
	var deps []string
	for _, ct := range cts {
		if ct.State == "running" {
			deps = append(deps, ct.Labels[docker.LabelDeploy])
		}
	}
	return deps
}

func (f flow) current(t *testing.T, svc store.Service) string {
	t.Helper()
	s, err := f.c.store.GetService(f.ctx, svc.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s.CurrentDeploymentID
}

// Deploy an image, roll a new version out, see a broken one leave the
// running version alone, then roll back.
func TestDeployRolloutRollbackFlowDocker(t *testing.T) {
	f := newFlow(t)
	p := f.project(t, "shop")
	web := f.service(t, p.ID, ServiceInput{Name: "web", Kind: store.ServiceKindApp, Image: "nginx:1.29-alpine", Port: 80})

	first := f.ok(t)(f.c.Deploy(f.ctx, web.ID, DeployOptions{}))
	if got := f.running(t, web); len(got) != 1 || got[0] != first.ID || f.current(t, web) != first.ID {
		t.Fatalf("after the first deploy: running %v, current %s, want %s", got, f.current(t, web), first.ID)
	}

	second := f.ok(t)(f.c.Deploy(f.ctx, web.ID, DeployOptions{Tag: "1.28-alpine"}))
	if got := f.running(t, web); len(got) != 1 || got[0] != second.ID || f.current(t, web) != second.ID {
		t.Fatalf("after the rollout: running %v, current %s, want %s", got, f.current(t, web), second.ID)
	}
	if !strings.Contains(second.Image, "1.28-alpine") {
		t.Errorf("rolled out image %q", second.Image)
	}

	// A tag that doesn't exist never deploys: the running version stays.
	broken, err := f.c.Deploy(f.ctx, web.ID, DeployOptions{Tag: "x"})
	if err == nil {
		if broken = f.deployed(t, broken.ID); broken.Status == store.DeploymentSucceeded {
			t.Fatalf("a missing tag deployed")
		}
	}
	if got := f.running(t, web); len(got) != 1 || got[0] != second.ID || f.current(t, web) != second.ID {
		t.Fatalf("after a failed deploy: running %v, current %s, want %s still", got, f.current(t, web), second.ID)
	}

	back := f.ok(t)(f.c.Rollback(f.ctx, web.ID, ""))
	if got := f.running(t, web); len(got) != 1 || got[0] != back.ID || f.current(t, web) != back.ID {
		t.Fatalf("after the rollback: running %v, current %s, want %s", got, f.current(t, web), back.ID)
	}
	if back.Image != first.Image {
		t.Errorf("rolled back to %q, want the first deploy's %q", back.Image, first.Image)
	}
}

// After a restart the reconciler brings back what went missing, keeps what
// runs, and removes managed containers of no service.
func TestReconcileAfterRestartFlowDocker(t *testing.T) {
	f := newFlow(t)
	p := f.project(t, "shop")
	web := f.service(t, p.ID, ServiceInput{Name: "web", Kind: store.ServiceKindApp, Image: "nginx:1.29-alpine", Port: 80, Replicas: 2})
	dep := f.ok(t)(f.c.Deploy(f.ctx, web.ID, DeployOptions{}))
	cts, err := f.c.serviceContainers(f.ctx, web)
	if err != nil || len(cts) != 2 {
		t.Fatalf("replicas: %d, %v", len(cts), err)
	}

	// While the manager is down: a replica is lost, an unknown managed
	// container shows up.
	f.c.cancel()
	if err := f.dk.RemoveContainer(f.ctx, cts[0].ID, 0); err != nil {
		t.Fatal(err)
	}
	if err := f.dk.EnsureImage(f.ctx, "busybox", "", ""); err != nil {
		t.Fatal(err)
	}
	stray, err := f.dk.Run(f.ctx, client.ContainerCreateOptions{Name: "kipitiny-test-stray", Config: &container.Config{
		Image: "busybox", Cmd: []string{"sleep", "600"},
		Labels: map[string]string{docker.LabelManaged: "true", docker.LabelProject: p.ID, docker.LabelService: "01STRAY0000000000000000000"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.dk.RemoveContainer(context.Background(), stray, 0) })

	restarted := f.core(t)
	if err := restarted.reconcile(f.ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.running(t, web); len(got) != 2 || got[0] != dep.ID || got[1] != dep.ID {
		t.Errorf("replicas after the restart: %v, want 2 of %s", got, dep.ID)
	}
	if left, _ := f.dk.ListContainers(f.ctx, map[string]string{docker.LabelService: "01STRAY0000000000000000000"}); len(left) != 0 {
		t.Errorf("orphan kept: %d", len(left))
	}
	// A second pass changes nothing.
	before, _ := f.c.serviceContainers(f.ctx, web)
	if err := restarted.reconcile(f.ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := f.c.serviceContainers(f.ctx, web)
	if containerIDs(before) != containerIDs(after) {
		t.Errorf("a second reconcile touched running replicas: %s, then %s", containerIDs(before), containerIDs(after))
	}
}

func containerIDs(cts []container.Summary) string {
	var b bytes.Buffer
	for _, c := range cts {
		b.WriteString(c.ID[:12] + " ")
	}
	return b.String()
}

// A compose file deploys its database before the app that depends on it.
func TestComposeFlowDocker(t *testing.T) {
	f := newFlow(t)
	p := f.project(t, "blog")
	compose := `services:
  db:
    image: postgres:17-alpine
    environment:
      POSTGRES_PASSWORD: hunter2
  web:
    image: nginx:1.29-alpine
    depends_on: [db]
`
	plan, err := f.c.ApplyCompose(f.ctx, p.ID, []byte(compose), ApplyOptions{Deploy: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Create) != 2 {
		t.Fatalf("plan = %+v", plan)
	}
	svcs, err := f.c.store.ListServices(f.ctx, p.ID)
	if err != nil || len(svcs) != 2 {
		t.Fatalf("services: %d, %v", len(svcs), err)
	}
	ended := map[string]time.Time{}
	for _, s := range svcs {
		var deps []store.Deployment
		for deadline := time.Now().Add(5 * time.Minute); len(deps) == 0; time.Sleep(time.Second) {
			if deps, err = f.c.store.ListDeployments(f.ctx, s.ID, 1); err != nil {
				t.Fatal(err)
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s never deployed", s.Name)
			}
		}
		d := f.ok(t)(deps[0], nil)
		ended[s.Name] = d.CreatedAt
		if got := f.running(t, s); len(got) != 1 {
			t.Errorf("%s running %v", s.Name, got)
		}
	}
	if !ended["db"].Before(ended["web"]) {
		t.Errorf("web deployed before its database: %v", ended)
	}
}

// A database deployed by core is backed up, verified, and restored after
// its data changed.
func TestDatabaseBackupFlowDocker(t *testing.T) {
	f := newFlow(t)
	p := f.project(t, "shop")
	db := f.service(t, p.ID, ServiceInput{Name: "db", Kind: store.ServiceKindPostgres, Image: DefaultPostgresImage})
	f.ok(t)(f.c.Deploy(f.ctx, db.ID, DeployOptions{}))
	db, _ = f.c.store.GetService(f.ctx, db.ID)
	psql := func(sql string) string {
		t.Helper()
		cts, err := f.c.serviceContainers(f.ctx, db)
		if err != nil || len(cts) != 1 {
			t.Fatalf("db containers: %d, %v", len(cts), err)
		}
		var out bytes.Buffer
		cmd := []string{"psql", "-U", db.Env[pgUser], "-d", db.Env[pgDatabase], "-tAc", sql}
		if err := f.dk.Exec(f.ctx, cts[0].ID, docker.ExecOptions{Cmd: cmd, Stdout: &out}); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return strings.TrimSpace(out.String())
	}
	psql("CREATE TABLE orders (id int); INSERT INTO orders VALUES (1), (2)")

	b, err := f.c.BackupService(f.ctx, db.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	b = waitBackup(t, f.c, b.ID, func(b store.Backup) bool { return b.Status != store.OpRunning })
	if b.Status != store.OpSucceeded {
		t.Fatalf("backup: %+v", b)
	}
	if _, err := f.c.VerifyBackup(f.ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if b = waitBackup(t, f.c, b.ID, func(b store.Backup) bool { return b.VerifyStatus != "" && b.VerifyStatus != store.OpRunning }); b.VerifyStatus != store.OpSucceeded {
		t.Fatalf("verify: %+v", b)
	}

	psql("DELETE FROM orders")
	if _, err := f.c.RestoreBackup(f.ctx, b.ID, db.ID, "nope"); err == nil {
		t.Error("restore without the service's name as confirmation")
	}
	r, err := f.c.RestoreBackup(f.ctx, b.ID, db.ID, db.Name)
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(2 * time.Minute); r.Status == store.OpRunning; time.Sleep(time.Second) {
		rs, err := f.c.ListRestores(f.ctx, db.ID)
		if err != nil || len(rs) == 0 {
			t.Fatalf("restores: %v", err)
		}
		r = rs[0]
		if time.Now().After(deadline) {
			t.Fatal("restore still running")
		}
	}
	if r.Status != store.OpSucceeded {
		t.Fatalf("restore: %+v", r)
	}
	if got := psql("SELECT count(*) FROM orders"); got != "2" {
		t.Errorf("rows after the restore: %s", got)
	}
}
