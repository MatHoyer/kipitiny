package sqlite

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/ids"
)

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
