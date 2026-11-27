package sqlite

import (
	"context"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/ids"
)

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
