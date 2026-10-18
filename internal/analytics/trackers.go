package analytics

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/MatHoyer/kipitiny/internal/ids"
)

// Kinds of Tracker.
const (
	// KindPixel is a 1x1 GIF at /p/<slug>.gif: each load is a view.
	KindPixel = "pixel"
	// KindLink redirects /l/<slug> to its target: each visit is a click.
	KindLink = "link"
)

// Tracker is a pixel or a tracked link.
type Tracker struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Target    string `json:"target,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	// Hits counts its views or clicks of the last 30 days.
	Hits int `json:"hits" bun:"-"`
	// URL is set by the manager, which knows the analytics domain.
	URL string `json:"url,omitempty" bun:"-"`
}

// Host is the pseudo-host its hits are recorded under.
func (t Tracker) Host() string { return t.Kind + ":" + t.ID }

// ErrInvalid is a tracker the server refuses: the message says why.
var ErrInvalid = errors.New("invalid")

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func randomSlug() string {
	const alphabet = "abcdefghijkmnpqrstuvwxyz23456789" // no 0/o, 1/l
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// check validates t, giving a pixel no target and a missing slug a random one.
func (t *Tracker) check() error {
	t.Name, t.Slug, t.Target = strings.TrimSpace(t.Name), strings.ToLower(strings.TrimSpace(t.Slug)), strings.TrimSpace(t.Target)
	if t.Kind != KindPixel && t.Kind != KindLink {
		return fmt.Errorf("%w: kind is pixel or link", ErrInvalid)
	}
	if t.Name == "" || len(t.Name) > 100 {
		return fmt.Errorf("%w: a name of 1 to 100 characters", ErrInvalid)
	}
	if t.Slug == "" {
		t.Slug = randomSlug()
	}
	if !slugRe.MatchString(t.Slug) {
		return fmt.Errorf("%w: slug %q: lowercase letters, digits and -, up to 63", ErrInvalid, t.Slug)
	}
	if t.Kind == KindPixel {
		t.Target = ""
		return nil
	}
	u, err := url.Parse(t.Target)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || len(t.Target) > 2048 {
		return fmt.Errorf("%w: a link's target is an http(s) URL", ErrInvalid)
	}
	return nil
}

// loadTrackers refreshes the slugs the public port serves.
func (s *Store) loadTrackers(ctx context.Context) error {
	var ts []Tracker
	if err := s.db.NewRaw("SELECT id, kind, slug, name, target, created_at FROM trackers").Scan(ctx, &ts); err != nil {
		return err
	}
	m := make(map[string]Tracker, len(ts))
	for _, t := range ts {
		m[t.Slug] = t
	}
	s.mu.Lock()
	s.trackers = m
	s.mu.Unlock()
	return nil
}

func (s *Store) tracker(kind, slug string) (Tracker, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.trackers[slug]
	return t, ok && t.Kind == kind
}

// Trackers lists them, newest first, with their last 30 days' hits.
func (s *Store) Trackers(ctx context.Context) ([]Tracker, error) {
	ts := []Tracker{}
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

func (s *Store) Tracker(ctx context.Context, id string) (Tracker, error) {
	var t Tracker
	err := s.db.NewRaw("SELECT id, kind, slug, name, target, created_at FROM trackers WHERE id = ?", id).Scan(ctx, &t)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

func uniqueSlug(err error, slug string) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return fmt.Errorf("%w: slug %q is taken", ErrInvalid, slug)
	}
	return err
}

func (s *Store) CreateTracker(ctx context.Context, t Tracker) (Tracker, error) {
	if err := t.check(); err != nil {
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
func (s *Store) UpdateTracker(ctx context.Context, t Tracker) (Tracker, error) {
	old, err := s.Tracker(ctx, t.ID)
	if err != nil {
		return t, err
	}
	t.Kind, t.CreatedAt = old.Kind, old.CreatedAt
	if t.Slug == "" {
		t.Slug = old.Slug
	}
	if err := t.check(); err != nil {
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
