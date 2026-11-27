package sqlite

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/ids"
)

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
