package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/ids"
)

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
	if err := os.WriteFile(snap, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, path); err == nil || !strings.HasSuffix(err.Error(), "or restore "+snap) {
		t.Fatalf("open newer schema with snapshot: %v", err)
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
	if err := s.DeleteBackupTarget(ctx, "t1"); !errors.Is(err, domain.ErrConflict) {
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
	if _, err := s.GetBackupTarget(ctx, "gd"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("drive target kept: %v", err)
	}
	if _, err := s.GetBackup(ctx, "b2"); !errors.Is(err, domain.ErrNotFound) {
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
	// Raw inserts: the current models have columns this schema lacks.
	pr := domain.Project{ID: "p1", ServerID: domain.LocalServerID}
	if _, err := db.ExecContext(ctx, `INSERT INTO projects (id, name, server_id, created_at, updated_at)
		VALUES ('p1', 'shop', ?, '2026-01-01 00:00:00+00:00', '2026-01-01 00:00:00+00:00')`, pr.ServerID); err != nil {
		t.Fatal(err)
	}
	newService := func(name string) domain.Service {
		svc := domain.Service{ID: ids.New(), ProjectID: pr.ID, Name: name}
		if _, err := db.ExecContext(ctx, `INSERT INTO services (id, project_id, server_id, name, kind, image, created_at, updated_at)
			VALUES (?, ?, ?, ?, 'app', '', '2026-01-01 00:00:00+00:00', '2026-01-01 00:00:00+00:00')`,
			svc.ID, pr.ID, pr.ServerID, name); err != nil {
			t.Fatal(err)
		}
		return svc
	}
	built := newService("api")
	d, err := old.CreateDeployment(ctx, domain.Deployment{ServiceID: built.ID, Status: domain.DeploymentSucceeded, Image: "kipitiny/api:abc"})
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

// Project tokens are dropped: a link that had one keeps its settings and
// says to pick a git provider.
func TestGitProvidersMigration(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 39); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO projects (id, name, server_id, created_at, updated_at) VALUES ('p1', 'shop', 'local', '2026-01-01 00:00:00+00:00', '2026-01-01 00:00:00+00:00'), ('p2', 'blog', 'local', '2026-01-01 00:00:00+00:00', '2026-01-01 00:00:00+00:00')`,
		`INSERT INTO project_git (project_id, repo_url, branch, path, token, webhook_secret, created_at) VALUES
			('p1', 'https://github.com/me/private.git', 'main', 'compose.yaml', 'ghp_x', 'w', '2026-01-01 00:00:00+00:00'),
			('p2', 'https://github.com/me/public.git', 'main', 'compose.yaml', '', 'w', '2026-01-01 00:00:00+00:00')`,
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
	g, err := s.GetProjectGit(ctx, "p1")
	if err != nil || g.ProviderID != "" || !strings.Contains(g.LastError, "git provider") || g.RepoURL != "https://github.com/me/private.git" {
		t.Fatalf("private link = %+v, %v", g, err)
	}
	if g, err := s.GetProjectGit(ctx, "p2"); err != nil || g.LastError != "" {
		t.Fatalf("public link = %+v, %v", g, err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE project_git SET provider_id = 'nope' WHERE project_id = 'p2'`); err == nil {
		t.Fatal("provider_id isn't a foreign key")
	}
}
