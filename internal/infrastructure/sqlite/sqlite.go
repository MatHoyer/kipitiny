// Package sqlite implements domain.Store on SQLite (modernc.org/sqlite, no CGO).
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/kipit-labs/kipitiny/internal/anonymize"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/ids"
	"github.com/kipit-labs/kipitiny/internal/sqlitedb"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct {
	db *bun.DB
}

var _ domain.Store = (*Store)(nil)

// Open opens (or creates) the database at path and applies pending migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, err
	}
	sqldb, err := sqlitedb.Open(ctx, path, sub)
	if err != nil {
		return nil, err
	}
	return &Store{db: bun.NewDB(sqldb, sqlitedialect.New())}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Snapshot uses VACUUM INTO: a consistent, compacted copy taken without
// blocking writers for long.
func (s *Store) Snapshot(ctx context.Context, path string) error {
	_, err := s.db.ExecContext(ctx, "VACUUM INTO ?", path)
	return err
}

func (s *Store) CreateProject(ctx context.Context, p domain.Project) (domain.Project, error) {
	now := now()
	p.ID, p.CreatedAt, p.UpdatedAt = ids.New(), now, now
	if p.ServerID == "" {
		p.ServerID = domain.LocalServerID
	}
	if p.Env == nil {
		p.Env = map[string]string{}
	}
	if p.Secrets == nil {
		p.Secrets = []string{}
	}
	if p.Tags == nil {
		p.Tags = []string{}
	}
	if _, err := s.db.NewInsert().Model(&p).Exec(ctx); err != nil {
		return domain.Project{}, mapErr(err)
	}
	return p, nil
}

func (s *Store) GetProject(ctx context.Context, id string) (domain.Project, error) {
	var p domain.Project
	err := s.db.NewSelect().Model(&p).Where("id = ?", id).Scan(ctx)
	return p, mapErr(err)
}

func (s *Store) ListProjects(ctx context.Context) ([]domain.Project, error) {
	ps := []domain.Project{}
	err := s.db.NewSelect().Model(&ps).Order("name").Scan(ctx)
	return ps, mapErr(err)
}

func (s *Store) RenameProject(ctx context.Context, id, name string) error {
	res, err := s.db.NewUpdate().Model((*domain.Project)(nil)).
		Set("name = ?", name).Set("updated_at = ?", now()).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) SetProjectTags(ctx context.Context, id string, tags []string) error {
	if tags == nil {
		tags = []string{}
	}
	pr := domain.Project{ID: id, Tags: tags, UpdatedAt: now()}
	res, err := s.db.NewUpdate().Model(&pr).Column("tags", "updated_at").WherePK().Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) SetProjectPreview(ctx context.Context, id string, p domain.Preview) error {
	pr := domain.Project{ID: id, Preview: p, UpdatedAt: now()}
	res, err := s.db.NewUpdate().Model(&pr).Column("preview", "updated_at").WherePK().Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) SetProjectSleep(ctx context.Context, id string, sleepAfterSeconds int) error {
	pr := domain.Project{ID: id, SleepAfterSeconds: sleepAfterSeconds, UpdatedAt: now()}
	res, err := s.db.NewUpdate().Model(&pr).Column("sleep_after_seconds", "updated_at").WherePK().Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) SetProjectSleeping(ctx context.Context, id string, sleeping bool) error {
	q := s.db.NewUpdate().Model((*domain.Service)(nil)).Set("sleeping = ?", sleeping).Where("project_id = ?", id)
	if sleeping {
		q = q.Where("stopped = ?", false).Where("current_deployment_id != ''")
	}
	_, err := q.Exec(ctx)
	return mapErr(err)
}

func (s *Store) GetPreviewSettings(ctx context.Context, projectID string) (domain.PreviewSettings, error) {
	var ps domain.PreviewSettings
	err := s.db.NewSelect().Model(&ps).Where("project_id = ?", projectID).Scan(ctx)
	return ps, mapErr(err)
}

func (s *Store) SavePreviewSettings(ctx context.Context, ps domain.PreviewSettings) (domain.PreviewSettings, error) {
	ps.UpdatedAt = now()
	if ps.Env == nil {
		ps.Env = map[string]string{}
	}
	if ps.Secrets == nil {
		ps.Secrets = []string{}
	}
	if ps.Inherit == nil {
		ps.Inherit = []string{}
	}
	_, err := s.db.NewInsert().Model(&ps).On("CONFLICT (project_id) DO UPDATE").
		Set("base_domain = EXCLUDED.base_domain").Set("env = EXCLUDED.env").Set("secrets = EXCLUDED.secrets").
		Set("inherit = EXCLUDED.inherit").Set("ttl_seconds = EXCLUDED.ttl_seconds").
		Set("sleep_after_seconds = EXCLUDED.sleep_after_seconds").Set("seed_mode = EXCLUDED.seed_mode").Set("salt = EXCLUDED.salt").
		Set("updated_at = EXCLUDED.updated_at").Exec(ctx)
	return ps, mapErr(err)
}

func (s *Store) SetProjectEnv(ctx context.Context, id string, env map[string]string, secrets []string) (domain.Project, error) {
	if env == nil {
		env = map[string]string{}
	}
	if secrets == nil {
		secrets = []string{}
	}
	p := domain.Project{ID: id, Env: env, Secrets: secrets, UpdatedAt: now()}
	res, err := s.db.NewUpdate().Model(&p).Column("env", "secrets", "updated_at").WherePK().Exec(ctx)
	if err != nil {
		return domain.Project{}, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.Project{}, domain.ErrNotFound
	}
	return s.GetProject(ctx, id)
}

func (s *Store) DeleteProject(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "projects", id)
}

func (s *Store) CreateService(ctx context.Context, svc domain.Service) (domain.Service, error) {
	now := now()
	svc.ID, svc.CreatedAt, svc.UpdatedAt = ids.New(), now, now
	if svc.Secrets == nil {
		svc.Secrets = []string{}
	}
	if svc.Volumes == nil {
		svc.Volumes = []domain.Volume{}
	}
	if svc.PublishedPorts == nil {
		svc.PublishedPorts = []domain.PublishedPort{}
	}
	if svc.Routes == nil {
		svc.Routes = []domain.Route{}
	}
	if svc.Files == nil {
		svc.Files = []domain.ServiceFile{}
	}
	if svc.DependsOn == nil {
		svc.DependsOn = []domain.Dependency{}
	}
	if svc.ProjectNetworks == nil {
		svc.ProjectNetworks = []string{}
	}
	if svc.Networks == nil {
		svc.Networks = []string{}
	}
	if svc.Tags == nil {
		svc.Tags = []string{}
	}
	// Services always run on their project's server.
	err := s.db.NewSelect().Model((*domain.Project)(nil)).Column("server_id").
		Where("id = ?", svc.ProjectID).Scan(ctx, &svc.ServerID)
	if err != nil {
		return domain.Service{}, mapErr(err)
	}
	if _, err := s.db.NewInsert().Model(&svc).Exec(ctx); err != nil {
		return domain.Service{}, mapErr(err)
	}
	return svc, nil
}

func (s *Store) GetService(ctx context.Context, id string) (domain.Service, error) {
	var svc domain.Service
	err := s.db.NewSelect().Model(&svc).Where("id = ?", id).Scan(ctx)
	return svc, mapErr(err)
}

func (s *Store) ListServices(ctx context.Context, projectID string) ([]domain.Service, error) {
	svcs := []domain.Service{}
	err := s.db.NewSelect().Model(&svcs).
		Where("project_id = ?", projectID).Order("name").Scan(ctx)
	return svcs, mapErr(err)
}

func (s *Store) UpdateService(ctx context.Context, svc domain.Service) (domain.Service, error) {
	svc.UpdatedAt = now()
	if svc.Routes == nil {
		svc.Routes = []domain.Route{}
	}
	if svc.Tags == nil {
		svc.Tags = []string{}
	}
	if svc.Files == nil {
		svc.Files = []domain.ServiceFile{}
	}
	if svc.DependsOn == nil {
		svc.DependsOn = []domain.Dependency{}
	}
	if svc.ProjectNetworks == nil {
		svc.ProjectNetworks = []string{}
	}
	res, err := s.db.NewUpdate().Model(&svc).
		Column("image", "icon", "tags", "anonymize", "replicas", "port", "domain", "analytics", "analytics_since", "analytics_script", "analytics_hit", "errors", "errors_project", "errors_key", "env", "secrets", "memory_mb", "cpus", "health_path", "healthcheck", "pre_deploy", "volumes", "middlewares", "pre_backup", "published_ports", "routes", "stop_grace_seconds", "host_network", "docker_socket", "runtime", "files", "depends_on", "project_networks", "preview", "updated_at").
		WherePK().Exec(ctx)
	if err != nil {
		return domain.Service{}, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.Service{}, domain.ErrNotFound
	}
	return svc, nil
}

func (s *Store) RenameService(ctx context.Context, id, name string) error {
	res, err := s.db.NewUpdate().Model((*domain.Service)(nil)).
		Set("name = ?", name).Set("updated_at = ?", now()).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) SetServiceAnalyticsShare(ctx context.Context, id, hash string) error {
	svc := domain.Service{ID: id, AnalyticsShare: hash, UpdatedAt: now()}
	if hash != "" {
		svc.AnalyticsSharedAt = now()
	}
	res, err := s.db.NewUpdate().Model(&svc).Column("analytics_share", "analytics_shared_at", "updated_at").WherePK().Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) GetServiceByAnalyticsShare(ctx context.Context, hash string) (domain.Service, error) {
	var svc domain.Service
	if hash == "" {
		return svc, domain.ErrNotFound
	}
	err := s.db.NewSelect().Model(&svc).Where("analytics_share = ?", hash).Scan(ctx)
	return svc, mapErr(err)
}

func (s *Store) SetServiceTags(ctx context.Context, id string, tags []string) error {
	if tags == nil {
		tags = []string{}
	}
	svc := domain.Service{ID: id, Tags: tags, UpdatedAt: now()}
	res, err := s.db.NewUpdate().Model(&svc).Column("tags", "updated_at").WherePK().Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) SetServiceAnonymize(ctx context.Context, id string, rules anonymize.Rules) error {
	svc := domain.Service{ID: id, Anonymize: rules, UpdatedAt: now()}
	res, err := s.db.NewUpdate().Model(&svc).Column("anonymize", "updated_at").WherePK().Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) SetCurrentDeployment(ctx context.Context, serviceID, deploymentID string) error {
	res, err := s.db.NewUpdate().Model((*domain.Service)(nil)).
		Set("current_deployment_id = ?", deploymentID).Where("id = ?", serviceID).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) SetDeploymentImage(ctx context.Context, id, image string, config domain.Service) error {
	d := domain.Deployment{ID: id, Image: image, Config: config}
	res, err := s.db.NewUpdate().Model(&d).Column("image", "config").WherePK().Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) SetServiceStopped(ctx context.Context, serviceID string, stopped bool) error {
	res, err := s.db.NewUpdate().Model((*domain.Service)(nil)).
		Set("stopped = ?", stopped).Set("sleeping = ?", false).Where("id = ?", serviceID).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) SetServiceImage(ctx context.Context, serviceID, image string) error {
	res, err := s.db.NewUpdate().Model((*domain.Service)(nil)).
		Set("image = ?", image).Set("updated_at = ?", now()).Where("id = ?", serviceID).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) ListAllServices(ctx context.Context) ([]domain.Service, error) {
	svcs := []domain.Service{}
	err := s.db.NewSelect().Model(&svcs).Order("project_id", "name").Scan(ctx)
	return svcs, mapErr(err)
}

func (s *Store) DeleteService(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "services", id)
}

func (s *Store) CreateDeployment(ctx context.Context, d domain.Deployment) (domain.Deployment, error) {
	d.ID, d.CreatedAt = ids.New(), now()
	if _, err := s.db.NewInsert().Model(&d).Exec(ctx); err != nil {
		return domain.Deployment{}, mapErr(err)
	}
	return d, nil
}

func (s *Store) GetDeployment(ctx context.Context, id string) (domain.Deployment, error) {
	var d domain.Deployment
	err := s.db.NewSelect().Model(&d).Where("id = ?", id).Scan(ctx)
	return d, mapErr(err)
}

func (s *Store) ListDeployments(ctx context.Context, serviceID string, limit int) ([]domain.Deployment, error) {
	ds := []domain.Deployment{}
	err := s.db.NewSelect().Model(&ds).Where("service_id = ?", serviceID).
		Order("created_at DESC").Limit(limit).Scan(ctx)
	return ds, mapErr(err)
}

func (s *Store) DeleteDeployment(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "deployments", id)
}

func (s *Store) FinishDeployment(ctx context.Context, id string, status domain.DeploymentStatus, errMsg string) error {
	res, err := s.db.NewUpdate().Model((*domain.Deployment)(nil)).
		Set("status = ?", status).Set("error = ?", errMsg).Set("finished_at = ?", now()).
		Where("id = ?", id).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) FailRunningDeployments(ctx context.Context, errMsg string) (int, error) {
	res, err := s.db.NewUpdate().Model((*domain.Deployment)(nil)).
		Set("status = ?", domain.DeploymentFailed).Set("error = ?", errMsg).Set("finished_at = ?", now()).
		Where("status = ?", domain.DeploymentRunning).Exec(ctx)
	if err != nil {
		return 0, mapErr(err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	n, err := s.db.NewSelect().Model((*domain.User)(nil)).Count(ctx)
	return n, mapErr(err)
}

func (s *Store) CreateUser(ctx context.Context, u domain.User) (domain.User, error) {
	return createUser(ctx, s.db, u)
}

func createUser(ctx context.Context, db bun.IDB, u domain.User) (domain.User, error) {
	u.ID, u.CreatedAt = ids.New(), now()
	if u.Role == "" {
		u.Role = domain.RoleMember
	}
	if u.Features == nil {
		u.Features = []string{}
	}
	if _, err := db.NewInsert().Model(&u).Exec(ctx); err != nil {
		return domain.User{}, mapErr(err)
	}
	return u, nil
}

func (s *Store) ListUsers(ctx context.Context) ([]domain.User, error) {
	us := []domain.User{}
	err := s.db.NewSelect().Model(&us).Order("created_at", "id").Scan(ctx)
	return us, mapErr(err)
}

func (s *Store) UpdateUserAccess(ctx context.Context, id string, role domain.Role, features []string) (domain.User, error) {
	if features == nil {
		features = []string{}
	}
	u := domain.User{ID: id, Role: role, Features: features}
	res, err := s.db.NewUpdate().Model(&u).Column("role", "features").WherePK().Exec(ctx)
	if err != nil {
		return domain.User{}, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.User{}, domain.ErrNotFound
	}
	return s.GetUser(ctx, id)
}

func (s *Store) SetUserPreferences(ctx context.Context, id string, p domain.Preferences) error {
	res, err := s.db.NewUpdate().Model(&domain.User{ID: id, Preferences: p}).Column("preferences").WherePK().Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteUser(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "users", id)
}

func (s *Store) DeleteUserSessions(ctx context.Context, userID string) error {
	_, err := s.db.NewDelete().Model((*domain.Session)(nil)).Where("user_id = ?", userID).Exec(ctx)
	return mapErr(err)
}

func (s *Store) ListGrants(ctx context.Context, userID string) ([]domain.ProjectGrant, error) {
	gs := []domain.ProjectGrant{}
	err := s.db.NewSelect().Model(&gs).Where("user_id = ?", userID).Order("created_at").Scan(ctx)
	return gs, mapErr(err)
}

func (s *Store) SetGrant(ctx context.Context, g domain.ProjectGrant) error {
	return setGrant(ctx, s.db, g)
}

func setGrant(ctx context.Context, db bun.IDB, g domain.ProjectGrant) error {
	if len(g.Perms) == 0 {
		_, err := db.NewDelete().Model((*domain.ProjectGrant)(nil)).
			Where("user_id = ? AND project_id = ?", g.UserID, g.ProjectID).Exec(ctx)
		return mapErr(err)
	}
	g.CreatedAt = now()
	_, err := db.NewInsert().Model(&g).On("CONFLICT (user_id, project_id) DO UPDATE").Set("perms = EXCLUDED.perms").Exec(ctx)
	return mapErr(err)
}

func (s *Store) CreateInvite(ctx context.Context, i domain.Invite) (domain.Invite, error) {
	i.ID, i.CreatedAt, i.UsedAt = ids.New(), now(), nil
	if i.Features == nil {
		i.Features = []string{}
	}
	if i.Grants == nil {
		i.Grants = map[string][]string{}
	}
	if _, err := s.db.NewInsert().Model(&i).Exec(ctx); err != nil {
		return domain.Invite{}, mapErr(err)
	}
	return i, nil
}

func (s *Store) ListInvites(ctx context.Context) ([]domain.Invite, error) {
	is := []domain.Invite{}
	err := s.db.NewSelect().Model(&is).Where("used_at IS NULL AND expires_at > ?", now()).Order("created_at DESC").Scan(ctx)
	return is, mapErr(err)
}

func (s *Store) GetInviteByHash(ctx context.Context, hash string) (domain.Invite, error) {
	var i domain.Invite
	err := s.db.NewSelect().Model(&i).Where("token_hash = ? AND used_at IS NULL AND expires_at > ?", hash, now()).Scan(ctx)
	return i, mapErr(err)
}

func (s *Store) AcceptInvite(ctx context.Context, inviteID string, u domain.User, sso *domain.SSOIdentity) (domain.User, error) {
	var created domain.User
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var i domain.Invite
		res, err := tx.NewUpdate().Model(&i).Set("used_at = ?", now()).
			Where("id = ? AND used_at IS NULL AND expires_at > ?", inviteID, now()).Returning("*").Exec(ctx)
		if err != nil {
			return mapErr(err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return domain.ErrNotFound
		}
		u.Role, u.Features = i.Role, i.Features
		if created, err = createUser(ctx, tx, u); err != nil {
			return err
		}
		if sso != nil {
			id := *sso
			id.ID, id.UserID, id.CreatedAt = ids.New(), created.ID, now()
			if _, err := tx.NewInsert().Model(&id).Exec(ctx); err != nil {
				return mapErr(err)
			}
		}
		for projectID, perms := range i.Grants {
			// A project deleted since the invite was made is skipped.
			if exists, err := tx.NewSelect().Table("projects").Where("id = ?", projectID).Exists(ctx); err != nil {
				return mapErr(err)
			} else if !exists {
				continue
			}
			if err := setGrant(ctx, tx, domain.ProjectGrant{UserID: created.ID, ProjectID: projectID, Perms: perms}); err != nil {
				return err
			}
		}
		return nil
	})
	return created, err
}

func (s *Store) DeleteInvite(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "invites", id)
}

func (s *Store) GetUser(ctx context.Context, id string) (domain.User, error) {
	var u domain.User
	err := s.db.NewSelect().Model(&u).Where("id = ?", id).Scan(ctx)
	return u, mapErr(err)
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (domain.User, error) {
	var u domain.User
	err := s.db.NewSelect().Model(&u).Where("username = ?", username).Scan(ctx)
	return u, mapErr(err)
}

func (s *Store) SetPassword(ctx context.Context, userID, passwordHash string) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		res, err := tx.NewUpdate().Model((*domain.User)(nil)).
			Set("password_hash = ?", passwordHash).Where("id = ?", userID).Exec(ctx)
		if err != nil {
			return mapErr(err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return domain.ErrNotFound
		}
		_, err = tx.NewDelete().Model((*domain.Session)(nil)).Where("user_id = ?", userID).Exec(ctx)
		return mapErr(err)
	})
}

func (s *Store) SetTOTP(ctx context.Context, userID, secret string, codeHashes []string) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		res, err := tx.NewUpdate().Model((*domain.User)(nil)).
			Set("totp_secret = ?", secret).Set("totp_last_step = 0").Where("id = ?", userID).Exec(ctx)
		if err != nil {
			return mapErr(err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return domain.ErrNotFound
		}
		return replaceRecoveryCodes(ctx, tx, userID, codeHashes)
	})
}

func (s *Store) UseTOTPStep(ctx context.Context, userID string, step int64) error {
	res, err := s.db.NewUpdate().Model((*domain.User)(nil)).Set("totp_last_step = ?", step).
		Where("id = ?", userID).Where("totp_last_step < ?", step).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrConflict
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
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) CreatePasskey(ctx context.Context, p domain.Passkey) (domain.Passkey, error) {
	p.ID, p.CreatedAt = ids.New(), now()
	if _, err := s.db.NewInsert().Model(&p).Exec(ctx); err != nil {
		return domain.Passkey{}, mapErr(err)
	}
	return p, nil
}

func (s *Store) ListPasskeys(ctx context.Context, userID string) ([]domain.Passkey, error) {
	ps := []domain.Passkey{}
	err := s.db.NewSelect().Model(&ps).Where("user_id = ?", userID).Order("created_at").Scan(ctx)
	return ps, mapErr(err)
}

func (s *Store) GetPasskeyByCredentialID(ctx context.Context, credentialID string) (domain.Passkey, error) {
	var p domain.Passkey
	err := s.db.NewSelect().Model(&p).Where("credential_id = ?", credentialID).Scan(ctx)
	return p, mapErr(err)
}

func (s *Store) PasskeyUsed(ctx context.Context, id, credential string) error {
	_, err := s.db.NewUpdate().Model((*domain.Passkey)(nil)).
		Set("credential = ?", credential).Set("last_used_at = ?", now()).Where("id = ?", id).Exec(ctx)
	return mapErr(err)
}

func (s *Store) DeletePasskey(ctx context.Context, userID, id string) error {
	res, err := s.db.NewDelete().Model((*domain.Passkey)(nil)).Where("id = ?", id).Where("user_id = ?", userID).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) SavePushSubscription(ctx context.Context, p domain.PushSubscription) (domain.PushSubscription, error) {
	p.ID, p.CreatedAt = ids.New(), now()
	_, err := s.db.NewInsert().Model(&p).On("CONFLICT (endpoint) DO UPDATE").
		Set("user_id = EXCLUDED.user_id").Set("name = EXCLUDED.name").
		Set("p256dh = EXCLUDED.p256dh").Set("auth = EXCLUDED.auth").
		Returning("*").Exec(ctx)
	return p, mapErr(err)
}

func (s *Store) ListPushSubscriptions(ctx context.Context, userID string) ([]domain.PushSubscription, error) {
	ps := []domain.PushSubscription{}
	err := s.db.NewSelect().Model(&ps).Where("user_id = ?", userID).Order("created_at").Scan(ctx)
	return ps, mapErr(err)
}

func (s *Store) ListAdminPushSubscriptions(ctx context.Context) ([]domain.PushSubscription, error) {
	ps := []domain.PushSubscription{}
	err := s.db.NewSelect().Model(&ps).
		Where("user_id IN (SELECT id FROM users WHERE role = ?)", domain.RoleAdmin).Order("created_at").Scan(ctx)
	return ps, mapErr(err)
}

func (s *Store) DeletePushSubscription(ctx context.Context, userID, id string) error {
	res, err := s.db.NewDelete().Model((*domain.PushSubscription)(nil)).Where("id = ?", id).Where("user_id = ?", userID).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) CreateSession(ctx context.Context, sess domain.Session) error {
	sess.CreatedAt = now()
	sess.ExpiresAt = sess.ExpiresAt.UTC().Truncate(time.Microsecond)
	_, err := s.db.NewInsert().Model(&sess).Exec(ctx)
	return mapErr(err)
}

func (s *Store) GetSession(ctx context.Context, tokenHash string) (domain.Session, error) {
	var sess domain.Session
	err := s.db.NewSelect().Model(&sess).
		Where("token_hash = ?", tokenHash).Where("expires_at > ?", now()).Scan(ctx)
	return sess, mapErr(err)
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.NewDelete().Model((*domain.Session)(nil)).Where("token_hash = ?", tokenHash).Exec(ctx)
	return mapErr(err)
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	_, err := s.db.NewDelete().Model((*domain.Session)(nil)).Where("expires_at <= ?", now()).Exec(ctx)
	return mapErr(err)
}

func (s *Store) ListBackupTargets(ctx context.Context) ([]domain.BackupTarget, error) {
	ts := []domain.BackupTarget{}
	// Local first, then by name.
	err := s.db.NewSelect().Model(&ts).OrderExpr("kind = 'local' DESC, name").Scan(ctx)
	return ts, mapErr(err)
}

func (s *Store) GetBackupTarget(ctx context.Context, id string) (domain.BackupTarget, error) {
	var t domain.BackupTarget
	err := s.db.NewSelect().Model(&t).Where("id = ?", id).Scan(ctx)
	return t, mapErr(err)
}

func (s *Store) CreateBackupTarget(ctx context.Context, t domain.BackupTarget) (domain.BackupTarget, error) {
	t.ID, t.CreatedAt = ids.New(), now()
	if _, err := s.db.NewInsert().Model(&t).Exec(ctx); err != nil {
		return domain.BackupTarget{}, mapErr(err)
	}
	return t, nil
}

func (s *Store) UpdateBackupTarget(ctx context.Context, t domain.BackupTarget) (domain.BackupTarget, error) {
	res, err := s.db.NewUpdate().Model(&t).
		Column("name", "endpoint", "region", "bucket", "prefix", "access_key", "secret_key", "use_ssl").
		WherePK().Exec(ctx)
	if err != nil {
		return domain.BackupTarget{}, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.BackupTarget{}, domain.ErrNotFound
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
	return fmt.Errorf("%w: already encrypted", domain.ErrConflict)
}

func (s *Store) DeleteBackupTarget(ctx context.Context, id string) error {
	n, err := s.db.NewSelect().Model((*domain.Backup)(nil)).Where("target_id = ?", id).Count(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n > 0 {
		return fmt.Errorf("%w: %d backups are stored on this target", domain.ErrConflict, n)
	}
	return deleteByID(ctx, s.db, "backup_targets", id)
}

func (s *Store) CreateBackup(ctx context.Context, b domain.Backup) (domain.Backup, error) {
	b.ID = ids.New()
	if b.CreatedAt.IsZero() { // set when adopting another manager's backup
		b.CreatedAt = now()
	}
	if b.Kind == "" {
		b.Kind = domain.BackupKindPostgres
	}
	if _, err := s.db.NewInsert().Model(&b).Exec(ctx); err != nil {
		return domain.Backup{}, mapErr(err)
	}
	return b, nil
}

func (s *Store) GetBackup(ctx context.Context, id string) (domain.Backup, error) {
	var b domain.Backup
	err := s.db.NewSelect().Model(&b).Where("id = ?", id).Scan(ctx)
	return b, mapErr(err)
}

func (s *Store) ListBackups(ctx context.Context, f domain.BackupFilter) ([]domain.Backup, error) {
	bs := []domain.Backup{}
	q := s.db.NewSelect().Model(&bs).Order("created_at DESC")
	if f.ServiceID != "" {
		q = q.Where("service_id = ?", f.ServiceID)
	}
	if f.ProjectID != "" {
		q = q.Where("project_id = ?", f.ProjectID)
	}
	if f.ProjectIn != nil {
		if len(f.ProjectIn) == 0 {
			return bs, nil
		}
		q = q.Where("project_id IN (?)", bun.In(f.ProjectIn))
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
	if f.Anonymized {
		q = q.Where("source_id != ''")
	} else {
		q = q.Where("source_id = ''")
	}
	if f.SourceID != "" {
		q = q.Where("source_id = ?", f.SourceID)
	}
	if f.Limit > 0 {
		q = q.Limit(f.Limit)
	}
	return bs, mapErr(q.Scan(ctx))
}

// FinishBackup records the outcome and metadata of a backup.
func (s *Store) FinishBackup(ctx context.Context, b domain.Backup) error {
	t := now()
	b.FinishedAt = &t
	res, err := s.db.NewUpdate().Model(&b).
		Column("status", "size_bytes", "sha256", "encrypted", "pg_version", "extensions", "duration_ms", "error", "finished_at").
		WherePK().Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) SetBackupVerification(ctx context.Context, id string, v domain.Verification) error {
	b := domain.Backup{ID: id, Verification: v}
	res, err := s.db.NewUpdate().Model(&b).
		Column("verify_status", "verify_error", "verify_details", "verified_at").
		WherePK().Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteBackup(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "backups", id)
}

func (s *Store) ListBackupSchedules(ctx context.Context, serviceID string) ([]domain.BackupSchedule, error) {
	ss := []domain.BackupSchedule{}
	q := s.db.NewSelect().Model(&ss).Order("created_at")
	if serviceID != "" {
		q = q.Where("service_id = ?", serviceID)
	}
	return ss, mapErr(q.Scan(ctx))
}

func (s *Store) GetBackupSchedule(ctx context.Context, id string) (domain.BackupSchedule, error) {
	var sc domain.BackupSchedule
	err := s.db.NewSelect().Model(&sc).Where("id = ?", id).Scan(ctx)
	return sc, mapErr(err)
}

func (s *Store) CreateBackupSchedule(ctx context.Context, sc domain.BackupSchedule) (domain.BackupSchedule, error) {
	sc.ID, sc.CreatedAt = ids.New(), now()
	if sc.Kind == "" {
		sc.Kind = domain.BackupKindPostgres
	}
	if _, err := s.db.NewInsert().Model(&sc).Exec(ctx); err != nil {
		return domain.BackupSchedule{}, mapErr(err)
	}
	return sc, nil
}

func (s *Store) UpdateBackupSchedule(ctx context.Context, sc domain.BackupSchedule) (domain.BackupSchedule, error) {
	res, err := s.db.NewUpdate().Model(&sc).
		Column("target_id", "db_name", "cron", "keep_last", "keep_daily", "keep_weekly", "keep_monthly", "enabled", "verify").
		WherePK().Exec(ctx)
	if err != nil {
		return domain.BackupSchedule{}, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.BackupSchedule{}, domain.ErrNotFound
	}
	return sc, nil
}

func (s *Store) DeleteBackupSchedule(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "backup_schedules", id)
}

func (s *Store) ListJobs(ctx context.Context, serviceID string) ([]domain.Job, error) {
	js := []domain.Job{}
	q := s.db.NewSelect().Model(&js).Order("name")
	if serviceID != "" {
		q = q.Where("service_id = ?", serviceID)
	}
	return js, mapErr(q.Scan(ctx))
}

func (s *Store) GetJob(ctx context.Context, id string) (domain.Job, error) {
	var j domain.Job
	err := s.db.NewSelect().Model(&j).Where("id = ?", id).Scan(ctx)
	return j, mapErr(err)
}

func (s *Store) CreateJob(ctx context.Context, j domain.Job) (domain.Job, error) {
	j.ID, j.CreatedAt = ids.New(), now()
	if _, err := s.db.NewInsert().Model(&j).Exec(ctx); err != nil {
		return domain.Job{}, mapErr(err)
	}
	return j, nil
}

func (s *Store) UpdateJob(ctx context.Context, j domain.Job) (domain.Job, error) {
	res, err := s.db.NewUpdate().Model(&j).
		Column("name", "command", "cron", "timeout_sec", "enabled").
		WherePK().Exec(ctx)
	if err != nil {
		return domain.Job{}, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.Job{}, domain.ErrNotFound
	}
	return j, nil
}

func (s *Store) DeleteJob(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "jobs", id)
}

func (s *Store) CreateJobRun(ctx context.Context, r domain.JobRun) (domain.JobRun, error) {
	r.ID, r.CreatedAt = ids.New(), now()
	if _, err := s.db.NewInsert().Model(&r).Exec(ctx); err != nil {
		return domain.JobRun{}, mapErr(err)
	}
	return r, nil
}

func (s *Store) GetJobRun(ctx context.Context, id string) (domain.JobRun, error) {
	var r domain.JobRun
	err := s.db.NewSelect().Model(&r).Where("id = ?", id).Scan(ctx)
	return r, mapErr(err)
}

func (s *Store) ListJobRuns(ctx context.Context, jobID string, limit int) ([]domain.JobRun, error) {
	rs := []domain.JobRun{}
	err := s.db.NewSelect().Model(&rs).Where("job_id = ?", jobID).
		Order("created_at DESC").Limit(limit).Scan(ctx)
	return rs, mapErr(err)
}

func (s *Store) FinishJobRun(ctx context.Context, r domain.JobRun) error {
	t := now()
	r.FinishedAt = &t
	res, err := s.db.NewUpdate().Model(&r).Column("status", "exit_code", "error", "finished_at").WherePK().Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteJobRun(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "job_runs", id)
}

func (s *Store) CreateRestore(ctx context.Context, r domain.Restore) (domain.Restore, error) {
	r.ID, r.CreatedAt = ids.New(), now()
	if _, err := s.db.NewInsert().Model(&r).Exec(ctx); err != nil {
		return domain.Restore{}, mapErr(err)
	}
	return r, nil
}

func (s *Store) ListRestores(ctx context.Context, serviceID string, limit int) ([]domain.Restore, error) {
	rs := []domain.Restore{}
	err := s.db.NewSelect().Model(&rs).Where("service_id = ?", serviceID).
		Order("created_at DESC").Limit(limit).Scan(ctx)
	return rs, mapErr(err)
}

func (s *Store) FinishRestore(ctx context.Context, id string, status domain.OpStatus, errMsg string) error {
	res, err := s.db.NewUpdate().Model((*domain.Restore)(nil)).
		Set("status = ?", status).Set("error = ?", errMsg).Set("finished_at = ?", now()).
		Where("id = ?", id).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) FailRunningOperations(ctx context.Context, errMsg string) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for _, model := range []any{(*domain.Backup)(nil), (*domain.Restore)(nil), (*domain.JobRun)(nil)} {
			_, err := tx.NewUpdate().Model(model).
				Set("status = ?", domain.OpFailed).Set("error = ?", errMsg).Set("finished_at = ?", now()).
				Where("status = ?", domain.OpRunning).Exec(ctx)
			if err != nil {
				return mapErr(err)
			}
		}
		_, err := tx.NewUpdate().Model((*domain.Backup)(nil)).
			Set("verify_status = ?", domain.OpFailed).Set("verify_error = ?", errMsg).Set("verified_at = ?", now()).
			Where("verify_status = ?", domain.OpRunning).Exec(ctx)
		return mapErr(err)
	})
}

func (s *Store) CreateAPIToken(ctx context.Context, t domain.APIToken) (domain.APIToken, error) {
	t.ID, t.CreatedAt = ids.New(), now()
	if t.Features == nil {
		t.Features = []string{}
	}
	if t.Grants == nil {
		t.Grants = map[string][]string{}
	}
	if _, err := s.db.NewInsert().Model(&t).Exec(ctx); err != nil {
		return domain.APIToken{}, mapErr(err)
	}
	return t, nil
}

func (s *Store) ListAPITokens(ctx context.Context, userID string) ([]domain.APIToken, error) {
	ts := []domain.APIToken{}
	q := s.db.NewSelect().Model(&ts).Order("created_at DESC")
	if userID != "" {
		q = q.Where("user_id = ?", userID)
	}
	err := q.Scan(ctx)
	return ts, mapErr(err)
}

func (s *Store) GetAPITokenByHash(ctx context.Context, hash string) (domain.APIToken, error) {
	var t domain.APIToken
	if err := s.db.NewSelect().Model(&t).Where("token_hash = ?", hash).Scan(ctx); err != nil {
		return domain.APIToken{}, mapErr(err)
	}
	// Minute resolution is plenty and keeps writes rare.
	if t.LastUsedAt == nil || time.Since(*t.LastUsedAt) > time.Minute {
		used := now()
		t.LastUsedAt = &used
		_, _ = s.db.NewUpdate().Model(&t).Column("last_used_at").WherePK().Exec(ctx)
	}
	return t, nil
}

func (s *Store) UpdateAPIToken(ctx context.Context, t domain.APIToken) (domain.APIToken, error) {
	res, err := s.db.NewUpdate().Model(&t).Column("name", "restricted", "features", "grants", "token_hash").WherePK().Exec(ctx)
	if err != nil {
		return domain.APIToken{}, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.APIToken{}, domain.ErrNotFound
	}
	return t, nil
}

func (s *Store) DeleteAPIToken(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "api_tokens", id)
}

func (s *Store) CreateDomain(ctx context.Context, d domain.Domain) (domain.Domain, error) {
	d.ID, d.CreatedAt = ids.New(), now()
	if _, err := s.db.NewInsert().Model(&d).Exec(ctx); err != nil {
		return domain.Domain{}, mapErr(err)
	}
	return d, nil
}

func (s *Store) ListDomains(ctx context.Context) ([]domain.Domain, error) {
	ds := []domain.Domain{}
	err := s.db.NewSelect().Model(&ds).Order("name").Scan(ctx)
	return ds, mapErr(err)
}

func (s *Store) SetDomainProxied(ctx context.Context, id string, proxied bool) (domain.Domain, error) {
	d := domain.Domain{ID: id, Proxied: proxied}
	res, err := s.db.NewUpdate().Model(&d).Column("proxied").WherePK().Exec(ctx)
	if err != nil {
		return domain.Domain{}, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.Domain{}, domain.ErrNotFound
	}
	err = s.db.NewSelect().Model(&d).WherePK().Scan(ctx)
	return d, mapErr(err)
}

func (s *Store) DeleteDomain(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "domains", id)
}

func (s *Store) ListNotificationChannels(ctx context.Context) ([]domain.NotificationChannel, error) {
	chs := []domain.NotificationChannel{}
	err := s.db.NewSelect().Model(&chs).Order("name").Scan(ctx)
	return chs, mapErr(err)
}

func (s *Store) GetNotificationChannel(ctx context.Context, id string) (domain.NotificationChannel, error) {
	var ch domain.NotificationChannel
	err := s.db.NewSelect().Model(&ch).Where("id = ?", id).Scan(ctx)
	return ch, mapErr(err)
}

func (s *Store) CreateNotificationChannel(ctx context.Context, ch domain.NotificationChannel) (domain.NotificationChannel, error) {
	ch.ID, ch.CreatedAt = ids.New(), now()
	if _, err := s.db.NewInsert().Model(&ch).Exec(ctx); err != nil {
		return domain.NotificationChannel{}, mapErr(err)
	}
	return ch, nil
}

func (s *Store) UpdateNotificationChannel(ctx context.Context, ch domain.NotificationChannel) (domain.NotificationChannel, error) {
	res, err := s.db.NewUpdate().Model(&ch).Column("name", "config", "events", "enabled").WherePK().Exec(ctx)
	if err != nil {
		return domain.NotificationChannel{}, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.NotificationChannel{}, domain.ErrNotFound
	}
	err = s.db.NewSelect().Model(&ch).WherePK().Scan(ctx)
	return ch, mapErr(err)
}

func (s *Store) DeleteNotificationChannel(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "notification_channels", id)
}

func (s *Store) SetServiceGitState(ctx context.Context, serviceID, specHash string, orphaned bool) error {
	res, err := s.db.NewUpdate().Model((*domain.Service)(nil)).
		Set("git_spec_hash = ?", specHash).Set("orphaned = ?", orphaned).Where("id = ?", serviceID).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) GetProjectGit(ctx context.Context, projectID string) (domain.ProjectGit, error) {
	var g domain.ProjectGit
	err := s.db.NewSelect().Model(&g).Where("project_id = ?", projectID).Scan(ctx)
	return g, mapErr(err)
}

func (s *Store) ListProjectGit(ctx context.Context) ([]domain.ProjectGit, error) {
	gs := []domain.ProjectGit{}
	err := s.db.NewSelect().Model(&gs).Order("created_at").Scan(ctx)
	return gs, mapErr(err)
}

func (s *Store) SaveProjectGit(ctx context.Context, g domain.ProjectGit) (domain.ProjectGit, error) {
	g.CreatedAt = now()
	if g.Warnings == nil {
		g.Warnings = []string{}
	}
	if g.Applied == nil {
		g.Applied = map[string]string{}
	}
	_, err := s.db.NewInsert().Model(&g).
		On("CONFLICT (project_id) DO UPDATE").
		Set("repo_url = EXCLUDED.repo_url").
		Set("branch = EXCLUDED.branch").
		Set("path = EXCLUDED.path").
		Set("provider_id = EXCLUDED.provider_id").
		Set("auto_sync = EXCLUDED.auto_sync").
		Set("poll_seconds = EXCLUDED.poll_seconds").
		Set("linked_by = EXCLUDED.linked_by").
		Set("linked_token = EXCLUDED.linked_token").
		Exec(ctx)
	if err != nil {
		return domain.ProjectGit{}, mapErr(err)
	}
	return s.GetProjectGit(ctx, g.ProjectID)
}

func (s *Store) SetProjectGitSync(ctx context.Context, g domain.ProjectGit) error {
	if g.Warnings == nil {
		g.Warnings = []string{}
	}
	if g.Applied == nil {
		g.Applied = map[string]string{}
	}
	res, err := s.db.NewUpdate().Model(&g).
		Column("last_commit", "last_synced_at", "last_error", "warnings", "applied").
		WherePK().Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteProjectGit(ctx context.Context, projectID string) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		res, err := tx.NewDelete().Model((*domain.ProjectGit)(nil)).Where("project_id = ?", projectID).Exec(ctx)
		if err != nil {
			return mapErr(err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return domain.ErrNotFound
		}
		_, err = tx.NewUpdate().Model((*domain.Service)(nil)).
			Set("git_spec_hash = ''").Set("orphaned = 0").Where("project_id = ?", projectID).Exec(ctx)
		return mapErr(err)
	})
}

func (s *Store) ListGitProviders(ctx context.Context) ([]domain.GitProvider, error) {
	ps := []domain.GitProvider{}
	err := s.db.NewSelect().Model(&ps).Order("name").Scan(ctx)
	return ps, mapErr(err)
}

func (s *Store) GetGitProvider(ctx context.Context, id string) (domain.GitProvider, error) {
	var p domain.GitProvider
	err := s.db.NewSelect().Model(&p).Where("id = ?", id).Scan(ctx)
	return p, mapErr(err)
}

func (s *Store) CreateGitProvider(ctx context.Context, p domain.GitProvider) (domain.GitProvider, error) {
	if p.ID == "" {
		p.ID = ids.New()
	}
	p.CreatedAt = now()
	if _, err := s.db.NewInsert().Model(&p).Exec(ctx); err != nil {
		return domain.GitProvider{}, mapErr(err)
	}
	return p, nil
}

func (s *Store) UpdateGitProvider(ctx context.Context, p domain.GitProvider) (domain.GitProvider, error) {
	res, err := s.db.NewUpdate().Model(&p).ExcludeColumn("id", "kind", "created_at").WherePK().Exec(ctx)
	if err != nil {
		return domain.GitProvider{}, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.GitProvider{}, domain.ErrNotFound
	}
	return s.GetGitProvider(ctx, p.ID)
}

func (s *Store) DeleteGitProvider(ctx context.Context, id string) error {
	n, err := s.db.NewSelect().Model((*domain.ProjectGit)(nil)).Where("provider_id = ?", id).Count(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n > 0 {
		return fmt.Errorf("%w: %d projects follow a repository through it", domain.ErrConflict, n)
	}
	return deleteByID(ctx, s.db, "git_providers", id)
}

func (s *Store) ListEmailProviders(ctx context.Context) ([]domain.EmailProvider, error) {
	ps := []domain.EmailProvider{}
	err := s.db.NewSelect().Model(&ps).Order("created_at").Scan(ctx)
	return ps, mapErr(err)
}

func (s *Store) GetEmailProvider(ctx context.Context, id string) (domain.EmailProvider, error) {
	var p domain.EmailProvider
	err := s.db.NewSelect().Model(&p).Where("id = ?", id).Scan(ctx)
	return p, mapErr(err)
}

func (s *Store) CreateEmailProvider(ctx context.Context, p domain.EmailProvider) (domain.EmailProvider, error) {
	p.ID, p.CreatedAt = ids.New(), now()
	if _, err := s.db.NewInsert().Model(&p).Exec(ctx); err != nil {
		return domain.EmailProvider{}, mapErr(err)
	}
	return p, nil
}

func (s *Store) UpdateEmailProvider(ctx context.Context, p domain.EmailProvider) (domain.EmailProvider, error) {
	res, err := s.db.NewUpdate().Model(&p).ExcludeColumn("id", "created_at").WherePK().Exec(ctx)
	if err != nil {
		return domain.EmailProvider{}, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.EmailProvider{}, domain.ErrNotFound
	}
	return s.GetEmailProvider(ctx, p.ID)
}

func (s *Store) DeleteEmailProvider(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "email_providers", id)
}

func (s *Store) ListSSOProviders(ctx context.Context) ([]domain.SSOProvider, error) {
	ps := []domain.SSOProvider{}
	err := s.db.NewSelect().Model(&ps).Order("name").Scan(ctx)
	return ps, mapErr(err)
}

func (s *Store) GetSSOProvider(ctx context.Context, id string) (domain.SSOProvider, error) {
	var p domain.SSOProvider
	err := s.db.NewSelect().Model(&p).Where("id = ?", id).Scan(ctx)
	return p, mapErr(err)
}

func (s *Store) CreateSSOProvider(ctx context.Context, p domain.SSOProvider) (domain.SSOProvider, error) {
	p.ID, p.CreatedAt = ids.New(), now()
	if _, err := s.db.NewInsert().Model(&p).Exec(ctx); err != nil {
		return domain.SSOProvider{}, mapErr(err)
	}
	return p, nil
}

func (s *Store) UpdateSSOProvider(ctx context.Context, p domain.SSOProvider) (domain.SSOProvider, error) {
	res, err := s.db.NewUpdate().Model(&p).ExcludeColumn("id", "created_at").WherePK().Exec(ctx)
	if err != nil {
		return domain.SSOProvider{}, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.SSOProvider{}, domain.ErrNotFound
	}
	return s.GetSSOProvider(ctx, p.ID)
}

func (s *Store) DeleteSSOProvider(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "sso_providers", id)
}

func (s *Store) ListSSOIdentities(ctx context.Context, userID string) ([]domain.SSOIdentity, error) {
	is := []domain.SSOIdentity{}
	err := s.db.NewSelect().Model(&is).Where("user_id = ?", userID).Order("created_at").Scan(ctx)
	return is, mapErr(err)
}

func (s *Store) GetSSOIdentity(ctx context.Context, providerID, subject string) (domain.SSOIdentity, error) {
	var i domain.SSOIdentity
	err := s.db.NewSelect().Model(&i).Where("provider_id = ? AND subject = ?", providerID, subject).Scan(ctx)
	return i, mapErr(err)
}

func (s *Store) CreateSSOIdentity(ctx context.Context, i domain.SSOIdentity) (domain.SSOIdentity, error) {
	i.ID, i.CreatedAt = ids.New(), now()
	if _, err := s.db.NewInsert().Model(&i).Exec(ctx); err != nil {
		return domain.SSOIdentity{}, mapErr(err)
	}
	return i, nil
}

func (s *Store) SSOIdentityUsed(ctx context.Context, id, email string) error {
	_, err := s.db.NewUpdate().Model((*domain.SSOIdentity)(nil)).
		Set("email = ?", email).Set("last_used_at = ?", now()).Where("id = ?", id).Exec(ctx)
	return mapErr(err)
}

func (s *Store) DeleteSSOIdentity(ctx context.Context, userID, id string) error {
	res, err := s.db.NewDelete().Model((*domain.SSOIdentity)(nil)).Where("id = ?", id).Where("user_id = ?", userID).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) ListRegistries(ctx context.Context) ([]domain.Registry, error) {
	rs := []domain.Registry{}
	err := s.db.NewSelect().Model(&rs).Order("host").Scan(ctx)
	return rs, mapErr(err)
}

func (s *Store) GetRegistry(ctx context.Context, id string) (domain.Registry, error) {
	var r domain.Registry
	err := s.db.NewSelect().Model(&r).Where("id = ?", id).Scan(ctx)
	return r, mapErr(err)
}

func (s *Store) CreateRegistry(ctx context.Context, r domain.Registry) (domain.Registry, error) {
	r.ID, r.CreatedAt = ids.New(), now()
	if _, err := s.db.NewInsert().Model(&r).Exec(ctx); err != nil {
		return domain.Registry{}, mapErr(err)
	}
	return r, nil
}

func (s *Store) UpdateRegistry(ctx context.Context, r domain.Registry) (domain.Registry, error) {
	res, err := s.db.NewUpdate().Model(&r).Column("host", "username", "password").WherePK().Exec(ctx)
	if err != nil {
		return domain.Registry{}, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.Registry{}, domain.ErrNotFound
	}
	err = s.db.NewSelect().Model(&r).WherePK().Scan(ctx)
	return r, mapErr(err)
}

func (s *Store) DeleteRegistry(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "registries", id)
}

func (s *Store) ListSecretConnections(ctx context.Context) ([]domain.SecretConnection, error) {
	cs := []domain.SecretConnection{}
	err := s.db.NewSelect().Model(&cs).Order("kind", "name").Scan(ctx)
	return cs, mapErr(err)
}

func (s *Store) GetSecretConnection(ctx context.Context, id string) (domain.SecretConnection, error) {
	var c domain.SecretConnection
	err := s.db.NewSelect().Model(&c).Where("id = ?", id).Scan(ctx)
	return c, mapErr(err)
}

func (s *Store) CreateSecretConnection(ctx context.Context, c domain.SecretConnection) (domain.SecretConnection, error) {
	c.ID, c.CreatedAt = ids.New(), now()
	if _, err := s.db.NewInsert().Model(&c).Exec(ctx); err != nil {
		return domain.SecretConnection{}, mapErr(err)
	}
	return c, nil
}

func (s *Store) SetSecretConnectionToken(ctx context.Context, id, token string) error {
	res, err := s.db.NewUpdate().Model((*domain.SecretConnection)(nil)).Set("token = ?", token).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteSecretConnection(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "secret_connections", id)
}

func (s *Store) ListNetworks(ctx context.Context) ([]domain.Network, error) {
	ns := []domain.Network{}
	err := s.db.NewSelect().Model(&ns).Order("name").Scan(ctx)
	return ns, mapErr(err)
}

func (s *Store) GetNetwork(ctx context.Context, id string) (domain.Network, error) {
	var n domain.Network
	err := s.db.NewSelect().Model(&n).Where("id = ?", id).Scan(ctx)
	return n, mapErr(err)
}

func (s *Store) CreateNetwork(ctx context.Context, n domain.Network) (domain.Network, error) {
	n.ID, n.CreatedAt = ids.New(), now()
	if _, err := s.db.NewInsert().Model(&n).Exec(ctx); err != nil {
		return domain.Network{}, mapErr(err)
	}
	return n, nil
}

func (s *Store) DeleteNetwork(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "networks", id)
}

func (s *Store) SetServiceNetworks(ctx context.Context, serviceID string, networkIDs []string) error {
	if networkIDs == nil {
		networkIDs = []string{}
	}
	res, err := s.db.NewUpdate().Model(&domain.Service{ID: serviceID, Networks: networkIDs}).
		Column("networks").WherePK().Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) SetProjectMaintenance(ctx context.Context, projectID string, m domain.Maintenance) error {
	res, err := s.db.NewUpdate().Model(&domain.Project{ID: projectID, Maintenance: m, UpdatedAt: now()}).
		Column("maintenance", "updated_at").WherePK().Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) ListCanvasPositions(ctx context.Context) ([]domain.CanvasPosition, error) {
	ps := []domain.CanvasPosition{}
	err := s.db.NewSelect().Model(&ps).Order("node").Scan(ctx)
	return ps, mapErr(err)
}

func (s *Store) ListTagColors(ctx context.Context) ([]domain.TagColor, error) {
	cs := []domain.TagColor{}
	err := s.db.NewSelect().Model(&cs).Order("name").Scan(ctx)
	return cs, mapErr(err)
}

func (s *Store) SetTagColor(ctx context.Context, name, color string) error {
	if color == "" {
		_, err := s.db.NewDelete().Model((*domain.TagColor)(nil)).Where("name = ?", name).Exec(ctx)
		return mapErr(err)
	}
	_, err := s.db.NewInsert().Model(&domain.TagColor{Name: name, Color: color, UpdatedAt: now()}).
		On("CONFLICT (name) DO UPDATE").
		Set("color = EXCLUDED.color").
		Set("updated_at = EXCLUDED.updated_at").
		Exec(ctx)
	return mapErr(err)
}

func (s *Store) SaveCanvasPositions(ctx context.Context, ps []domain.CanvasPosition, remove []string) error {
	return mapErr(s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if len(remove) > 0 {
			if _, err := tx.NewDelete().Model((*domain.CanvasPosition)(nil)).Where("node IN (?)", bun.List(remove)).Exec(ctx); err != nil {
				return err
			}
		}
		if len(ps) == 0 {
			return nil
		}
		t := now()
		for i := range ps {
			ps[i].UpdatedAt = t
		}
		_, err := tx.NewInsert().Model(&ps).
			On("CONFLICT (node) DO UPDATE").
			Set("x = EXCLUDED.x").
			Set("y = EXCLUDED.y").
			Set("updated_at = EXCLUDED.updated_at").
			Exec(ctx)
		return err
	}))
}

func (s *Store) ListUptimeChecks(ctx context.Context) ([]domain.UptimeCheck, error) {
	cs := []domain.UptimeCheck{}
	err := s.db.NewSelect().Model(&cs).Order("created_at").Scan(ctx)
	return cs, mapErr(err)
}

func (s *Store) GetUptimeCheck(ctx context.Context, serviceID string) (domain.UptimeCheck, error) {
	var c domain.UptimeCheck
	err := s.db.NewSelect().Model(&c).Where("service_id = ?", serviceID).Scan(ctx)
	return c, mapErr(err)
}

func (s *Store) SaveUptimeCheck(ctx context.Context, c domain.UptimeCheck) (domain.UptimeCheck, error) {
	c.CreatedAt = now()
	if c.Recent == nil {
		c.Recent = []domain.UptimeResult{}
	}
	_, err := s.db.NewInsert().Model(&c).
		On("CONFLICT (service_id) DO UPDATE").
		Set("path = EXCLUDED.path").
		Set("interval_sec = EXCLUDED.interval_sec").
		Set("timeout_sec = EXCLUDED.timeout_sec").
		Set("expected_status = EXCLUDED.expected_status").
		Set("enabled = EXCLUDED.enabled").
		Exec(ctx)
	if err != nil {
		return domain.UptimeCheck{}, mapErr(err)
	}
	return s.GetUptimeCheck(ctx, c.ServiceID)
}

func (s *Store) SetUptimeState(ctx context.Context, serviceID string, down bool, since time.Time) error {
	res, err := s.db.NewUpdate().Model((*domain.UptimeCheck)(nil)).
		Set("down = ?", down).Set("changed_at = ?", since.UTC().Truncate(time.Microsecond)).
		Where("service_id = ?", serviceID).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteUptimeCheck(ctx context.Context, serviceID string) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		res, err := tx.NewDelete().Model((*domain.UptimeCheck)(nil)).Where("service_id = ?", serviceID).Exec(ctx)
		if err != nil {
			return mapErr(err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return domain.ErrNotFound
		}
		_, err = tx.NewDelete().Model((*domain.UptimeHour)(nil)).Where("service_id = ?", serviceID).Exec(ctx)
		return mapErr(err)
	})
}

func (s *Store) SetUptimeRecent(ctx context.Context, serviceID string, recent []domain.UptimeResult) error {
	if recent == nil {
		recent = []domain.UptimeResult{}
	}
	_, err := s.db.NewUpdate().Model(&domain.UptimeCheck{Recent: recent}).Column("recent").
		Where("service_id = ?", serviceID).Exec(ctx)
	return mapErr(err)
}

func (s *Store) AddUptimeResult(ctx context.Context, serviceID string, at time.Time, ok bool, latency time.Duration) error {
	h := domain.UptimeHour{ServiceID: serviceID, Hour: at.UTC().Truncate(time.Hour), Checks: 1}
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

func (s *Store) ListUptimeHours(ctx context.Context, serviceID string, since time.Time) ([]domain.UptimeHour, error) {
	hs := []domain.UptimeHour{}
	err := s.db.NewSelect().Model(&hs).Where("service_id = ?", serviceID).
		Where("hour >= ?", since.UTC().Truncate(time.Hour)).Order("hour").Scan(ctx)
	return hs, mapErr(err)
}

func (s *Store) PruneUptime(ctx context.Context, before time.Time) error {
	_, err := s.db.NewDelete().Model((*domain.UptimeHour)(nil)).
		Where("hour < ?", before.UTC().Truncate(time.Hour)).Exec(ctx)
	return mapErr(err)
}

func (s *Store) AddAudit(ctx context.Context, e domain.AuditEntry) error {
	e.ID, e.CreatedAt = ids.New(), now()
	_, err := s.db.NewInsert().Model(&e).Exec(ctx)
	return mapErr(err)
}

func (s *Store) ListAudit(ctx context.Context, limit int) ([]domain.AuditEntry, error) {
	es := []domain.AuditEntry{}
	err := s.db.NewSelect().Model(&es).Order("created_at DESC").Limit(limit).Scan(ctx)
	return es, mapErr(err)
}

func (s *Store) PruneAudit(ctx context.Context, before time.Time) error {
	_, err := s.db.NewDelete().Model((*domain.AuditEntry)(nil)).
		Where("created_at < ?", before.UTC().Truncate(time.Microsecond)).Exec(ctx)
	return mapErr(err)
}

func (s *Store) ListServers(ctx context.Context) ([]domain.Server, error) {
	ss := []domain.Server{}
	err := s.db.NewSelect().Model(&ss).OrderExpr("kind = 'local' DESC, name").Scan(ctx)
	return ss, mapErr(err)
}

func (s *Store) GetServer(ctx context.Context, id string) (domain.Server, error) {
	var sv domain.Server
	err := s.db.NewSelect().Model(&sv).Where("id = ?", id).Scan(ctx)
	return sv, mapErr(err)
}

func (s *Store) CreateServer(ctx context.Context, sv domain.Server) (domain.Server, error) {
	sv.ID, sv.CreatedAt = ids.New(), now()
	if _, err := s.db.NewInsert().Model(&sv).Exec(ctx); err != nil {
		return domain.Server{}, mapErr(err)
	}
	return sv, nil
}

func (s *Store) UpdateServer(ctx context.Context, sv domain.Server) (domain.Server, error) {
	res, err := s.db.NewUpdate().Model(&sv).
		Column("name", "host", "port", "ssh_user", "socket", "host_key", "public_ip", "tunnel_token").WherePK().Exec(ctx)
	if err != nil {
		return domain.Server{}, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.Server{}, domain.ErrNotFound
	}
	return sv, nil
}

func (s *Store) DeleteServer(ctx context.Context, id string) error {
	n, err := s.db.NewSelect().Model((*domain.Project)(nil)).Where("server_id = ?", id).Count(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n > 0 {
		return fmt.Errorf("%w: %d projects run on this server", domain.ErrConflict, n)
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
		return domain.ErrNotFound
	}
	return nil
}

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	}
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() {
		case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
			return fmt.Errorf("%w: %s", domain.ErrConflict, err.Error())
		}
	}
	return err
}
