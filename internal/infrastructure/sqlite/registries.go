package sqlite

import (
	"context"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/ids"
)

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
