package sqlite

import (
	"context"
	"time"

	"github.com/uptrace/bun"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/ids"
)

func (s *Store) ListUptimeChecks(ctx context.Context) ([]domain.UptimeCheck, error) {
	cs := []domain.UptimeCheck{}
	err := s.db.NewSelect().Model(&cs).Order("created_at").Scan(ctx)
	return cs, mapErr(err)
}

func (s *Store) GetUptimeCheck(ctx context.Context, serviceID string) (domain.UptimeCheck, error) {
	var c domain.UptimeCheck
	err := s.db.NewSelect().Model(&c).Where("service_id = ?", serviceID).Scan(ctx)
	return c, mapErr(err)
}

func (s *Store) SaveUptimeCheck(ctx context.Context, c domain.UptimeCheck) (domain.UptimeCheck, error) {
	c.CreatedAt = now()
	if c.Recent == nil {
		c.Recent = []domain.UptimeResult{}
	}
	_, err := s.db.NewInsert().Model(&c).
		On("CONFLICT (service_id) DO UPDATE").
		Set("path = EXCLUDED.path").
		Set("interval_sec = EXCLUDED.interval_sec").
		Set("timeout_sec = EXCLUDED.timeout_sec").
		Set("expected_status = EXCLUDED.expected_status").
		Set("enabled = EXCLUDED.enabled").
		Exec(ctx)
	if err != nil {
		return domain.UptimeCheck{}, mapErr(err)
	}
	return s.GetUptimeCheck(ctx, c.ServiceID)
}

func (s *Store) SetUptimeState(ctx context.Context, serviceID string, down bool, since time.Time) error {
	res, err := s.db.NewUpdate().Model((*domain.UptimeCheck)(nil)).
		Set("down = ?", down).Set("changed_at = ?", since.UTC().Truncate(time.Microsecond)).
		Where("service_id = ?", serviceID).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteUptimeCheck(ctx context.Context, serviceID string) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		res, err := tx.NewDelete().Model((*domain.UptimeCheck)(nil)).Where("service_id = ?", serviceID).Exec(ctx)
		if err != nil {
			return mapErr(err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return domain.ErrNotFound
		}
		_, err = tx.NewDelete().Model((*domain.UptimeHour)(nil)).Where("service_id = ?", serviceID).Exec(ctx)
		return mapErr(err)
	})
}

func (s *Store) SetUptimeRecent(ctx context.Context, serviceID string, recent []domain.UptimeResult) error {
	if recent == nil {
		recent = []domain.UptimeResult{}
	}
	_, err := s.db.NewUpdate().Model(&domain.UptimeCheck{Recent: recent}).Column("recent").
		Where("service_id = ?", serviceID).Exec(ctx)
	return mapErr(err)
}

func (s *Store) AddUptimeResult(ctx context.Context, serviceID string, at time.Time, ok bool, latency time.Duration) error {
	h := domain.UptimeHour{ServiceID: serviceID, Hour: at.UTC().Truncate(time.Hour), Checks: 1}
	if ok {
		h.LatencyMS = latency.Milliseconds()
	} else {
		h.Failures = 1
	}
	_, err := s.db.NewInsert().Model(&h).
		On("CONFLICT (service_id, hour) DO UPDATE").
		Set("checks = uptime_hour.checks + 1").
		Set("failures = uptime_hour.failures + EXCLUDED.failures").
		Set("latency_ms = uptime_hour.latency_ms + EXCLUDED.latency_ms").
		Exec(ctx)
	return mapErr(err)
}

func (s *Store) ListUptimeHours(ctx context.Context, serviceID string, since time.Time) ([]domain.UptimeHour, error) {
	hs := []domain.UptimeHour{}
	err := s.db.NewSelect().Model(&hs).Where("service_id = ?", serviceID).
		Where("hour >= ?", since.UTC().Truncate(time.Hour)).Order("hour").Scan(ctx)
	return hs, mapErr(err)
}

func (s *Store) PruneUptime(ctx context.Context, before time.Time) error {
	_, err := s.db.NewDelete().Model((*domain.UptimeHour)(nil)).
		Where("hour < ?", before.UTC().Truncate(time.Hour)).Exec(ctx)
	return mapErr(err)
}

func (s *Store) AddAudit(ctx context.Context, e domain.AuditEntry) error {
	e.ID, e.CreatedAt = ids.New(), now()
	_, err := s.db.NewInsert().Model(&e).Exec(ctx)
	return mapErr(err)
}

func (s *Store) ListAudit(ctx context.Context, limit int) ([]domain.AuditEntry, error) {
	es := []domain.AuditEntry{}
	err := s.db.NewSelect().Model(&es).Order("created_at DESC").Limit(limit).Scan(ctx)
	return es, mapErr(err)
}

func (s *Store) PruneAudit(ctx context.Context, before time.Time) error {
	_, err := s.db.NewDelete().Model((*domain.AuditEntry)(nil)).
		Where("created_at < ?", before.UTC().Truncate(time.Microsecond)).Exec(ctx)
	return mapErr(err)
}
