package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestProjectsAndServices(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	p, err := s.CreateProject(ctx, domain.Project{Name: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProject(ctx, domain.Project{Name: "shop"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate name: got %v, want ErrConflict", err)
	}

	got, err := s.GetProject(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "shop" || !got.CreatedAt.Equal(p.CreatedAt) {
		t.Fatalf("round-trip mismatch: %+v vs %+v", got, p)
	}

	ps, err := s.ListProjects(ctx)
	if err != nil || len(ps) != 1 {
		t.Fatalf("list: %v %v", ps, err)
	}

	if got, err = s.SetProjectEnv(ctx, p.ID, map[string]string{"REGION": "eu", "KEY": "k"}, []string{"KEY"}); err != nil ||
		got.Env["REGION"] != "eu" || !slices.Equal(got.Secrets, []string{"KEY"}) {
		t.Fatalf("set env: %+v %v %v", got.Env, got.Secrets, err)
	}
	if _, err := s.SetProjectEnv(ctx, "missing", nil, nil); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("set env of unknown project: got %v, want ErrNotFound", err)
	}

	svc, err := s.CreateService(ctx, domain.Service{
		ProjectID: p.ID, Name: "web", Kind: domain.ServiceKindApp, Image: "nginx", Replicas: 1,
		Port: 80, Domain: "shop.example.com", Env: map[string]string{"A": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Services may share a domain (split by path): core decides who may.
	web2, err := s.CreateService(ctx, domain.Service{
		ProjectID: p.ID, Name: "web2", Kind: domain.ServiceKindApp, Image: "nginx", Domain: "shop.example.com",
	})
	if err != nil {
		t.Fatalf("shared domain: %v", err)
	}
	if err := s.DeleteService(ctx, web2.ID); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"worker1", "worker2"} {
		if _, err := s.CreateService(ctx, domain.Service{
			ProjectID: p.ID, Name: name, Kind: domain.ServiceKindApp, Image: "busybox",
		}); err != nil {
			t.Fatal(err)
		}
	}

	if svc.Volumes == nil || len(svc.Volumes) != 0 {
		t.Fatalf("volumes default: %#v", svc.Volumes)
	}
	if svc.PublishedPorts == nil || len(svc.PublishedPorts) != 0 {
		t.Fatalf("published ports default: %#v", svc.PublishedPorts)
	}
	svc.Env["B"] = "2"
	svc.Replicas = 3
	svc.Volumes = []domain.Volume{{Name: "uploads", Path: "/app/uploads"}}
	svc.PublishedPorts = []domain.PublishedPort{{HostPort: 25565, ContainerPort: 25565, Protocol: "tcp"}}
	svc.StopGraceSeconds = 60
	svc.Icon = "ghost"
	svc.Analytics = true
	svc.Files = []domain.ServiceFile{{Path: "/etc/app.conf", Content: []byte("a=1\n"), Size: 4, Mode: 0o600, Source: "config:app"}}
	svc.DependsOn = []domain.Dependency{{Service: "db", Condition: "healthy"}}
	svc.ProjectNetworks = []string{"back", "default"}
	svc.Runtime = domain.Runtime{Command: []string{"worker", "--queue", "a b"}, Ulimits: map[string]domain.Ulimit{"nofile": {Soft: 1, Hard: 2}}}
	svc.Middlewares = domain.Middlewares{
		BasicAuth:   []domain.BasicAuthUser{{Name: "ann", Hash: "$2a$10$x"}},
		IPAllowList: []string{"10.0.0.0/8"},
		RateLimit:   &domain.RateLimit{Average: 10, Burst: 20},
		Headers:     map[string]string{"X-Robots-Tag": "noindex"},
	}
	if _, err := s.UpdateService(ctx, svc); err != nil {
		t.Fatal(err)
	}
	got2, err := s.GetService(ctx, svc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got2.Replicas != 3 || got2.Env["A"] != "1" || got2.Env["B"] != "2" || got2.Domain != "shop.example.com" ||
		!reflect.DeepEqual(got2.Volumes, svc.Volumes) || !slices.Equal(got2.PublishedPorts, svc.PublishedPorts) || got2.StopGraceSeconds != 60 || got2.Icon != "ghost" || !got2.Analytics || !reflect.DeepEqual(got2.Middlewares, svc.Middlewares) ||
		!reflect.DeepEqual(got2.Runtime, svc.Runtime) || !reflect.DeepEqual(got2.Files, svc.Files) || !reflect.DeepEqual(got2.DependsOn, svc.DependsOn) || !slices.Equal(got2.ProjectNetworks, svc.ProjectNetworks) {
		t.Fatalf("service round-trip mismatch: %+v", got2)
	}

	d, err := s.CreateDeployment(ctx, domain.Deployment{ServiceID: svc.ID, Status: domain.DeploymentRunning, Image: "nginx", Config: got2,
		GitCommit: "abc1234", TriggeredBy: "token:ci"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetDeployment(ctx, d.ID); got.Config.Env["B"] != "2" || got.Config.Replicas != 3 {
		t.Fatalf("deployment config snapshot: %+v", got.Config)
	} else if got.GitCommit != "abc1234" || got.TriggeredBy != "token:ci" {
		t.Fatalf("deployment commit/trigger: %q %q", got.GitCommit, got.TriggeredBy)
	}
	if err := s.SetServiceStopped(ctx, svc.ID, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetService(ctx, svc.ID); !got.Stopped {
		t.Fatal("stopped flag not saved")
	}
	if all, _ := s.ListAllServices(ctx); len(all) != 3 {
		t.Fatalf("all services: %d", len(all))
	}
	if n, err := s.FailRunningDeployments(ctx, "interrupted"); err != nil || n != 1 {
		t.Fatalf("fail running: n=%d err=%v", n, err)
	}
	d, err = s.GetDeployment(ctx, d.ID)
	if err != nil || d.Status != domain.DeploymentFailed || d.FinishedAt == nil || d.Error != "interrupted" {
		t.Fatalf("deployment after fail: %+v %v", d, err)
	}
	ds, err := s.ListDeployments(ctx, svc.ID, 10)
	if err != nil || len(ds) != 1 {
		t.Fatalf("list deployments: %v %v", ds, err)
	}

	if err := s.DeleteProject(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetService(ctx, svc.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("service should cascade-delete, got %v", err)
	}
	if err := s.DeleteProject(ctx, p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second delete: got %v, want ErrNotFound", err)
	}
}

func TestSnapshot(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	if _, err := s.CreateProject(ctx, domain.Project{Name: "shop"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "snap.db")
	if err := s.Snapshot(ctx, path); err != nil {
		t.Fatal(err)
	}
	snap, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	ps, err := snap.ListProjects(ctx)
	if err != nil || len(ps) != 1 || ps[0].Name != "shop" {
		t.Fatalf("snapshot content: %v %v", ps, err)
	}
}

func TestServersAndSettings(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	servers, err := s.ListServers(ctx)
	if err != nil || len(servers) != 1 || servers[0].ID != domain.LocalServerID {
		t.Fatalf("seeded local server: %+v %v", servers, err)
	}
	remote, err := s.CreateServer(ctx, domain.Server{Name: "db-2", Kind: domain.ServerSSH, Host: "10.0.0.2", Port: 22, SSHUser: "root", Socket: "/var/run/docker.sock"})
	if err != nil {
		t.Fatal(err)
	}
	remote.HostKey = "ssh-ed25519 AAAA"
	if _, err := s.UpdateServer(ctx, remote); err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(ctx, domain.Project{Name: "shop", ServerID: remote.ID})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := s.CreateService(ctx, domain.Service{ProjectID: p.ID, Name: "web", Kind: domain.ServiceKindApp, Image: "nginx"})
	if err != nil || svc.ServerID != remote.ID {
		t.Fatalf("service must inherit the project's server: %+v %v", svc, err)
	}
	if def, _ := s.CreateProject(ctx, domain.Project{Name: "other"}); def.ServerID != domain.LocalServerID {
		t.Fatalf("default server: %q", def.ServerID)
	}
	if err := s.DeleteServer(ctx, remote.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("deleting a server in use: %v", err)
	}

	if _, err := s.GetSetting(ctx, "k"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing setting: %v", err)
	}
	for _, v := range []string{"a", "b"} {
		if err := s.SetSetting(ctx, "k", v); err != nil {
			t.Fatal(err)
		}
	}
	if v, _ := s.GetSetting(ctx, "k"); v != "b" {
		t.Fatalf("setting upsert: %q", v)
	}
}

func TestDomains(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	for _, n := range []string{"example.org", "example.com"} {
		if _, err := s.CreateDomain(ctx, domain.Domain{Name: n}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateDomain(ctx, domain.Domain{Name: "example.com"}); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("duplicate: %v", err)
	}
	ds, err := s.ListDomains(ctx)
	if err != nil || len(ds) != 2 || ds[0].Name != "example.com" {
		t.Fatalf("list = %+v, %v", ds, err)
	}
	if err := s.DeleteDomain(ctx, ds[0].ID); err != nil {
		t.Fatal(err)
	}
	if ds, _ := s.ListDomains(ctx); len(ds) != 1 || ds[0].Name != "example.org" {
		t.Errorf("after delete: %+v", ds)
	}
}

func TestNotificationChannels(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	ch, err := s.CreateNotificationChannel(ctx, domain.NotificationChannel{
		Name: "ops", Kind: "discord", Config: map[string]string{"webhookUrl": "u"}, Events: []string{"deploy.failed"}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateNotificationChannel(ctx, domain.NotificationChannel{Name: "ops", Kind: "discord"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate name: got %v, want ErrConflict", err)
	}
	ch.Name, ch.Events, ch.Enabled = "alerts", []string{"backup.failed", "deploy.failed"}, false
	if _, err := s.UpdateNotificationChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetNotificationChannel(ctx, ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "alerts" || got.Enabled || got.Config["webhookUrl"] != "u" || !slices.Equal(got.Events, ch.Events) {
		t.Errorf("got %+v", got)
	}
	if _, err := s.UpdateNotificationChannel(ctx, domain.NotificationChannel{ID: "missing"}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("update missing: %v", err)
	}
	if err := s.DeleteNotificationChannel(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	if chs, _ := s.ListNotificationChannels(ctx); len(chs) != 0 {
		t.Errorf("after delete: %+v", chs)
	}
}

func TestUptime(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	p, err := s.CreateProject(ctx, domain.Project{Name: "shop", ServerID: domain.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := s.CreateService(ctx, domain.Service{ProjectID: p.ID, Name: "web", Kind: domain.ServiceKindApp, Image: "nginx", Replicas: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetUptimeCheck(ctx, svc.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("before: %v", err)
	}
	c, err := s.SaveUptimeCheck(ctx, domain.UptimeCheck{ServiceID: svc.ID, Path: "/", IntervalSec: 60, TimeoutSec: 10, Enabled: true})
	if err != nil || c.Down || c.ChangedAt != nil {
		t.Fatalf("create: %+v %v", c, err)
	}
	since := time.Now()
	if err := s.SetUptimeState(ctx, svc.ID, true, since); err != nil {
		t.Fatal(err)
	}
	// Saving the settings again keeps the state.
	c, err = s.SaveUptimeCheck(ctx, domain.UptimeCheck{ServiceID: svc.ID, Path: "/health", IntervalSec: 30, TimeoutSec: 5, ExpectedStatus: 204})
	if err != nil || c.Path != "/health" || c.IntervalSec != 30 || c.ExpectedStatus != 204 || c.Enabled || !c.Down || c.ChangedAt == nil {
		t.Fatalf("update: %+v %v", c, err)
	}
	if cs, err := s.ListUptimeChecks(ctx); err != nil || len(cs) != 1 || cs[0].Recent == nil {
		t.Fatalf("list: %v %v", cs, err)
	}
	recent := []domain.UptimeResult{{At: time.Now().UTC().Truncate(time.Second), Status: 503, Error: "status 503"}}
	if err := s.SetUptimeRecent(ctx, svc.ID, recent); err != nil {
		t.Fatal(err)
	}
	// Saving the settings again keeps the results.
	if c, err := s.SaveUptimeCheck(ctx, domain.UptimeCheck{ServiceID: svc.ID, Path: "/", IntervalSec: 60, TimeoutSec: 10}); err != nil || !reflect.DeepEqual(c.Recent, recent) {
		t.Fatalf("recent: %+v %v", c.Recent, err)
	}

	h0 := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	for _, r := range []struct {
		at time.Time
		ok bool
		ms int
	}{{h0.Add(time.Minute), true, 100}, {h0.Add(2 * time.Minute), false, 0}, {h0.Add(3 * time.Minute), true, 50}, {h0.Add(time.Hour), true, 10}} {
		if err := s.AddUptimeResult(ctx, svc.ID, r.at, r.ok, time.Duration(r.ms)*time.Millisecond); err != nil {
			t.Fatal(err)
		}
	}
	hs, err := s.ListUptimeHours(ctx, svc.ID, h0.Add(30*time.Minute))
	if err != nil || len(hs) != 2 {
		t.Fatalf("hours: %+v %v", hs, err)
	}
	if h := hs[0]; !h.Hour.Equal(h0) || h.Checks != 3 || h.Failures != 1 || h.LatencyMS != 150 {
		t.Errorf("first hour: %+v", h)
	}
	if err := s.PruneUptime(ctx, h0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if hs, _ := s.ListUptimeHours(ctx, svc.ID, h0); len(hs) != 1 || hs[0].Checks != 1 {
		t.Errorf("after prune: %+v", hs)
	}

	if err := s.DeleteUptimeCheck(ctx, svc.ID); err != nil {
		t.Fatal(err)
	}
	if hs, _ := s.ListUptimeHours(ctx, svc.ID, h0); len(hs) != 0 {
		t.Errorf("results kept after delete: %+v", hs)
	}
	if err := s.DeleteUptimeCheck(ctx, svc.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("delete again: %v", err)
	}
}

func TestProjectGit(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	p, err := s.CreateProject(ctx, domain.Project{Name: "shop", ServerID: domain.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := s.CreateService(ctx, domain.Service{ProjectID: p.ID, Name: "web", Kind: domain.ServiceKindApp, Image: "nginx", Replicas: 1})
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.SaveProjectGit(ctx, domain.ProjectGit{ProjectID: p.ID, RepoURL: "https://example.com/r.git", Branch: "main", Path: "compose.yaml",
		AutoSync: true, PollSeconds: 300, WebhookSecret: "w"})
	if err != nil || g.Branch != "main" || g.Warnings == nil || g.Applied == nil {
		t.Fatalf("create: %+v %v", g, err)
	}
	synced := time.Now()
	g.LastCommit, g.LastSyncedAt, g.Warnings, g.Applied = "abc", &synced, []string{"w1"}, map[string]string{"web": "nginx:1"}
	if err := s.SetProjectGitSync(ctx, g); err != nil {
		t.Fatal(err)
	}
	// Saving the link again keeps the sync state and the webhook secret.
	g, err = s.SaveProjectGit(ctx, domain.ProjectGit{ProjectID: p.ID, RepoURL: "https://example.com/r.git", Branch: "prod", Path: "c.yml", WebhookSecret: "new"})
	if err != nil || g.Branch != "prod" || g.LastCommit != "abc" || g.WebhookSecret != "w" || g.Applied["web"] != "nginx:1" || g.LastSyncedAt == nil {
		t.Fatalf("update: %+v %v", g, err)
	}
	if err := s.SetServiceGitState(ctx, svc.ID, "h", true); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetService(ctx, svc.ID); got.GitSpecHash != "h" || !got.Orphaned {
		t.Fatalf("service = %+v", got)
	}
	if gs, err := s.ListProjectGit(ctx); err != nil || len(gs) != 1 {
		t.Fatalf("list: %v %v", gs, err)
	}
	if err := s.DeleteProjectGit(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetService(ctx, svc.ID); got.GitSpecHash != "" || got.Orphaned {
		t.Fatalf("unlinked service = %+v", got)
	}
	if _, err := s.GetProjectGit(ctx, p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}

func TestNetworks(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	n, err := s.CreateNetwork(ctx, domain.Network{ServerID: domain.LocalServerID, Name: "shared"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateNetwork(ctx, domain.Network{ServerID: domain.LocalServerID, Name: "shared"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate name on a server: got %v, want ErrConflict", err)
	}

	p, err := s.CreateProject(ctx, domain.Project{Name: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := s.CreateService(ctx, domain.Service{ProjectID: p.ID, Name: "web", Kind: domain.ServiceKindApp, Image: "nginx", Replicas: 1})
	if err != nil {
		t.Fatal(err)
	}
	if svc.Networks == nil || len(svc.Networks) != 0 {
		t.Fatalf("new service networks = %#v, want empty", svc.Networks)
	}
	if err := s.SetServiceNetworks(ctx, svc.ID, []string{n.ID}); err != nil {
		t.Fatal(err)
	}
	// A settings update (e.g. a git sync) keeps them.
	svc.Image = "nginx:1.27"
	if _, err := s.UpdateService(ctx, svc); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetService(ctx, svc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Networks) != 1 || got.Networks[0] != n.ID {
		t.Fatalf("networks = %v", got.Networks)
	}
	if err := s.SetServiceNetworks(ctx, "nope", nil); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown service: got %v", err)
	}

	ns, err := s.ListNetworks(ctx)
	if err != nil || len(ns) != 1 || ns[0].Name != "shared" {
		t.Fatalf("list = %v, %v", ns, err)
	}
	if err := s.DeleteNetwork(ctx, n.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetNetwork(ctx, n.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted network: got %v", err)
	}
}
