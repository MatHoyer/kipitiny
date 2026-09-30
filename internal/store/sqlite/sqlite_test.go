package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

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

	svc.Env["B"] = "2"
	svc.Replicas = 3
	if _, err := s.UpdateService(ctx, svc); err != nil {
		t.Fatal(err)
	}
	got2, err := s.GetService(ctx, svc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got2.Replicas != 3 || got2.Env["A"] != "1" || got2.Env["B"] != "2" || got2.Domain != "shop.example.com" {
		t.Fatalf("service round-trip mismatch: %+v", got2)
	}

	d, err := s.CreateDeployment(ctx, store.Deployment{ServiceID: svc.ID, Status: store.DeploymentRunning, Image: "nginx", Config: got2})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetDeployment(ctx, d.ID); got.Config.Env["B"] != "2" || got.Config.Replicas != 3 {
		t.Fatalf("deployment config snapshot: %+v", got.Config)
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
