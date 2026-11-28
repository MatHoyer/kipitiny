package analyticsdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/kipit-labs/kipitiny/internal/domain/analytics"
	"github.com/kipit-labs/kipitiny/internal/ids"
)

// loadTrackers refreshes the slugs the public port serves.
func (s *Store) loadTrackers(ctx context.Context) error {
	var ts []analytics.Tracker
	if err := s.db.NewRaw("SELECT id, kind, slug, name, target, created_at FROM trackers").Scan(ctx, &ts); err != nil {
		return err
	}
	m := make(map[string]analytics.Tracker, len(ts))
	for _, t := range ts {
		m[t.Slug] = t
	}
	s.mu.Lock()
	s.trackers = m
	s.mu.Unlock()
	return nil
}

// TrackerBySlug is the pixel or link the public port serves at slug.
func (s *Store) TrackerBySlug(kind, slug string) (analytics.Tracker, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.trackers[slug]
	return t, ok && t.Kind == kind
}

// Trackers lists them, newest first, with their last 30 days' hits.
func (s *Store) Trackers(ctx context.Context) ([]analytics.Tracker, error) {
	ts := []analytics.Tracker{}
	if err := s.db.NewRaw("SELECT id, kind, slug, name, target, created_at FROM trackers ORDER BY id DESC").Scan(ctx, &ts); err != nil {
		return nil, err
	}
	var counts []struct {
		Hostname string
		N        int
	}
	err := s.db.NewRaw(`SELECT hostname, COUNT(*) AS n FROM events WHERE (hostname LIKE 'pixel:%' OR hostname LIKE 'link:%')
		AND created_at >= ? GROUP BY hostname`, s.now().Add(-30*24*time.Hour).Unix()).Scan(ctx, &counts)
	if err != nil {
		return nil, err
	}
	n := map[string]int{}
	for _, c := range counts {
		n[c.Hostname] = c.N
	}
	for i := range ts {
		ts[i].Hits = n[ts[i].Host()]
	}
	return ts, nil
}

func (s *Store) Tracker(ctx context.Context, id string) (analytics.Tracker, error) {
	var t analytics.Tracker
	err := s.db.NewRaw("SELECT id, kind, slug, name, target, created_at FROM trackers WHERE id = ?", id).Scan(ctx, &t)
	if errors.Is(err, sql.ErrNoRows) {
		return t, analytics.ErrNotFound
	}
	return t, err
}

func uniqueSlug(err error, slug string) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return fmt.Errorf("%w: slug %q is taken", analytics.ErrInvalid, slug)
	}
	return err
}

func (s *Store) CreateTracker(ctx context.Context, t analytics.Tracker) (analytics.Tracker, error) {
	if err := t.Check(); err != nil {
		return t, err
	}
	t.ID, t.CreatedAt = ids.New(), s.now().Unix()
	_, err := s.db.NewRaw("INSERT INTO trackers (id, kind, slug, name, target, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		t.ID, t.Kind, t.Slug, t.Name, t.Target, t.CreatedAt).Exec(ctx)
	if err := uniqueSlug(err, t.Slug); err != nil {
		return t, err
	}
	return t, s.loadTrackers(ctx)
}

// UpdateTracker changes its name, slug and target; its kind stays.
func (s *Store) UpdateTracker(ctx context.Context, t analytics.Tracker) (analytics.Tracker, error) {
	old, err := s.Tracker(ctx, t.ID)
	if err != nil {
		return t, err
	}
	t.Kind, t.CreatedAt = old.Kind, old.CreatedAt
	if t.Slug == "" {
		t.Slug = old.Slug
	}
	if err := t.Check(); err != nil {
		return t, err
	}
	_, err = s.db.NewRaw("UPDATE trackers SET slug = ?, name = ?, target = ? WHERE id = ?", t.Slug, t.Name, t.Target, t.ID).Exec(ctx)
	if err := uniqueSlug(err, t.Slug); err != nil {
		return t, err
	}
	return t, s.loadTrackers(ctx)
}

// DeleteTracker removes it and its hits.
func (s *Store) DeleteTracker(ctx context.Context, id string) error {
	t, err := s.Tracker(ctx, id)
	if err != nil {
		return err
	}
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for _, q := range []string{"DELETE FROM events WHERE hostname = ?", "DELETE FROM visits WHERE hostname = ?"} {
			if _, err := tx.NewRaw(q, t.Host()).Exec(ctx); err != nil {
				return err
			}
		}
		_, err := tx.NewRaw("DELETE FROM trackers WHERE id = ?", id).Exec(ctx)
		return err
	})
	if err != nil {
		return err
	}
	return s.loadTrackers(ctx)
}
