package sqlite

import (
	"context"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/ids"
)

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
