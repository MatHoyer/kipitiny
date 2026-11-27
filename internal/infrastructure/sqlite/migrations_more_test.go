package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

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
	// Raw SQL: the current models have columns added after this version.
	pr := domain.Project{ID: "p1"}
	if _, err := db.ExecContext(ctx, `INSERT INTO projects (id, name, server_id, created_at, updated_at)
		VALUES ('p1', 'shop', ?, '2026-01-01 00:00:00+00:00', '2026-01-01 00:00:00+00:00')`, domain.LocalServerID); err != nil {
		t.Fatal(err)
	}
	pg := domain.Service{ID: "pg1"}
	if _, err := db.ExecContext(ctx, `INSERT INTO services (id, project_id, name, kind, image, replicas, created_at, updated_at, env, memory_mb, secrets, cpus)
		VALUES ('pg1', ?, 'db', 'postgres', 'postgres:17', 1, '2026-01-01 00:00:00+00:00', '2026-01-01 00:00:00+00:00', '{"A":"1"}', 512, '["A"]', 0.5)`, pr.ID); err != nil {
		t.Fatal(err)
	}
	d, err := old.CreateDeployment(ctx, domain.Deployment{ServiceID: pg.ID, Status: domain.DeploymentSucceeded, Image: "postgres:17"})
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
	if _, err := s.CreateService(ctx, domain.Service{ProjectID: pr.ID, Name: "cache", Kind: domain.ServiceKindRedis, Image: "redis:8-alpine", Replicas: 1}); err != nil {
		t.Fatalf("create redis: %v", err)
	}
	if _, err := s.CreateService(ctx, domain.Service{ProjectID: pr.ID, Name: "db", Kind: domain.ServiceKindRedis, Image: "redis:8-alpine", Replicas: 1}); err == nil {
		t.Fatal("unique (project_id, name) lost")
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO deployments (id, service_id, status, image, created_at) VALUES ('d2', 'nope', 'running', 'x', '2026-01-01 00:00:00+00:00')`); err == nil {
		t.Fatal("foreign keys are off after the migration")
	}
	if err := s.DeleteService(ctx, pg.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetDeployment(ctx, d.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deployment not cascaded: %v", err)
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
		b.VerifyStatus != domain.OpSucceeded || b.VerifyDetails.Tables != 3 || len(b.Volumes) != 0 {
		t.Fatalf("backup after rebuild = %+v, %v", b, err)
	}
	if rs, err := s.ListRestores(ctx, "s1", 10); err != nil || len(rs) != 1 {
		t.Fatalf("restores after rebuild = %v, %v", rs, err)
	}
	if sc, err := s.GetBackupSchedule(ctx, "sc1"); err != nil || sc.KeepDaily != 7 {
		t.Fatalf("schedule after rebuild = %+v, %v", sc, err)
	}
	if _, err := s.CreateBackupSchedule(ctx, domain.BackupSchedule{Kind: domain.BackupKindVolume, ServiceID: "s1", TargetID: domain.LocalTargetID, Cron: "@daily"}); err != nil {
		t.Fatalf("volume schedule: %v", err)
	}
	if _, err := s.CreateBackupSchedule(ctx, domain.BackupSchedule{Kind: domain.BackupKindVolume, TargetID: domain.LocalTargetID, Cron: "@daily"}); err == nil {
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

func TestProjectSleepAndMaintenanceMigration(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 52); err != nil {
		t.Fatal(err)
	}
	// Raw inserts: the current models have columns this schema lacks.
	for _, q := range []string{
		`INSERT INTO projects (id, name, server_id, created_at, updated_at) VALUES ('shop', 'shop', '` + domain.LocalServerID + `', '2026-01-01', '2026-01-01')`,
		`INSERT INTO projects (id, name, server_id, parent_id, created_at, updated_at) VALUES ('pr1', 'shop-pr-1', '` + domain.LocalServerID + `', 'shop', '2026-01-01', '2026-01-01')`,
		`INSERT INTO projects (id, name, server_id, created_at, updated_at) VALUES ('blog', 'blog', '` + domain.LocalServerID + `', '2026-01-01', '2026-01-01')`,
		`INSERT INTO preview_settings (project_id, sleep_after_seconds, updated_at) VALUES ('shop', 600, '2026-01-01')`,
		`INSERT INTO services (id, project_id, server_id, name, kind, image, sleep_after_seconds, sleeping, maintenance, created_at, updated_at)
			VALUES ('web', 'shop', '` + domain.LocalServerID + `', 'web', 'app', 'nginx', 1800, 1, '{"enabled":true,"title":"Back soon"}', '2026-01-01', '2026-01-01')`,
		`INSERT INTO services (id, project_id, server_id, name, kind, image, maintenance, created_at, updated_at)
			VALUES ('api', 'shop', '` + domain.LocalServerID + `', 'api', 'app', 'nginx', '{"enabled":true}', '2026-01-01', '2026-01-01')`,
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
	shop, err := s.GetProject(ctx, "shop")
	if err != nil {
		t.Fatal(err)
	}
	// Apps' sleep is dropped; previews sleep as their settings say.
	if shop.SleepAfterSeconds != 0 || shop.Maintenance.Title != "Back soon" || shop.Maintenance.Enabled {
		t.Errorf("shop: sleep %d, maintenance %+v", shop.SleepAfterSeconds, shop.Maintenance)
	}
	if pr, _ := s.GetProject(ctx, "pr1"); pr.SleepAfterSeconds != 600 {
		t.Errorf("preview sleeps after %d", pr.SleepAfterSeconds)
	}
	if blog, _ := s.GetProject(ctx, "blog"); blog.Maintenance.Title != "" || blog.Maintenance.Enabled {
		t.Errorf("blog: %+v", blog.Maintenance)
	}
	if web, _ := s.GetService(ctx, "web"); web.Sleeping {
		t.Error("an app asleep during the upgrade stays asleep")
	}
}

func TestSecretConnectionsMigration(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 54); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('secrets_protonpass_token', 'pst_x::y')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cs, err := s.ListSecretConnections(ctx)
	if err != nil || len(cs) != 1 {
		t.Fatalf("connections = %+v, %v", cs, err)
	}
	if c := cs[0]; c.Kind != "protonpass" || c.Name != "" || c.Token != "pst_x::y" || len(c.ID) != 26 {
		t.Errorf("migrated = %+v", c)
	}
	if v, _ := s.GetSetting(ctx, "secrets_protonpass_token"); v != "" {
		t.Errorf("setting kept: %q", v)
	}

	// Two connections of a kind need different names.
	work, err := s.CreateSecretConnection(ctx, domain.SecretConnection{Kind: "protonpass", Name: "work", Token: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSecretConnection(ctx, domain.SecretConnection{Kind: "protonpass", Name: "work", Token: "b"}); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("duplicate name: %v", err)
	}
	if err := s.SetSecretConnectionToken(ctx, work.ID, "c"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetSecretConnection(ctx, work.ID); got.Token != "c" {
		t.Errorf("token = %q", got.Token)
	}
	if err := s.SetSecretConnectionToken(ctx, "nope", "c"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown connection: %v", err)
	}
	if err := s.DeleteSecretConnection(ctx, work.ID); err != nil {
		t.Fatal(err)
	}
	if cs, _ := s.ListSecretConnections(ctx); len(cs) != 1 {
		t.Errorf("after delete: %+v", cs)
	}
}

func TestTokenScopeMigration(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 59); err != nil {
		t.Fatal(err)
	}
	const at = "2026-01-01 00:00:00+00:00"
	for _, q := range []string{
		`INSERT INTO users (id, username, password_hash, created_at, role) VALUES ('a', 'admin', 'x', '` + at + `', 'admin')`,
		`INSERT INTO users (id, username, password_hash, created_at, role) VALUES ('m', 'member', 'x', '` + at + `', 'member')`,
		`INSERT INTO projects (id, name, created_at, updated_at) VALUES ('p1', 'shop', '` + at + `', '` + at + `')`,
		`INSERT INTO projects (id, name, created_at, updated_at) VALUES ('p2', 'site', '` + at + `', '` + at + `')`,
		`INSERT INTO projects (id, name, created_at, updated_at, parent_id) VALUES ('pv', 'shop-pr-1', '` + at + `', '` + at + `', 'p1')`,
		`INSERT INTO project_grants (user_id, project_id, perms, created_at) VALUES ('m', 'p2', '["view","deploy"]', '` + at + `')`,
		`INSERT INTO api_tokens (id, name, token_hash, scope, created_at, user_id) VALUES ('tr', 'r', 'hr', 'read', '` + at + `', 'a')`,
		`INSERT INTO api_tokens (id, name, token_hash, scope, created_at, user_id) VALUES ('td', 'd', 'hd', 'deploy', '` + at + `', 'm')`,
		`INSERT INTO api_tokens (id, name, token_hash, scope, created_at, user_id) VALUES ('ta', 'f', 'ha', 'admin', '` + at + `', 'a')`,
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
	for hash, want := range map[string]domain.APIToken{
		"hr": {Restricted: true, Grants: map[string][]string{"p1": {"view", "data"}, "p2": {"view", "data"}}},
		"hd": {Restricted: true, Grants: map[string][]string{"p2": {"view", "deploy", "backups", "data"}}},
		"ha": {Restricted: false, Grants: map[string][]string{}},
	} {
		tok, err := s.GetAPITokenByHash(ctx, hash)
		if err != nil || tok.Restricted != want.Restricted || !reflect.DeepEqual(tok.Grants, want.Grants) ||
			(want.Restricted && !slices.Contains(tok.Features, "storage")) || slices.Contains(tok.Features, "create-projects") {
			t.Errorf("%s = %+v, %v", hash, tok, err)
		}
	}
}

func TestSeedModeMigration(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 72); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO projects (id, name, server_id, created_at, updated_at) VALUES ('shop', 'shop', '` + domain.LocalServerID + `', '2026-01-01', '2026-01-01')`,
		`INSERT INTO projects (id, name, server_id, created_at, updated_at) VALUES ('blog', 'blog', '` + domain.LocalServerID + `', '2026-01-01', '2026-01-01')`,
		`INSERT INTO preview_settings (project_id, allow_seed, updated_at) VALUES ('shop', 1, '2026-01-01')`,
		`INSERT INTO preview_settings (project_id, allow_seed, updated_at) VALUES ('blog', 0, '2026-01-01')`,
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
	// Projects that allowed seeding keep seeding with their data as is.
	if ps, _ := s.GetPreviewSettings(ctx, "shop"); ps.SeedMode != domain.SeedRaw {
		t.Errorf("shop seeds %q", ps.SeedMode)
	}
	if ps, _ := s.GetPreviewSettings(ctx, "blog"); ps.SeedMode != domain.SeedOff {
		t.Errorf("blog seeds %q", ps.SeedMode)
	}
}
