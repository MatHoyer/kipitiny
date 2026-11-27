package sqlite

import (
	"context"
	"fmt"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/ids"
)

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
