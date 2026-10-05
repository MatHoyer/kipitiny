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
	v, err := p.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if err := checkNotAhead(path, v, p.ListSources()); err != nil {
		return err
	}
	pending, err := p.HasPending(ctx)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if !pending {
		return nil
	}
	if v > 0 {
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

// checkNotAhead refuses a database migrated by a newer binary: this one
// would find nothing pending and run on a schema it doesn't know.
func checkNotAhead(path string, v int64, srcs []*goose.Source) error {
	if len(srcs) == 0 {
		return nil
	}
	latest := srcs[len(srcs)-1].Version
	if v <= latest {
		return nil
	}
	msg := fmt.Sprintf("database schema is at %d, this version knows up to %d: run a newer version", v, latest)
	snap := fmt.Sprintf("%s.pre-migrate-%d", path, latest)
	if _, err := os.Stat(snap); err == nil {
		msg += ", or restore " + snap
	}
	return errors.New(msg)
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
	if svc.Secrets == nil {
		svc.Secrets = []string{}
	}
	if svc.Volumes == nil {
		svc.Volumes = []store.Volume{}
	}
	if svc.PublishedPorts == nil {
		svc.PublishedPorts = []store.PublishedPort{}
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
		Column("image", "replicas", "port", "domain", "env", "secrets", "memory_mb", "cpus", "health_path", "pre_deploy", "volumes", "middlewares", "pre_backup", "published_ports", "stop_grace_seconds", "updated_at").
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

func (s *Store) SetDeploymentImage(ctx context.Context, id, image string, config store.Service) error {
	d := store.Deployment{ID: id, Image: image, Config: config}
	res, err := s.db.NewUpdate().Model(&d).Column("image", "config").WherePK().Exec(ctx)
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

func (s *Store) SetServiceImage(ctx context.Context, serviceID, image string) error {
	res, err := s.db.NewUpdate().Model((*store.Service)(nil)).
		Set("image = ?", image).Set("updated_at = ?", now()).Where("id = ?", serviceID).Exec(ctx)
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

func (s *Store) SetTOTP(ctx context.Context, userID, secret string, codeHashes []string) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		res, err := tx.NewUpdate().Model((*store.User)(nil)).
			Set("totp_secret = ?", secret).Set("totp_last_step = 0").Where("id = ?", userID).Exec(ctx)
		if err != nil {
			return mapErr(err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return store.ErrNotFound
		}
		return replaceRecoveryCodes(ctx, tx, userID, codeHashes)
	})
}

func (s *Store) UseTOTPStep(ctx context.Context, userID string, step int64) error {
	res, err := s.db.NewUpdate().Model((*store.User)(nil)).Set("totp_last_step = ?", step).
		Where("id = ?", userID).Where("totp_last_step < ?", step).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrConflict
	}
	return nil
}

func (s *Store) SetRecoveryCodes(ctx context.Context, userID string, codeHashes []string) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return replaceRecoveryCodes(ctx, tx, userID, codeHashes)
	})
}

type recoveryCode struct {
	bun.BaseModel `bun:"table:recovery_codes"`

	CodeHash  string    `bun:"code_hash,pk"`
	UserID    string    `bun:"user_id"`
	CreatedAt time.Time `bun:"created_at"`
}

func replaceRecoveryCodes(ctx context.Context, tx bun.Tx, userID string, codeHashes []string) error {
	if _, err := tx.NewDelete().Model((*recoveryCode)(nil)).Where("user_id = ?", userID).Exec(ctx); err != nil {
		return mapErr(err)
	}
	if len(codeHashes) == 0 {
		return nil
	}
	rows := make([]recoveryCode, len(codeHashes))
	for i, h := range codeHashes {
		rows[i] = recoveryCode{CodeHash: h, UserID: userID, CreatedAt: now()}
	}
	_, err := tx.NewInsert().Model(&rows).Exec(ctx)
	return mapErr(err)
}

func (s *Store) CountRecoveryCodes(ctx context.Context, userID string) (int, error) {
	n, err := s.db.NewSelect().Model((*recoveryCode)(nil)).Where("user_id = ?", userID).Count(ctx)
	return n, mapErr(err)
}

func (s *Store) UseRecoveryCode(ctx context.Context, userID, codeHash string) error {
	res, err := s.db.NewDelete().Model((*recoveryCode)(nil)).
		Where("user_id = ?", userID).Where("code_hash = ?", codeHash).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) CreatePasskey(ctx context.Context, p store.Passkey) (store.Passkey, error) {
	p.ID, p.CreatedAt = ids.New(), now()
	if _, err := s.db.NewInsert().Model(&p).Exec(ctx); err != nil {
		return store.Passkey{}, mapErr(err)
	}
	return p, nil
}

func (s *Store) ListPasskeys(ctx context.Context, userID string) ([]store.Passkey, error) {
	ps := []store.Passkey{}
	err := s.db.NewSelect().Model(&ps).Where("user_id = ?", userID).Order("created_at").Scan(ctx)
	return ps, mapErr(err)
}

func (s *Store) GetPasskeyByCredentialID(ctx context.Context, credentialID string) (store.Passkey, error) {
	var p store.Passkey
	err := s.db.NewSelect().Model(&p).Where("credential_id = ?", credentialID).Scan(ctx)
	return p, mapErr(err)
}

func (s *Store) PasskeyUsed(ctx context.Context, id, credential string) error {
	_, err := s.db.NewUpdate().Model((*store.Passkey)(nil)).
		Set("credential = ?", credential).Set("last_used_at = ?", now()).Where("id = ?", id).Exec(ctx)
	return mapErr(err)
}

func (s *Store) DeletePasskey(ctx context.Context, userID, id string) error {
	res, err := s.db.NewDelete().Model((*store.Passkey)(nil)).Where("id = ?", id).Where("user_id = ?", userID).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
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

func (s *Store) SetBackupTargetKey(ctx context.Context, id, identity, recipient string) error {
	res, err := s.db.NewUpdate().Table("backup_targets").
		Set("age_identity = ?", identity).Set("age_recipient = ?", recipient).
		Where("id = ?", id).Where("age_recipient = ''").Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	if _, err := s.GetBackupTarget(ctx, id); err != nil {
		return err
	}
	return fmt.Errorf("%w: already encrypted", store.ErrConflict)
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
	if sc.Kind == "" {
		sc.Kind = store.BackupKindPostgres
	}
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

func (s *Store) ListRegistries(ctx context.Context) ([]store.Registry, error) {
	rs := []store.Registry{}
	err := s.db.NewSelect().Model(&rs).Order("host").Scan(ctx)
	return rs, mapErr(err)
}

func (s *Store) GetRegistry(ctx context.Context, id string) (store.Registry, error) {
	var r store.Registry
	err := s.db.NewSelect().Model(&r).Where("id = ?", id).Scan(ctx)
	return r, mapErr(err)
}

func (s *Store) CreateRegistry(ctx context.Context, r store.Registry) (store.Registry, error) {
	r.ID, r.CreatedAt = ids.New(), now()
	if _, err := s.db.NewInsert().Model(&r).Exec(ctx); err != nil {
		return store.Registry{}, mapErr(err)
	}
	return r, nil
}

func (s *Store) UpdateRegistry(ctx context.Context, r store.Registry) (store.Registry, error) {
	res, err := s.db.NewUpdate().Model(&r).Column("host", "username", "password").WherePK().Exec(ctx)
	if err != nil {
		return store.Registry{}, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.Registry{}, store.ErrNotFound
	}
	err = s.db.NewSelect().Model(&r).WherePK().Scan(ctx)
	return r, mapErr(err)
}

func (s *Store) DeleteRegistry(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "registries", id)
}

func (s *Store) ListUptimeChecks(ctx context.Context) ([]store.UptimeCheck, error) {
	cs := []store.UptimeCheck{}
	err := s.db.NewSelect().Model(&cs).Order("created_at").Scan(ctx)
	return cs, mapErr(err)
}

func (s *Store) GetUptimeCheck(ctx context.Context, serviceID string) (store.UptimeCheck, error) {
	var c store.UptimeCheck
	err := s.db.NewSelect().Model(&c).Where("service_id = ?", serviceID).Scan(ctx)
	return c, mapErr(err)
}

func (s *Store) SaveUptimeCheck(ctx context.Context, c store.UptimeCheck) (store.UptimeCheck, error) {
	c.CreatedAt = now()
	_, err := s.db.NewInsert().Model(&c).
		On("CONFLICT (service_id) DO UPDATE").
		Set("path = EXCLUDED.path").
		Set("interval_sec = EXCLUDED.interval_sec").
		Set("timeout_sec = EXCLUDED.timeout_sec").
		Set("expected_status = EXCLUDED.expected_status").
		Set("enabled = EXCLUDED.enabled").
		Exec(ctx)
	if err != nil {
		return store.UptimeCheck{}, mapErr(err)
	}
	return s.GetUptimeCheck(ctx, c.ServiceID)
}

func (s *Store) SetUptimeState(ctx context.Context, serviceID string, down bool, since time.Time) error {
	res, err := s.db.NewUpdate().Model((*store.UptimeCheck)(nil)).
		Set("down = ?", down).Set("changed_at = ?", since.UTC().Truncate(time.Microsecond)).
		Where("service_id = ?", serviceID).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteUptimeCheck(ctx context.Context, serviceID string) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		res, err := tx.NewDelete().Model((*store.UptimeCheck)(nil)).Where("service_id = ?", serviceID).Exec(ctx)
		if err != nil {
			return mapErr(err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return store.ErrNotFound
		}
		_, err = tx.NewDelete().Model((*store.UptimeHour)(nil)).Where("service_id = ?", serviceID).Exec(ctx)
		return mapErr(err)
	})
}

func (s *Store) AddUptimeResult(ctx context.Context, serviceID string, at time.Time, ok bool, latency time.Duration) error {
	h := store.UptimeHour{ServiceID: serviceID, Hour: at.UTC().Truncate(time.Hour), Checks: 1}
	if ok {
		h.LatencyMS = latency.Milliseconds()
	} else {
		h.Failures = 1
	}
	_, err := s.db.NewInsert().Model(&h).
		On("CONFLICT (service_id, hour) DO UPDATE").
		Set("checks = uptime_hour.checks + 1").
		Set("failures = uptime_hour.failures + EXCLUDED.failures").
		Set("latency_ms = uptime_hour.latency_ms + EXCLUDED.latency_ms").
		Exec(ctx)
	return mapErr(err)
}

func (s *Store) ListUptimeHours(ctx context.Context, serviceID string, since time.Time) ([]store.UptimeHour, error) {
	hs := []store.UptimeHour{}
	err := s.db.NewSelect().Model(&hs).Where("service_id = ?", serviceID).
		Where("hour >= ?", since.UTC().Truncate(time.Hour)).Order("hour").Scan(ctx)
	return hs, mapErr(err)
}

func (s *Store) PruneUptime(ctx context.Context, before time.Time) error {
	_, err := s.db.NewDelete().Model((*store.UptimeHour)(nil)).
		Where("hour < ?", before.UTC().Truncate(time.Hour)).Exec(ctx)
	return mapErr(err)
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
