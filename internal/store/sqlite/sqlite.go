// Package sqlite implements store.Store on SQLite (modernc.org/sqlite, no CGO).
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
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

	if err := migrate(ctx, sqldb); err != nil {
		sqldb.Close()
		return nil, err
	}
	return &Store{db: bun.NewDB(sqldb, sqlitedialect.New())}, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, db, sub)
	if err != nil {
		return fmt.Errorf("goose: %w", err)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
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

func (s *Store) DeleteProject(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "projects", id)
}

func (s *Store) CreateService(ctx context.Context, svc store.Service) (store.Service, error) {
	now := now()
	svc.ID, svc.CreatedAt, svc.UpdatedAt = ids.New(), now, now
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
		Column("image", "replicas", "port", "domain", "env", "memory_mb", "database_id", "updated_at").
		WherePK().Exec(ctx)
	if err != nil {
		return store.Service{}, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.Service{}, store.ErrNotFound
	}
	return svc, nil
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
		Column("name", "endpoint", "region", "bucket", "prefix", "access_key", "secret_key", "use_ssl").
		WherePK().Exec(ctx)
	if err != nil {
		return store.BackupTarget{}, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.BackupTarget{}, store.ErrNotFound
	}
	return t, nil
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
		Column("target_id", "cron", "keep_last", "keep_daily", "keep_weekly", "keep_monthly", "enabled").
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
		return nil
	})
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
