package sqlite

import (
	"context"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/ids"
)

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
