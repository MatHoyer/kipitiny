package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"

	"github.com/MatHoyer/kipitiny/internal/ids"
	"github.com/MatHoyer/kipitiny/internal/store"
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

	p, err := s.CreateProject(ctx, store.Project{Name: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProject(ctx, store.Project{Name: "shop"}); !errors.Is(err, store.ErrConflict) {
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
	if _, err := s.SetProjectEnv(ctx, "missing", nil, nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("set env of unknown project: got %v, want ErrNotFound", err)
	}

	svc, err := s.CreateService(ctx, store.Service{
		ProjectID: p.ID, Name: "web", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 1,
		Port: 80, Domain: "shop.example.com", Env: map[string]string{"A": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateService(ctx, store.Service{
		ProjectID: p.ID, Name: "web2", Kind: store.ServiceKindApp, Image: "nginx", Domain: "shop.example.com",
	}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate domain: got %v, want ErrConflict", err)
	}
	// Empty domains don't collide.
	for _, name := range []string{"worker1", "worker2"} {
		if _, err := s.CreateService(ctx, store.Service{
			ProjectID: p.ID, Name: name, Kind: store.ServiceKindApp, Image: "busybox",
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
	svc.Volumes = []store.Volume{{Name: "uploads", Path: "/app/uploads"}}
	svc.PublishedPorts = []store.PublishedPort{{HostPort: 25565, ContainerPort: 25565, Protocol: "tcp"}}
	svc.StopGraceSeconds = 60
	svc.Icon = "ghost"
	svc.Middlewares = store.Middlewares{
		BasicAuth:   []store.BasicAuthUser{{Name: "ann", Hash: "$2a$10$x"}},
		IPAllowList: []string{"10.0.0.0/8"},
		RateLimit:   &store.RateLimit{Average: 10, Burst: 20},
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
		!slices.Equal(got2.Volumes, svc.Volumes) || !slices.Equal(got2.PublishedPorts, svc.PublishedPorts) || got2.StopGraceSeconds != 60 || got2.Icon != "ghost" || !reflect.DeepEqual(got2.Middlewares, svc.Middlewares) {
		t.Fatalf("service round-trip mismatch: %+v", got2)
	}

	d, err := s.CreateDeployment(ctx, store.Deployment{ServiceID: svc.ID, Status: store.DeploymentRunning, Image: "nginx", Config: got2,
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
	if err != nil || d.Status != store.DeploymentFailed || d.FinishedAt == nil || d.Error != "interrupted" {
		t.Fatalf("deployment after fail: %+v %v", d, err)
	}
	ds, err := s.ListDeployments(ctx, svc.ID, 10)
	if err != nil || len(ds) != 1 {
		t.Fatalf("list deployments: %v %v", ds, err)
	}

	if err := s.DeleteProject(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetService(ctx, svc.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("service should cascade-delete, got %v", err)
	}
	if err := s.DeleteProject(ctx, p.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete: got %v, want ErrNotFound", err)
	}
}

func TestUsersAndSessions(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	if n, _ := s.CountUsers(ctx); n != 0 {
		t.Fatalf("users = %d", n)
	}
	u, err := s.CreateUser(ctx, store.User{Username: "admin", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(ctx, store.User{Username: "admin", PasswordHash: "y"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate user: %v", err)
	}
	if got, err := s.GetUserByUsername(ctx, "admin"); err != nil || got.ID != u.ID {
		t.Fatalf("by username: %+v %v", got, err)
	}

	future := time.Now().Add(time.Hour)
	if err := s.CreateSession(ctx, store.Session{TokenHash: "live", UserID: u.ID, ExpiresAt: future}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(ctx, store.Session{TokenHash: "dead", UserID: u.ID, ExpiresAt: time.Now().Add(-time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSession(ctx, "live"); err != nil {
		t.Fatalf("live session: %v", err)
	}
	if _, err := s.GetSession(ctx, "dead"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired session should be not found, got %v", err)
	}
	if err := s.SetPassword(ctx, u.ID, "new-hash"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSession(ctx, "live"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("password change must revoke sessions, got %v", err)
	}
	if err := s.CreateSession(ctx, store.Session{TokenHash: "live", UserID: u.ID, ExpiresAt: future}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteExpiredSessions(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSession(ctx, "live"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSession(ctx, "live"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted session: %v", err)
	}
}

func TestBackups(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	targets, err := s.ListBackupTargets(ctx)
	if err != nil || len(targets) != 1 || targets[0].ID != store.LocalTargetID {
		t.Fatalf("seeded local target: %+v %v", targets, err)
	}
	s3, err := s.CreateBackupTarget(ctx, store.BackupTarget{
		Name: "offsite", Kind: store.BackupTargetS3, Endpoint: "s3.example.com", Bucket: "b", SecretKey: "k", UseSSL: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	s3.Prefix = "pg/"
	if _, err := s.UpdateBackupTarget(ctx, s3); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetBackupTarget(ctx, s3.ID); got.Prefix != "pg/" || !got.UseSSL {
		t.Fatalf("target round-trip: %+v", got)
	}

	b, err := s.CreateBackup(ctx, store.Backup{
		ServiceID: "SVC", ProjectID: "P", ServiceName: "db", ProjectName: "shop",
		TargetID: s3.ID, ObjectKey: "shop/db/x.dump", Status: store.OpRunning,
	})
	if err != nil {
		t.Fatal(err)
	}
	vb, err := s.CreateBackup(ctx, store.Backup{
		Kind: store.BackupKindVolume, ServiceID: "SVC2", ProjectID: "P", ServiceName: "web", ProjectName: "shop",
		ServiceKind: store.ServiceKindRedis, ServiceIcon: "redis", TargetID: store.LocalTargetID, ObjectKey: "shop/web/x.tar.gz", Status: store.OpRunning, Volumes: []string{"uploads", "cache"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetBackup(ctx, vb.ID); err != nil || got.Kind != store.BackupKindVolume || got.ServiceKind != store.ServiceKindRedis || got.ServiceIcon != "redis" || !slices.Equal(got.Volumes, vb.Volumes) {
		t.Fatalf("volume backup round-trip: %+v %v", got, err)
	}
	if err := s.DeleteBackup(ctx, vb.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBackupTarget(ctx, s3.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("deleting a target in use: %v", err)
	}

	b.Status, b.SizeBytes, b.SHA256, b.PGVersion, b.DurationMS, b.Encrypted = store.OpSucceeded, 1234, "abc", "17.2", 99, true
	if err := s.FinishBackup(ctx, b); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetBackup(ctx, b.ID)
	if err != nil || got.SizeBytes != 1234 || got.PGVersion != "17.2" || got.FinishedAt == nil || !got.Encrypted {
		t.Fatalf("backup round-trip: %+v %v", got, err)
	}

	at := time.Now()
	v := store.Verification{VerifyStatus: store.OpSucceeded, VerifiedAt: &at,
		VerifyDetails: store.VerificationDetails{Tables: 3, Rows: 42}}
	if err := s.SetBackupVerification(ctx, b.ID, v); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetBackup(ctx, b.ID); got.VerifyStatus != store.OpSucceeded || got.VerifyDetails.Rows != 42 || got.VerifiedAt == nil || got.SizeBytes != 1234 {
		t.Fatalf("verification round-trip: %+v", got)
	}

	running, _ := s.CreateBackup(ctx, store.Backup{ServiceID: "SVC", TargetID: "local", Status: store.OpRunning})
	if err := s.FailRunningOperations(ctx, "interrupted"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetBackup(ctx, running.ID); got.Status != store.OpFailed {
		t.Fatalf("running backup not failed: %+v", got)
	}
	ok, err := s.ListBackups(ctx, store.BackupFilter{ServiceID: "SVC", Status: store.OpSucceeded})
	if err != nil || len(ok) != 1 {
		t.Fatalf("filtered list: %v %v", ok, err)
	}

	if err := s.DeleteBackup(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBackup(ctx, running.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBackupTarget(ctx, s3.ID); err != nil {
		t.Fatalf("delete unused target: %v", err)
	}
}

func TestBackupSchedules(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	p, _ := s.CreateProject(ctx, store.Project{Name: "shop"})
	db, err := s.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "db", Kind: store.ServiceKindPostgres, Image: "postgres:17"})
	if err != nil {
		t.Fatal(err)
	}
	sc, err := s.CreateBackupSchedule(ctx, store.BackupSchedule{
		ServiceID: db.ID, TargetID: store.LocalTargetID, Cron: "@daily", KeepDaily: 7, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	sc.Enabled, sc.KeepWeekly = false, 4
	if _, err := s.UpdateBackupSchedule(ctx, sc); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetBackupSchedule(ctx, sc.ID)
	if err != nil || got.Enabled || got.KeepWeekly != 4 || got.KeepDaily != 7 {
		t.Fatalf("schedule round-trip: %+v %v", got, err)
	}
	if _, err := s.CreateBackup(ctx, store.Backup{ServiceID: db.ID, TargetID: "local", ScheduleID: sc.ID, Status: store.OpSucceeded}); err != nil {
		t.Fatal(err)
	}
	if bs, _ := s.ListBackups(ctx, store.BackupFilter{ScheduleID: sc.ID}); len(bs) != 1 {
		t.Fatalf("filter by schedule: %v", bs)
	}
	// Deleting the database removes its schedules (backups stay).
	if err := s.DeleteService(ctx, db.ID); err != nil {
		t.Fatal(err)
	}
	if all, _ := s.ListBackupSchedules(ctx, ""); len(all) != 0 {
		t.Fatalf("schedules not cascaded: %v", all)
	}
	if bs, _ := s.ListBackups(ctx, store.BackupFilter{}); len(bs) != 1 {
		t.Fatalf("backups must outlive the database: %v", bs)
	}
}

func TestManagerSchedules(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	sc, err := s.CreateBackupSchedule(ctx, store.BackupSchedule{
		Kind: store.BackupKindManager, TargetID: store.LocalTargetID, Cron: "@daily", KeepLast: 14, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetBackupSchedule(ctx, sc.ID)
	if err != nil || got.Kind != store.BackupKindManager || got.ServiceID != "" {
		t.Fatalf("manager schedule round-trip: %+v %v", got, err)
	}
	// A manager schedule has no service, a database schedule needs one.
	if _, err := s.CreateBackupSchedule(ctx, store.BackupSchedule{TargetID: store.LocalTargetID, Cron: "@daily"}); err == nil {
		t.Fatal("database schedule without a service accepted")
	}
}

func TestSnapshot(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	if _, err := s.CreateProject(ctx, store.Project{Name: "shop"}); err != nil {
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

func TestTokensAndAudit(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	tok, err := s.CreateAPIToken(ctx, store.APIToken{Name: "ci", TokenHash: "h1", Scope: store.ScopeDeploy})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAPITokenByHash(ctx, "h1")
	if err != nil || got.ID != tok.ID || got.LastUsedAt == nil {
		t.Fatalf("by hash: %+v %v", got, err)
	}
	if _, err := s.GetAPITokenByHash(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown token: %v", err)
	}
	if err := s.DeleteAPIToken(ctx, tok.ID); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"deploy", "stop"} {
		if err := s.AddAudit(ctx, store.AuditEntry{Actor: "user:admin", Action: a, Status: 200}); err != nil {
			t.Fatal(err)
		}
	}
	es, err := s.ListAudit(ctx, 10)
	if err != nil || len(es) != 2 || es[0].Action != "stop" {
		t.Fatalf("audit: %+v %v", es, err)
	}
	if err := s.PruneAudit(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if es, _ := s.ListAudit(ctx, 10); len(es) != 0 {
		t.Fatalf("prune: %v", es)
	}
}

func TestScopeAllows(t *testing.T) {
	cases := []struct {
		have, want store.Scope
		ok         bool
	}{
		{store.ScopeRead, store.ScopeRead, true},
		{store.ScopeRead, store.ScopeDeploy, false},
		{store.ScopeDeploy, store.ScopeRead, true},
		{store.ScopeDeploy, store.ScopeAdmin, false},
		{store.ScopeAdmin, store.ScopeDeploy, true},
		{"bogus", store.ScopeRead, false},
		{store.ScopeAdmin, "bogus", false},
	}
	for _, c := range cases {
		if got := c.have.Allows(c.want); got != c.ok {
			t.Errorf("%s allows %s = %v", c.have, c.want, got)
		}
	}
}

func TestServersAndSettings(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	servers, err := s.ListServers(ctx)
	if err != nil || len(servers) != 1 || servers[0].ID != store.LocalServerID {
		t.Fatalf("seeded local server: %+v %v", servers, err)
	}
	remote, err := s.CreateServer(ctx, store.Server{Name: "db-2", Kind: store.ServerSSH, Host: "10.0.0.2", Port: 22, SSHUser: "root", Socket: "/var/run/docker.sock"})
	if err != nil {
		t.Fatal(err)
	}
	remote.HostKey = "ssh-ed25519 AAAA"
	if _, err := s.UpdateServer(ctx, remote); err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(ctx, store.Project{Name: "shop", ServerID: remote.ID})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := s.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "web", Kind: store.ServiceKindApp, Image: "nginx"})
	if err != nil || svc.ServerID != remote.ID {
		t.Fatalf("service must inherit the project's server: %+v %v", svc, err)
	}
	if def, _ := s.CreateProject(ctx, store.Project{Name: "other"}); def.ServerID != store.LocalServerID {
		t.Fatalf("default server: %q", def.ServerID)
	}
	if err := s.DeleteServer(ctx, remote.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("deleting a server in use: %v", err)
	}

	if _, err := s.GetSetting(ctx, "k"); !errors.Is(err, store.ErrNotFound) {
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

func TestSnapshotBeforeMigrating(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "k.db")

	// A database created by an older version: every migration but the last.
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	sub, _ := fs.Sub(migrations, "migrations")
	p, err := goose.NewProvider(goose.DialectSQLite3, db, sub)
	if err != nil {
		t.Fatal(err)
	}
	srcs := p.ListSources()
	prev := srcs[len(srcs)-2].Version
	if _, err := p.UpTo(ctx, prev); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	snap := fmt.Sprintf("%s.pre-migrate-%d", path, prev)
	if _, err := os.Stat(snap); err != nil {
		t.Fatalf("no snapshot before migrating: %v", err)
	}

	// Up to date: no new snapshot.
	os.Remove(snap)
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := os.Stat(snap); !os.IsNotExist(err) {
		t.Error("snapshot taken without pending migrations")
	}
}

func TestRefuseNewerSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "k.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	// A newer binary applied a migration this one doesn't know.
	sub, _ := fs.Sub(migrations, "migrations")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, db, sub)
	if err != nil {
		t.Fatal(err)
	}
	srcs := p.ListSources()
	latest := srcs[len(srcs)-1].Version
	if _, err := db.ExecContext(ctx, "INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)", latest+1); err != nil {
		t.Fatal(err)
	}
	db.Close()

	want := fmt.Sprintf("database schema is at %d, this version knows up to %d", latest+1, latest)
	if _, err := Open(ctx, path); err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "restore") {
		t.Fatalf("open newer schema: %v", err)
	}

	snap := fmt.Sprintf("%s.pre-migrate-%d", path, latest)
	os.WriteFile(snap, nil, 0o600)
	if _, err := Open(ctx, path); err == nil || !strings.HasSuffix(err.Error(), "or restore "+snap) {
		t.Fatalf("open newer schema with snapshot: %v", err)
	}
}

func TestPrunePreMigrate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k.db")
	for _, v := range []int{2, 9, 10, 11, 12} {
		os.WriteFile(fmt.Sprintf("%s.pre-migrate-%d", path, v), nil, 0o600)
	}
	prunePreMigrate(path)
	left, _ := filepath.Glob(path + ".pre-migrate-*")
	slices.Sort(left)
	want := []string{path + ".pre-migrate-10", path + ".pre-migrate-11", path + ".pre-migrate-12"}
	if !slices.Equal(left, want) {
		t.Errorf("left %v, want %v", left, want)
	}
}

func TestDomains(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	for _, n := range []string{"example.org", "example.com"} {
		if _, err := s.CreateDomain(ctx, store.Domain{Name: n}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateDomain(ctx, store.Domain{Name: "example.com"}); !errors.Is(err, store.ErrConflict) {
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

	ch, err := s.CreateNotificationChannel(ctx, store.NotificationChannel{
		Name: "ops", Kind: "discord", Config: map[string]string{"webhookUrl": "u"}, Events: []string{"deploy.failed"}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateNotificationChannel(ctx, store.NotificationChannel{Name: "ops", Kind: "discord"}); !errors.Is(err, store.ErrConflict) {
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
	if _, err := s.UpdateNotificationChannel(ctx, store.NotificationChannel{ID: "missing"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("update missing: %v", err)
	}
	if err := s.DeleteNotificationChannel(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	if chs, _ := s.ListNotificationChannels(ctx); len(chs) != 0 {
		t.Errorf("after delete: %+v", chs)
	}
}

// Rebuilding backup_targets for drive kinds keeps targets, the backups and
// schedules pointing at them, and foreign keys.
func TestDriveTargetsMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "k.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	sub, _ := fs.Sub(migrations, "migrations")
	p, err := goose.NewProvider(goose.DialectSQLite3, db, sub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 20); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO backup_targets (id, name, kind, bucket, created_at, age_recipient) VALUES ('t1', 'offsite', 's3', 'b', '2026-01-01 00:00:00+00:00', 'age1x')`,
		`INSERT INTO backups (id, service_id, project_id, service_name, project_name, target_id, object_key, status, created_at) VALUES ('b1', 's', 'p', 'db', 'shop', 't1', 'k', 'succeeded', '2026-01-01 00:00:00+00:00')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tg, err := s.GetBackupTarget(ctx, "t1")
	if err != nil || tg.Bucket != "b" || tg.AgeRecipient != "age1x" {
		t.Fatalf("target = %+v, %v", tg, err)
	}
	if b, err := s.GetBackup(ctx, "b1"); err != nil || b.TargetID != "t1" {
		t.Fatalf("backup = %+v, %v", b, err)
	}
	if err := s.DeleteBackupTarget(ctx, "t1"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("delete referenced target: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO backups (id, service_id, project_id, service_name, project_name, target_id, object_key, status, created_at) VALUES ('b2', 's', 'p', 'db', 'shop', 'nope', 'k', 'running', '2026-01-01 00:00:00+00:00')`); err == nil {
		t.Fatal("foreign keys are off after the migration")
	}
}

// Dropping the drive kinds deletes drive targets with their schedules,
// backups and restores, and keeps the rest with foreign keys on.
func TestDropDriveTargetsMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "k.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	sub, _ := fs.Sub(migrations, "migrations")
	p, err := goose.NewProvider(goose.DialectSQLite3, db, sub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 38); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`PRAGMA foreign_keys = OFF`, // the restore's service doesn't exist
		`INSERT INTO backup_targets (id, name, kind, bucket, created_at) VALUES ('s3', 'offsite', 's3', 'b', '2026-01-01 00:00:00+00:00')`,
		`INSERT INTO backup_targets (id, name, kind, created_at, config) VALUES ('gd', 'drive', 'gdrive', '2026-01-01 00:00:00+00:00', '{"token":"x"}')`,
		`INSERT INTO backup_schedules (id, kind, target_id, cron, created_at) VALUES ('sc', 'manager', 'gd', '@daily', '2026-01-01 00:00:00+00:00')`,
		`INSERT INTO backups (id, service_id, project_id, service_name, project_name, target_id, object_key, status, created_at) VALUES ('b1', 's', 'p', 'db', 'shop', 's3', 'k', 'succeeded', '2026-01-01 00:00:00+00:00')`,
		`INSERT INTO backups (id, service_id, project_id, service_name, project_name, target_id, object_key, status, created_at) VALUES ('b2', 's', 'p', 'db', 'shop', 'gd', 'k', 'succeeded', '2026-01-01 00:00:00+00:00')`,
		`INSERT INTO restores (id, backup_id, service_id, status, created_at) VALUES ('r2', 'b2', 's', 'succeeded', '2026-01-01 00:00:00+00:00')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if tg, err := s.GetBackupTarget(ctx, "s3"); err != nil || tg.Bucket != "b" {
		t.Fatalf("s3 target = %+v, %v", tg, err)
	}
	if _, err := s.GetBackup(ctx, "b1"); err != nil {
		t.Fatalf("s3 backup: %v", err)
	}
	if _, err := s.GetBackupTarget(ctx, "gd"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("drive target kept: %v", err)
	}
	if _, err := s.GetBackup(ctx, "b2"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("drive backup kept: %v", err)
	}
	for _, table := range []string{"backup_schedules", "restores"} {
		var n int
		if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s left: %d, %v", table, n, err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO backups (id, service_id, project_id, service_name, project_name, target_id, object_key, status, created_at) VALUES ('b3', 's', 'p', 'db', 'shop', 'gd', 'k', 'running', '2026-01-01 00:00:00+00:00')`); err == nil {
		t.Fatal("foreign keys are off after the migration")
	}
}

func TestImageOnlyMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "k.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	sub, _ := fs.Sub(migrations, "migrations")
	p, err := goose.NewProvider(goose.DialectSQLite3, db, sub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 23); err != nil {
		t.Fatal(err)
	}
	old := &Store{db: bun.NewDB(db, sqlitedialect.New())}
	pr, err := old.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	// Raw inserts: the current model has columns this schema lacks.
	newService := func(name string) store.Service {
		svc := store.Service{ID: ids.New(), ProjectID: pr.ID, Name: name}
		if _, err := db.ExecContext(ctx, `INSERT INTO services (id, project_id, server_id, name, kind, image, created_at, updated_at)
			VALUES (?, ?, ?, ?, 'app', '', '2026-01-01 00:00:00+00:00', '2026-01-01 00:00:00+00:00')`,
			svc.ID, pr.ID, pr.ServerID, name); err != nil {
			t.Fatal(err)
		}
		return svc
	}
	built := newService("api")
	d, err := old.CreateDeployment(ctx, store.Deployment{ServiceID: built.ID, Status: store.DeploymentSucceeded, Image: "kipitiny/api:abc"})
	if err != nil {
		t.Fatal(err)
	}
	if err := old.SetCurrentDeployment(ctx, built.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	never := newService("worker")
	if _, err := db.ExecContext(ctx, `UPDATE services SET source = 'git', image = '', git_url = 'https://x/y.git'`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, err := s.GetService(ctx, built.ID); err != nil || got.Image != "kipitiny/api:abc" {
		t.Fatalf("deployed git service = %q, %v", got.Image, err)
	}
	if got, err := s.GetService(ctx, never.ID); err != nil || got.Image != "" {
		t.Fatalf("never deployed git service = %q, %v", got.Image, err)
	}
}

func TestSetBackupTargetKey(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	if err := s.SetBackupTargetKey(ctx, store.LocalTargetID, "AGE-SECRET-KEY-1", "age1one"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetBackupTarget(ctx, store.LocalTargetID)
	if err != nil || got.AgeRecipient != "age1one" || got.AgeIdentity != "AGE-SECRET-KEY-1" {
		t.Fatalf("key not saved: %+v %v", got, err)
	}
	// A key is never replaced: backups encrypted with it would be lost.
	if err := s.SetBackupTargetKey(ctx, store.LocalTargetID, "AGE-SECRET-KEY-2", "age1two"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second key: %v", err)
	}
	if got, _ := s.GetBackupTarget(ctx, store.LocalTargetID); got.AgeRecipient != "age1one" {
		t.Fatalf("key replaced: %+v", got)
	}
	if err := s.SetBackupTargetKey(ctx, "missing", "k", "r"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown target: %v", err)
	}
}

func TestRedisMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "k.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	sub, _ := fs.Sub(migrations, "migrations")
	p, err := goose.NewProvider(goose.DialectSQLite3, db, sub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 26); err != nil {
		t.Fatal(err)
	}
	old := &Store{db: bun.NewDB(db, sqlitedialect.New())}
	pr, err := old.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	// Raw SQL: the current model has columns added after this version.
	pg := store.Service{ID: "pg1"}
	if _, err := db.ExecContext(ctx, `INSERT INTO services (id, project_id, name, kind, image, replicas, created_at, updated_at, env, memory_mb, secrets, cpus)
		VALUES ('pg1', ?, 'db', 'postgres', 'postgres:17', 1, '2026-01-01 00:00:00+00:00', '2026-01-01 00:00:00+00:00', '{"A":"1"}', 512, '["A"]', 0.5)`, pr.ID); err != nil {
		t.Fatal(err)
	}
	d, err := old.CreateDeployment(ctx, store.Deployment{ServiceID: pg.ID, Status: store.DeploymentSucceeded, Image: "postgres:17"})
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.GetService(ctx, pg.ID)
	if err != nil || got.CPUs != 0.5 || got.MemoryMB != 512 || got.Env["A"] != "1" || !slices.Equal(got.Secrets, []string{"A"}) {
		t.Fatalf("service after rebuild = %+v, %v", got, err)
	}
	if _, err := s.GetDeployment(ctx, d.ID); err != nil {
		t.Fatalf("deployment lost: %v", err)
	}
	if _, err := s.CreateService(ctx, store.Service{ProjectID: pr.ID, Name: "cache", Kind: store.ServiceKindRedis, Image: "redis:8-alpine", Replicas: 1}); err != nil {
		t.Fatalf("create redis: %v", err)
	}
	if _, err := s.CreateService(ctx, store.Service{ProjectID: pr.ID, Name: "db", Kind: store.ServiceKindRedis, Image: "redis:8-alpine", Replicas: 1}); err == nil {
		t.Fatal("unique (project_id, name) lost")
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO deployments (id, service_id, status, image, created_at) VALUES ('d2', 'nope', 'running', 'x', '2026-01-01 00:00:00+00:00')`); err == nil {
		t.Fatal("foreign keys are off after the migration")
	}
	if err := s.DeleteService(ctx, pg.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetDeployment(ctx, d.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deployment not cascaded: %v", err)
	}
}

func TestUptime(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	p, err := s.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := s.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "web", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetUptimeCheck(ctx, svc.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("before: %v", err)
	}
	c, err := s.SaveUptimeCheck(ctx, store.UptimeCheck{ServiceID: svc.ID, Path: "/", IntervalSec: 60, TimeoutSec: 10, Enabled: true})
	if err != nil || c.Down || c.ChangedAt != nil {
		t.Fatalf("create: %+v %v", c, err)
	}
	since := time.Now()
	if err := s.SetUptimeState(ctx, svc.ID, true, since); err != nil {
		t.Fatal(err)
	}
	// Saving the settings again keeps the state.
	c, err = s.SaveUptimeCheck(ctx, store.UptimeCheck{ServiceID: svc.ID, Path: "/health", IntervalSec: 30, TimeoutSec: 5, ExpectedStatus: 204})
	if err != nil || c.Path != "/health" || c.IntervalSec != 30 || c.ExpectedStatus != 204 || c.Enabled || !c.Down || c.ChangedAt == nil {
		t.Fatalf("update: %+v %v", c, err)
	}
	if cs, err := s.ListUptimeChecks(ctx); err != nil || len(cs) != 1 {
		t.Fatalf("list: %v %v", cs, err)
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
	if err := s.DeleteUptimeCheck(ctx, svc.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("delete again: %v", err)
	}
}

func TestVolumeBackupsMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "k.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	sub, _ := fs.Sub(migrations, "migrations")
	p, err := goose.NewProvider(goose.DialectSQLite3, db, sub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 31); err != nil {
		t.Fatal(err)
	}
	// Raw SQL: the current model has columns added after this version.
	for _, q := range []string{
		`INSERT INTO projects (id, name, created_at, updated_at) VALUES ('p1', 'shop', '2026-01-01 00:00:00+00:00', '2026-01-01 00:00:00+00:00')`,
		`INSERT INTO services (id, project_id, name, kind, image, created_at, updated_at) VALUES ('s1', 'p1', 'db', 'postgres', 'postgres:17', '2026-01-01 00:00:00+00:00', '2026-01-01 00:00:00+00:00')`,
		`INSERT INTO backups (id, service_id, project_id, service_name, project_name, target_id, object_key, status, size_bytes, sha256, pg_version,
			duration_ms, created_at, schedule_id, kind, encrypted, verify_status, verify_details)
			VALUES ('b1', 's1', 'p1', 'db', 'shop', 'local', 'shop/db/x.dump', 'succeeded', 42, 'abc', '17.2', 7, '2026-01-01 00:00:00+00:00', 'sc1', 'postgres', 1, 'succeeded', '{"tables":3}')`,
		`INSERT INTO restores (id, backup_id, service_id, status, created_at) VALUES ('r1', 'b1', 's1', 'succeeded', '2026-01-02 00:00:00+00:00')`,
		`INSERT INTO backup_schedules (id, kind, service_id, target_id, cron, keep_daily, created_at) VALUES ('sc1', 'postgres', 's1', 'local', '@daily', 7, '2026-01-01 00:00:00+00:00')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	db.Close()

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := s.GetBackup(ctx, "b1")
	if err != nil || b.SizeBytes != 42 || b.PGVersion != "17.2" || !b.Encrypted || b.ScheduleID != "sc1" ||
		b.VerifyStatus != store.OpSucceeded || b.VerifyDetails.Tables != 3 || len(b.Volumes) != 0 {
		t.Fatalf("backup after rebuild = %+v, %v", b, err)
	}
	if rs, err := s.ListRestores(ctx, "s1", 10); err != nil || len(rs) != 1 {
		t.Fatalf("restores after rebuild = %v, %v", rs, err)
	}
	if sc, err := s.GetBackupSchedule(ctx, "sc1"); err != nil || sc.KeepDaily != 7 {
		t.Fatalf("schedule after rebuild = %+v, %v", sc, err)
	}
	if _, err := s.CreateBackupSchedule(ctx, store.BackupSchedule{Kind: store.BackupKindVolume, ServiceID: "s1", TargetID: store.LocalTargetID, Cron: "@daily"}); err != nil {
		t.Fatalf("volume schedule: %v", err)
	}
	if _, err := s.CreateBackupSchedule(ctx, store.BackupSchedule{Kind: store.BackupKindVolume, TargetID: store.LocalTargetID, Cron: "@daily"}); err == nil {
		t.Fatal("service-less volume schedule accepted")
	}
	// Restores still cascade with their backup.
	if err := s.DeleteBackup(ctx, "b1"); err != nil {
		t.Fatal(err)
	}
	if rs, _ := s.ListRestores(ctx, "s1", 10); len(rs) != 0 {
		t.Fatalf("restores not cascaded: %v", rs)
	}
}

func TestProjectGit(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	p, err := s.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := s.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "web", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 1})
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.SaveProjectGit(ctx, store.ProjectGit{ProjectID: p.ID, RepoURL: "https://example.com/r.git", Branch: "main", Path: "compose.yaml",
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
	g, err = s.SaveProjectGit(ctx, store.ProjectGit{ProjectID: p.ID, RepoURL: "https://example.com/r.git", Branch: "prod", Path: "c.yml", WebhookSecret: "new"})
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
	if _, err := s.GetProjectGit(ctx, p.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}
