package sqlite

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/ids"
)

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
		q = q.Where("project_id IN (?)", bun.List(f.ProjectIn))
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
