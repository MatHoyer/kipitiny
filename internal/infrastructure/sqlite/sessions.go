package sqlite

import (
	"context"
	"time"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/ids"
)

func (s *Store) CreateSession(ctx context.Context, sess domain.Session) error {
	sess.CreatedAt = now()
	sess.ExpiresAt = sess.ExpiresAt.UTC().Truncate(time.Microsecond)
	_, err := s.db.NewInsert().Model(&sess).Exec(ctx)
	return mapErr(err)
}

func (s *Store) GetSession(ctx context.Context, tokenHash string) (domain.Session, error) {
	var sess domain.Session
	err := s.db.NewSelect().Model(&sess).
		Where("token_hash = ?", tokenHash).Where("expires_at > ?", now()).Scan(ctx)
	return sess, mapErr(err)
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.NewDelete().Model((*domain.Session)(nil)).Where("token_hash = ?", tokenHash).Exec(ctx)
	return mapErr(err)
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	_, err := s.db.NewDelete().Model((*domain.Session)(nil)).Where("expires_at <= ?", now()).Exec(ctx)
	return mapErr(err)
}

func (s *Store) CreateAPIToken(ctx context.Context, t domain.APIToken) (domain.APIToken, error) {
	t.ID, t.CreatedAt = ids.New(), now()
	if t.Features == nil {
		t.Features = []string{}
	}
	if t.Grants == nil {
		t.Grants = map[string][]string{}
	}
	if _, err := s.db.NewInsert().Model(&t).Exec(ctx); err != nil {
		return domain.APIToken{}, mapErr(err)
	}
	return t, nil
}

func (s *Store) ListAPITokens(ctx context.Context, userID string) ([]domain.APIToken, error) {
	ts := []domain.APIToken{}
	q := s.db.NewSelect().Model(&ts).Order("created_at DESC")
	if userID != "" {
		q = q.Where("user_id = ?", userID)
	}
	err := q.Scan(ctx)
	return ts, mapErr(err)
}

func (s *Store) GetAPITokenByHash(ctx context.Context, hash string) (domain.APIToken, error) {
	var t domain.APIToken
	if err := s.db.NewSelect().Model(&t).Where("token_hash = ?", hash).Scan(ctx); err != nil {
		return domain.APIToken{}, mapErr(err)
	}
	// Minute resolution is plenty and keeps writes rare.
	if t.LastUsedAt == nil || time.Since(*t.LastUsedAt) > time.Minute {
		used := now()
		t.LastUsedAt = &used
		_, _ = s.db.NewUpdate().Model(&t).Column("last_used_at").WherePK().Exec(ctx)
	}
	return t, nil
}

func (s *Store) UpdateAPIToken(ctx context.Context, t domain.APIToken) (domain.APIToken, error) {
	res, err := s.db.NewUpdate().Model(&t).Column("name", "restricted", "features", "grants", "token_hash").WherePK().Exec(ctx)
	if err != nil {
		return domain.APIToken{}, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.APIToken{}, domain.ErrNotFound
	}
	return t, nil
}

func (s *Store) DeleteAPIToken(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "api_tokens", id)
}
