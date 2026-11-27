package sqlite

import (
	"context"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/ids"
)

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
