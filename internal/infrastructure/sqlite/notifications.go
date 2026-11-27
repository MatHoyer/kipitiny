package sqlite

import (
	"context"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/ids"
)

func (s *Store) SavePushSubscription(ctx context.Context, p domain.PushSubscription) (domain.PushSubscription, error) {
	p.ID, p.CreatedAt = ids.New(), now()
	_, err := s.db.NewInsert().Model(&p).On("CONFLICT (endpoint) DO UPDATE").
		Set("user_id = EXCLUDED.user_id").Set("name = EXCLUDED.name").
		Set("p256dh = EXCLUDED.p256dh").Set("auth = EXCLUDED.auth").
		Returning("*").Exec(ctx)
	return p, mapErr(err)
}

func (s *Store) ListPushSubscriptions(ctx context.Context, userID string) ([]domain.PushSubscription, error) {
	ps := []domain.PushSubscription{}
	err := s.db.NewSelect().Model(&ps).Where("user_id = ?", userID).Order("created_at").Scan(ctx)
	return ps, mapErr(err)
}

func (s *Store) ListAdminPushSubscriptions(ctx context.Context) ([]domain.PushSubscription, error) {
	ps := []domain.PushSubscription{}
	err := s.db.NewSelect().Model(&ps).
		Where("user_id IN (SELECT id FROM users WHERE role = ?)", domain.RoleAdmin).Order("created_at").Scan(ctx)
	return ps, mapErr(err)
}

func (s *Store) DeletePushSubscription(ctx context.Context, userID, id string) error {
	res, err := s.db.NewDelete().Model((*domain.PushSubscription)(nil)).Where("id = ?", id).Where("user_id = ?", userID).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) ListNotificationChannels(ctx context.Context) ([]domain.NotificationChannel, error) {
	chs := []domain.NotificationChannel{}
	err := s.db.NewSelect().Model(&chs).Order("name").Scan(ctx)
	return chs, mapErr(err)
}

func (s *Store) GetNotificationChannel(ctx context.Context, id string) (domain.NotificationChannel, error) {
	var ch domain.NotificationChannel
	err := s.db.NewSelect().Model(&ch).Where("id = ?", id).Scan(ctx)
	return ch, mapErr(err)
}

func (s *Store) CreateNotificationChannel(ctx context.Context, ch domain.NotificationChannel) (domain.NotificationChannel, error) {
	ch.ID, ch.CreatedAt = ids.New(), now()
	if _, err := s.db.NewInsert().Model(&ch).Exec(ctx); err != nil {
		return domain.NotificationChannel{}, mapErr(err)
	}
	return ch, nil
}

func (s *Store) UpdateNotificationChannel(ctx context.Context, ch domain.NotificationChannel) (domain.NotificationChannel, error) {
	res, err := s.db.NewUpdate().Model(&ch).Column("name", "config", "events", "enabled").WherePK().Exec(ctx)
	if err != nil {
		return domain.NotificationChannel{}, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.NotificationChannel{}, domain.ErrNotFound
	}
	err = s.db.NewSelect().Model(&ch).WherePK().Scan(ctx)
	return ch, mapErr(err)
}

func (s *Store) DeleteNotificationChannel(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "notification_channels", id)
}

func (s *Store) ListEmailProviders(ctx context.Context) ([]domain.EmailProvider, error) {
	ps := []domain.EmailProvider{}
	err := s.db.NewSelect().Model(&ps).Order("created_at").Scan(ctx)
	return ps, mapErr(err)
}

func (s *Store) GetEmailProvider(ctx context.Context, id string) (domain.EmailProvider, error) {
	var p domain.EmailProvider
	err := s.db.NewSelect().Model(&p).Where("id = ?", id).Scan(ctx)
	return p, mapErr(err)
}

func (s *Store) CreateEmailProvider(ctx context.Context, p domain.EmailProvider) (domain.EmailProvider, error) {
	p.ID, p.CreatedAt = ids.New(), now()
	if _, err := s.db.NewInsert().Model(&p).Exec(ctx); err != nil {
		return domain.EmailProvider{}, mapErr(err)
	}
	return p, nil
}

func (s *Store) UpdateEmailProvider(ctx context.Context, p domain.EmailProvider) (domain.EmailProvider, error) {
	res, err := s.db.NewUpdate().Model(&p).ExcludeColumn("id", "created_at").WherePK().Exec(ctx)
	if err != nil {
		return domain.EmailProvider{}, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.EmailProvider{}, domain.ErrNotFound
	}
	return s.GetEmailProvider(ctx, p.ID)
}

func (s *Store) DeleteEmailProvider(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "email_providers", id)
}
