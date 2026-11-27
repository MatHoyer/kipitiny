package sqlite

import (
	"context"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/ids"
)

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
