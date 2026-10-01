// Package sqlite implements store.Store on SQLite (modernc.org/sqlite, no CGO).
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/MatHoyer/kipitiny/internal/ids"
	"github.com/MatHoyer/kipitiny/internal/store"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct {
	db *bun.DB
}

var _ store.Store = (*Store)(nil)

// Open opens (or creates) the database at path and applies pending migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(ON)" +
		"&_pragma=synchronous(NORMAL)"
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// Single writer: serialize all access through one connection.
	sqldb.SetMaxOpenConns(1)

	if err := migrate(ctx, sqldb, path); err != nil {
		sqldb.Close()
		return nil, err
	}
	return &Store{db: bun.NewDB(sqldb, sqlitedialect.New())}, nil
}

// keepPreMigrate is how many pre-migration snapshots are kept next to the DB.
const keepPreMigrate = 3

// migrate applies pending migrations, first snapshotting an existing database
// to <path>.pre-migrate-<version> so an upgrade can be undone.
func migrate(ctx context.Context, db *sql.DB, path string) error {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, db, sub)
	if err != nil {
		return fmt.Errorf("goose: %w", err)
	}
	pending, err := p.HasPending(ctx)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if !pending {
		return nil
	}
	if v, err := p.GetDBVersion(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	} else if v > 0 {
		snap := fmt.Sprintf("%s.pre-migrate-%d", path, v)
		_ = os.Remove(snap) // VACUUM INTO refuses to overwrite
		if _, err := db.ExecContext(ctx, "VACUUM INTO ?", snap); err != nil {
			return fmt.Errorf("snapshot before migrating: %w", err)
		}
		prunePreMigrate(path)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// prunePreMigrate keeps the newest keepPreMigrate snapshots.
func prunePreMigrate(path string) {
	snaps, _ := filepath.Glob(path + ".pre-migrate-*")
	version := func(p string) int {
		n, _ := strconv.Atoi(p[strings.LastIndex(p, "-")+1:])
		return n
	}
	slices.SortFunc(snaps, func(a, b string) int { return version(b) - version(a) })
	for _, old := range snaps[min(len(snaps), keepPreMigrate):] {
		_ = os.Remove(old)
	}
}

func (s *Store) Close() error { return s.db.Close() }

// Snapshot uses VACUUM INTO: a consistent, compacted copy taken without
// blocking writers for long.
func (s *Store) Snapshot(ctx context.Context, path string) error {
	_, err := s.db.ExecContext(ctx, "VACUUM INTO ?", path)
	return err
}

func (s *Store) CreateProject(ctx context.Context, p store.Project) (store.Project, error) {
	now := now()
	p.ID, p.CreatedAt, p.UpdatedAt = ids.New(), now, now
	if p.ServerID == "" {
		p.ServerID = store.LocalServerID
	}
	if p.Env == nil {
		p.Env = map[string]string{}
	}
	if p.Secrets == nil {
		p.Secrets = []string{}
	}
	if _, err := s.db.NewInsert().Model(&p).Exec(ctx); err != nil {
		return store.Project{}, mapErr(err)
	}
	return p, nil
}

func (s *Store) GetProject(ctx context.Context, id string) (store.Project, error) {
	var p store.Project
	err := s.db.NewSelect().Model(&p).Where("id = ?", id).Scan(ctx)
	return p, mapErr(err)
}

func (s *Store) ListProjects(ctx context.Context) ([]store.Project, error) {
	ps := []store.Project{}
	err := s.db.NewSelect().Model(&ps).Order("name").Scan(ctx)
	return ps, mapErr(err)
}

func (s *Store) SetProjectEnv(ctx context.Context, id string, env map[string]string, secrets []string) (store.Project, error) {
	if env == nil {
		env = map[string]string{}
	}
	if secrets == nil {
		secrets = []string{}
	}
	p := store.Project{ID: id, Env: env, Secrets: secrets, UpdatedAt: now()}
	res, err := s.db.NewUpdate().Model(&p).Column("env", "secrets", "updated_at").WherePK().Exec(ctx)
	if err != nil {
		return store.Project{}, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.Project{}, store.ErrNotFound
	}
	return s.GetProject(ctx, id)
}

func (s *Store) DeleteProject(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "projects", id)
}

func (s *Store) CreateService(ctx context.Context, svc store.Service) (store.Service, error) {
	now := now()
	svc.ID, svc.CreatedAt, svc.UpdatedAt = ids.New(), now, now
	if svc.Source == "" {
		svc.Source = store.SourceImage
	}
	if svc.Secrets == nil {
		svc.Secrets = []string{}
	}
	// Services always run on their project's server.
	err := s.db.NewSelect().Model((*store.Project)(nil)).Column("server_id").
		Where("id = ?", svc.ProjectID).Scan(ctx, &svc.ServerID)
	if err != nil {
		return store.Service{}, mapErr(err)
	}
	if _, err := s.db.NewInsert().Model(&svc).Exec(ctx); err != nil {
		return store.Service{}, mapErr(err)
	}
	return svc, nil
}

func (s *Store) GetService(ctx context.Context, id string) (store.Service, error) {
	var svc store.Service
	err := s.db.NewSelect().Model(&svc).Where("id = ?", id).Scan(ctx)
	return svc, mapErr(err)
}

func (s *Store) ListServices(ctx context.Context, projectID string) ([]store.Service, error) {
	svcs := []store.Service{}
	err := s.db.NewSelect().Model(&svcs).
		Where("project_id = ?", projectID).Order("name").Scan(ctx)
	return svcs, mapErr(err)
}

func (s *Store) UpdateService(ctx context.Context, svc store.Service) (store.Service, error) {
	svc.UpdatedAt = now()
	res, err := s.db.NewUpdate().Model(&svc).
		Column("image", "replicas", "port", "domain", "env", "secrets", "memory_mb", "health_path", "pre_deploy",
			"git_url", "git_branch", "git_token", "dockerfile", "build_context", "updated_at").
		WherePK().Exec(ctx)
	if err != nil {
		return store.Service{}, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.Service{}, store.ErrNotFound
	}
	return svc, nil
}

func (s *Store) SetCurrentDeployment(ctx context.Context, serviceID, deploymentID string) error {
	res, err := s.db.NewUpdate().Model((*store.Service)(nil)).
		Set("current_deployment_id = ?", deploymentID).Where("id = ?", serviceID).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) SetDeploymentBuild(ctx context.Context, id, image, commit string, config store.Service) error {
	d := store.Deployment{ID: id, Image: image, GitCommit: commit, Config: config}
	res, err := s.db.NewUpdate().Model(&d).Column("image", "git_commit", "config").WherePK().Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) SetServiceStopped(ctx context.Context, serviceID string, stopped bool) error {
	res, err := s.db.NewUpdate().Model((*store.Service)(nil)).
		Set("stopped = ?", stopped).Where("id = ?", serviceID).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) ListAllServices(ctx context.Context) ([]store.Service, error) {
	svcs := []store.Service{}
	err := s.db.NewSelect().Model(&svcs).Order("project_id", "name").Scan(ctx)
	return svcs, mapErr(err)
}

func (s *Store) DeleteService(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "services", id)
}

func (s *Store) CreateDeployment(ctx context.Context, d store.Deployment) (store.Deployment, error) {
	d.ID, d.CreatedAt = ids.New(), now()
	if _, err := s.db.NewInsert().Model(&d).Exec(ctx); err != nil {
		return store.Deployment{}, mapErr(err)
	}
	return d, nil
}

func (s *Store) GetDeployment(ctx context.Context, id string) (store.Deployment, error) {
	var d store.Deployment
	err := s.db.NewSelect().Model(&d).Where("id = ?", id).Scan(ctx)
	return d, mapErr(err)
}

func (s *Store) ListDeployments(ctx context.Context, serviceID string, limit int) ([]store.Deployment, error) {
	ds := []store.Deployment{}
	err := s.db.NewSelect().Model(&ds).Where("service_id = ?", serviceID).
		Order("created_at DESC").Limit(limit).Scan(ctx)
	return ds, mapErr(err)
}

func (s *Store) DeleteDeployment(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "deployments", id)
}

func (s *Store) FinishDeployment(ctx context.Context, id string, status store.DeploymentStatus, errMsg string) error {
	res, err := s.db.NewUpdate().Model((*store.Deployment)(nil)).
		Set("status = ?", status).Set("error = ?", errMsg).Set("finished_at = ?", now()).
		Where("id = ?", id).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) FailRunningDeployments(ctx context.Context, errMsg string) (int, error) {
	res, err := s.db.NewUpdate().Model((*store.Deployment)(nil)).
		Set("status = ?", store.DeploymentFailed).Set("error = ?", errMsg).Set("finished_at = ?", now()).
		Where("status = ?", store.DeploymentRunning).Exec(ctx)
	if err != nil {
		return 0, mapErr(err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	n, err := s.db.NewSelect().Model((*store.User)(nil)).Count(ctx)
	return n, mapErr(err)
}

func (s *Store) CreateUser(ctx context.Context, u store.User) (store.User, error) {
	u.ID, u.CreatedAt = ids.New(), now()
	if _, err := s.db.NewInsert().Model(&u).Exec(ctx); err != nil {
		return store.User{}, mapErr(err)
	}
	return u, nil
}

func (s *Store) GetUser(ctx context.Context, id string) (store.User, error) {
	var u store.User
	err := s.db.NewSelect().Model(&u).Where("id = ?", id).Scan(ctx)
	return u, mapErr(err)
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (store.User, error) {
	var u store.User
	err := s.db.NewSelect().Model(&u).Where("username = ?", username).Scan(ctx)
	return u, mapErr(err)
}

func (s *Store) SetPassword(ctx context.Context, userID, passwordHash string) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		res, err := tx.NewUpdate().Model((*store.User)(nil)).
			Set("password_hash = ?", passwordHash).Where("id = ?", userID).Exec(ctx)
		if err != nil {
			return mapErr(err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return store.ErrNotFound
		}
		_, err = tx.NewDelete().Model((*store.Session)(nil)).Where("user_id = ?", userID).Exec(ctx)
		return mapErr(err)
	})
}

func (s *Store) CreateSession(ctx context.Context, sess store.Session) error {
	sess.CreatedAt = now()
	sess.ExpiresAt = sess.ExpiresAt.UTC().Truncate(time.Microsecond)
	_, err := s.db.NewInsert().Model(&sess).Exec(ctx)
	return mapErr(err)
}

func (s *Store) GetSession(ctx context.Context, tokenHash string) (store.Session, error) {
	var sess store.Session
	err := s.db.NewSelect().Model(&sess).
		Where("token_hash = ?", tokenHash).Where("expires_at > ?", now()).Scan(ctx)
	return sess, mapErr(err)
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.NewDelete().Model((*store.Session)(nil)).Where("token_hash = ?", tokenHash).Exec(ctx)
	return mapErr(err)
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	_, err := s.db.NewDelete().Model((*store.Session)(nil)).Where("expires_at <= ?", now()).Exec(ctx)
	return mapErr(err)
}

func (s *Store) ListBackupTargets(ctx context.Context) ([]store.BackupTarget, error) {
	ts := []store.BackupTarget{}
	// Local first, then by name.
	err := s.db.NewSelect().Model(&ts).OrderExpr("kind = 'local' DESC, name").Scan(ctx)
	return ts, mapErr(err)
}

func (s *Store) GetBackupTarget(ctx context.Context, id string) (store.BackupTarget, error) {
	var t store.BackupTarget
	err := s.db.NewSelect().Model(&t).Where("id = ?", id).Scan(ctx)
	return t, mapErr(err)
}

func (s *Store) CreateBackupTarget(ctx context.Context, t store.BackupTarget) (store.BackupTarget, error) {
	t.ID, t.CreatedAt = ids.New(), now()
	if _, err := s.db.NewInsert().Model(&t).Exec(ctx); err != nil {
		return store.BackupTarget{}, mapErr(err)
	}
	return t, nil
}

func (s *Store) UpdateBackupTarget(ctx context.Context, t store.BackupTarget) (store.BackupTarget, error) {
	res, err := s.db.NewUpdate().Model(&t).
		Column("name", "endpoint", "region", "bucket", "prefix", "access_key", "secret_key", "use_ssl", "config").
		WherePK().Exec(ctx)
	if err != nil {
		return store.BackupTarget{}, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.BackupTarget{}, store.ErrNotFound
	}
	return t, nil
}

func (s *Store) SetBackupTargetConfig(ctx context.Context, id string, config map[string]string) error {
	t := store.BackupTarget{ID: id, Config: config}
	res, err := s.db.NewUpdate().Model(&t).Column("config").WherePK().Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteBackupTarget(ctx context.Context, id string) error {
	n, err := s.db.NewSelect().Model((*store.Backup)(nil)).Where("target_id = ?", id).Count(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n > 0 {
		return fmt.Errorf("%w: %d backups are stored on this target", store.ErrConflict, n)
	}
	return deleteByID(ctx, s.db, "backup_targets", id)
}

func (s *Store) CreateBackup(ctx context.Context, b store.Backup) (store.Backup, error) {
	b.ID, b.CreatedAt = ids.New(), now()
	if b.Kind == "" {
		b.Kind = store.BackupKindPostgres
	}
	if _, err := s.db.NewInsert().Model(&b).Exec(ctx); err != nil {
		return store.Backup{}, mapErr(err)
	}
	return b, nil
}

func (s *Store) GetBackup(ctx context.Context, id string) (store.Backup, error) {
	var b store.Backup
	err := s.db.NewSelect().Model(&b).Where("id = ?", id).Scan(ctx)
	return b, mapErr(err)
}

func (s *Store) ListBackups(ctx context.Context, f store.BackupFilter) ([]store.Backup, error) {
	bs := []store.Backup{}
	q := s.db.NewSelect().Model(&bs).Order("created_at DESC")
	if f.ServiceID != "" {
		q = q.Where("service_id = ?", f.ServiceID)
	}
	if f.ProjectID != "" {
		q = q.Where("project_id = ?", f.ProjectID)
	}
	if f.Kind != "" {
		q = q.Where("kind = ?", f.Kind)
	}
	if f.ScheduleID != "" {
		q = q.Where("schedule_id = ?", f.ScheduleID)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.Limit > 0 {
		q = q.Limit(f.Limit)
	}
	return bs, mapErr(q.Scan(ctx))
}

// FinishBackup records the outcome and metadata of a backup.
func (s *Store) FinishBackup(ctx context.Context, b store.Backup) error {
	t := now()
	b.FinishedAt = &t
	res, err := s.db.NewUpdate().Model(&b).
		Column("status", "size_bytes", "sha256", "encrypted", "pg_version", "duration_ms", "error", "finished_at").
		WherePK().Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) SetBackupVerification(ctx context.Context, id string, v store.Verification) error {
	b := store.Backup{ID: id, Verification: v}
	res, err := s.db.NewUpdate().Model(&b).
		Column("verify_status", "verify_error", "verify_details", "verified_at").
		WherePK().Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteBackup(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "backups", id)
}

func (s *Store) ListBackupSchedules(ctx context.Context, serviceID string) ([]store.BackupSchedule, error) {
	ss := []store.BackupSchedule{}
	q := s.db.NewSelect().Model(&ss).Order("created_at")
	if serviceID != "" {
		q = q.Where("service_id = ?", serviceID)
	}
	return ss, mapErr(q.Scan(ctx))
}

func (s *Store) GetBackupSchedule(ctx context.Context, id string) (store.BackupSchedule, error) {
	var sc store.BackupSchedule
	err := s.db.NewSelect().Model(&sc).Where("id = ?", id).Scan(ctx)
	return sc, mapErr(err)
}

func (s *Store) CreateBackupSchedule(ctx context.Context, sc store.BackupSchedule) (store.BackupSchedule, error) {
	sc.ID, sc.CreatedAt = ids.New(), now()
	if _, err := s.db.NewInsert().Model(&sc).Exec(ctx); err != nil {
		return store.BackupSchedule{}, mapErr(err)
	}
	return sc, nil
}

func (s *Store) UpdateBackupSchedule(ctx context.Context, sc store.BackupSchedule) (store.BackupSchedule, error) {
	res, err := s.db.NewUpdate().Model(&sc).
		Column("target_id", "cron", "keep_last", "keep_daily", "keep_weekly", "keep_monthly", "enabled", "verify").
		WherePK().Exec(ctx)
	if err != nil {
		return store.BackupSchedule{}, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.BackupSchedule{}, store.ErrNotFound
	}
	return sc, nil
}

func (s *Store) DeleteBackupSchedule(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "backup_schedules", id)
}

func (s *Store) CreateRestore(ctx context.Context, r store.Restore) (store.Restore, error) {
	r.ID, r.CreatedAt = ids.New(), now()
	if _, err := s.db.NewInsert().Model(&r).Exec(ctx); err != nil {
		return store.Restore{}, mapErr(err)
	}
	return r, nil
}

func (s *Store) ListRestores(ctx context.Context, serviceID string, limit int) ([]store.Restore, error) {
	rs := []store.Restore{}
	err := s.db.NewSelect().Model(&rs).Where("service_id = ?", serviceID).
		Order("created_at DESC").Limit(limit).Scan(ctx)
	return rs, mapErr(err)
}

func (s *Store) FinishRestore(ctx context.Context, id string, status store.OpStatus, errMsg string) error {
	res, err := s.db.NewUpdate().Model((*store.Restore)(nil)).
		Set("status = ?", status).Set("error = ?", errMsg).Set("finished_at = ?", now()).
		Where("id = ?", id).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) FailRunningOperations(ctx context.Context, errMsg string) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for _, model := range []any{(*store.Backup)(nil), (*store.Restore)(nil)} {
			_, err := tx.NewUpdate().Model(model).
				Set("status = ?", store.OpFailed).Set("error = ?", errMsg).Set("finished_at = ?", now()).
				Where("status = ?", store.OpRunning).Exec(ctx)
			if err != nil {
				return mapErr(err)
			}
		}
		_, err := tx.NewUpdate().Model((*store.Backup)(nil)).
			Set("verify_status = ?", store.OpFailed).Set("verify_error = ?", errMsg).Set("verified_at = ?", now()).
			Where("verify_status = ?", store.OpRunning).Exec(ctx)
		return mapErr(err)
	})
}

func (s *Store) CreateAPIToken(ctx context.Context, t store.APIToken) (store.APIToken, error) {
	t.ID, t.CreatedAt = ids.New(), now()
	if _, err := s.db.NewInsert().Model(&t).Exec(ctx); err != nil {
		return store.APIToken{}, mapErr(err)
	}
	return t, nil
}

func (s *Store) ListAPITokens(ctx context.Context) ([]store.APIToken, error) {
	ts := []store.APIToken{}
	err := s.db.NewSelect().Model(&ts).Order("created_at DESC").Scan(ctx)
	return ts, mapErr(err)
}

func (s *Store) GetAPITokenByHash(ctx context.Context, hash string) (store.APIToken, error) {
	var t store.APIToken
	if err := s.db.NewSelect().Model(&t).Where("token_hash = ?", hash).Scan(ctx); err != nil {
		return store.APIToken{}, mapErr(err)
	}
	// Minute resolution is plenty and keeps writes rare.
	if t.LastUsedAt == nil || time.Since(*t.LastUsedAt) > time.Minute {
		used := now()
		t.LastUsedAt = &used
		_, _ = s.db.NewUpdate().Model(&t).Column("last_used_at").WherePK().Exec(ctx)
	}
	return t, nil
}

func (s *Store) DeleteAPIToken(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "api_tokens", id)
}

func (s *Store) CreateDomain(ctx context.Context, d store.Domain) (store.Domain, error) {
	d.ID, d.CreatedAt = ids.New(), now()
	if _, err := s.db.NewInsert().Model(&d).Exec(ctx); err != nil {
		return store.Domain{}, mapErr(err)
	}
	return d, nil
}

func (s *Store) ListDomains(ctx context.Context) ([]store.Domain, error) {
	ds := []store.Domain{}
	err := s.db.NewSelect().Model(&ds).Order("name").Scan(ctx)
	return ds, mapErr(err)
}

func (s *Store) SetDomainProxied(ctx context.Context, id string, proxied bool) (store.Domain, error) {
	d := store.Domain{ID: id, Proxied: proxied}
	res, err := s.db.NewUpdate().Model(&d).Column("proxied").WherePK().Exec(ctx)
	if err != nil {
		return store.Domain{}, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.Domain{}, store.ErrNotFound
	}
	err = s.db.NewSelect().Model(&d).WherePK().Scan(ctx)
	return d, mapErr(err)
}

func (s *Store) DeleteDomain(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "domains", id)
}

func (s *Store) ListNotificationChannels(ctx context.Context) ([]store.NotificationChannel, error) {
	chs := []store.NotificationChannel{}
	err := s.db.NewSelect().Model(&chs).Order("name").Scan(ctx)
	return chs, mapErr(err)
}

func (s *Store) GetNotificationChannel(ctx context.Context, id string) (store.NotificationChannel, error) {
	var ch store.NotificationChannel
	err := s.db.NewSelect().Model(&ch).Where("id = ?", id).Scan(ctx)
	return ch, mapErr(err)
}

func (s *Store) CreateNotificationChannel(ctx context.Context, ch store.NotificationChannel) (store.NotificationChannel, error) {
	ch.ID, ch.CreatedAt = ids.New(), now()
	if _, err := s.db.NewInsert().Model(&ch).Exec(ctx); err != nil {
		return store.NotificationChannel{}, mapErr(err)
	}
	return ch, nil
}

func (s *Store) UpdateNotificationChannel(ctx context.Context, ch store.NotificationChannel) (store.NotificationChannel, error) {
	res, err := s.db.NewUpdate().Model(&ch).Column("name", "config", "events", "enabled").WherePK().Exec(ctx)
	if err != nil {
		return store.NotificationChannel{}, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.NotificationChannel{}, store.ErrNotFound
	}
	err = s.db.NewSelect().Model(&ch).WherePK().Scan(ctx)
	return ch, mapErr(err)
}

func (s *Store) DeleteNotificationChannel(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "notification_channels", id)
}

func (s *Store) AddAudit(ctx context.Context, e store.AuditEntry) error {
	e.ID, e.CreatedAt = ids.New(), now()
	_, err := s.db.NewInsert().Model(&e).Exec(ctx)
	return mapErr(err)
}

func (s *Store) ListAudit(ctx context.Context, limit int) ([]store.AuditEntry, error) {
	es := []store.AuditEntry{}
	err := s.db.NewSelect().Model(&es).Order("created_at DESC").Limit(limit).Scan(ctx)
	return es, mapErr(err)
}

func (s *Store) PruneAudit(ctx context.Context, before time.Time) error {
	_, err := s.db.NewDelete().Model((*store.AuditEntry)(nil)).
		Where("created_at < ?", before.UTC().Truncate(time.Microsecond)).Exec(ctx)
	return mapErr(err)
}

func (s *Store) ListServers(ctx context.Context) ([]store.Server, error) {
	ss := []store.Server{}
	err := s.db.NewSelect().Model(&ss).OrderExpr("kind = 'local' DESC, name").Scan(ctx)
	return ss, mapErr(err)
}

func (s *Store) GetServer(ctx context.Context, id string) (store.Server, error) {
	var sv store.Server
	err := s.db.NewSelect().Model(&sv).Where("id = ?", id).Scan(ctx)
	return sv, mapErr(err)
}

func (s *Store) CreateServer(ctx context.Context, sv store.Server) (store.Server, error) {
	sv.ID, sv.CreatedAt = ids.New(), now()
	if _, err := s.db.NewInsert().Model(&sv).Exec(ctx); err != nil {
		return store.Server{}, mapErr(err)
	}
	return sv, nil
}

func (s *Store) UpdateServer(ctx context.Context, sv store.Server) (store.Server, error) {
	res, err := s.db.NewUpdate().Model(&sv).
		Column("name", "host", "port", "ssh_user", "socket", "host_key", "public_ip", "tunnel_token").WherePK().Exec(ctx)
	if err != nil {
		return store.Server{}, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.Server{}, store.ErrNotFound
	}
	return sv, nil
}

func (s *Store) DeleteServer(ctx context.Context, id string) error {
	n, err := s.db.NewSelect().Model((*store.Project)(nil)).Where("server_id = ?", id).Count(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n > 0 {
		return fmt.Errorf("%w: %d projects run on this server", store.ErrConflict, n)
	}
	return deleteByID(ctx, s.db, "servers", id)
}

func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&v)
	return v, mapErr(err)
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value",
		key, value)
	return mapErr(err)
}

// now matches the microsecond precision bun persists, so returned structs
// equal what a later read yields.
func now() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}

func deleteByID(ctx context.Context, db *bun.DB, table, id string) error {
	res, err := db.NewDelete().TableExpr(table).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() {
		case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
			return fmt.Errorf("%w: %v", store.ErrConflict, err)
		}
	}
	return err
}
