// Package analyticsdb is the analytics server's store: its events, visits,
// trackers and error issues in its own SQLite file.
package analyticsdb

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"io/fs"
	"strings"
	"sync"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"

	"github.com/kipit-labs/kipitiny/internal/domain/analytics"
	"github.com/kipit-labs/kipitiny/internal/ids"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/sqlitedb"
)

//go:embed migrations/*.sql
var migrations embed.FS

// visitGap ends a visit: the next event after it starts a new one.
const visitGap = 30 * 60

type Store struct {
	db  *bun.DB
	now func() time.Time

	mu       sync.Mutex
	sites    map[string]bool
	paths    map[string]analytics.Paths   // by hostname, the sites with their own
	trackers map[string]analytics.Tracker // by slug
	// errorKeys are the DSN keys of the projects taking errors.
	errorKeys map[int64]string
	day       int64
	salt      []byte
}

// Open opens the file at path, migrated; now is the clock events are
// stamped with.
func Open(ctx context.Context, path string, now func() time.Time) (*Store, error) {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, err
	}
	sqldb, err := sqlitedb.Open(ctx, path, sub)
	if err != nil {
		return nil, err
	}
	s := &Store{db: bun.NewDB(sqldb, sqlitedialect.New()), now: now}
	var hosts []string
	if err := s.db.NewRaw("SELECT hostname FROM sites").Scan(ctx, &hosts); err != nil {
		s.db.Close()
		return nil, err
	}
	s.sites = setOf(hosts)
	var rows []struct{ Hostname, Script, Hit string }
	if err := s.db.NewRaw("SELECT hostname, script, hit FROM sites WHERE script <> ''").Scan(ctx, &rows); err != nil {
		s.db.Close()
		return nil, err
	}
	s.paths = map[string]analytics.Paths{}
	for _, r := range rows {
		s.paths[r.Hostname] = analytics.Paths{Script: r.Script, Hit: r.Hit}
	}
	if err := s.loadTrackers(ctx); err != nil {
		s.db.Close()
		return nil, err
	}
	if err := s.loadErrorProjects(ctx); err != nil {
		s.db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func setOf(hosts []string) map[string]bool {
	m := make(map[string]bool, len(hosts))
	for _, h := range hosts {
		m[strings.ToLower(h)] = true
	}
	return m
}

// SetSites replaces the tracked hostnames, and the paths of those that have
// their own. Their events stay.
func (s *Store) SetSites(ctx context.Context, hosts []string, paths map[string]analytics.Paths) error {
	sites := setOf(hosts)
	own := map[string]analytics.Paths{}
	for h, p := range paths {
		if h = strings.ToLower(h); sites[h] && p.Script != "" && p.Hit != "" {
			own[h] = p
		}
	}
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw("DELETE FROM sites").Exec(ctx); err != nil {
			return err
		}
		for h := range sites {
			p := own[h]
			if _, err := tx.NewRaw("INSERT INTO sites (hostname, script, hit) VALUES (?, ?, ?)", h, p.Script, p.Hit).Exec(ctx); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.sites, s.paths = sites, own
	s.mu.Unlock()
	return nil
}

// SitePaths are host's own paths, if it has some.
func (s *Store) SitePaths(host string) (analytics.Paths, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.paths[host]
	return p, ok
}

func (s *Store) Tracked(host string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sites[host]
}

// visitorSalt is today's (UTC) salt, created on the first event of the day;
// older ones are deleted.
func (s *Store) visitorSalt(ctx context.Context, day int64) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.day == day {
		return s.salt, nil
	}
	salt := make([]byte, 32)
	_, _ = rand.Read(salt)
	if _, err := s.db.NewRaw("INSERT INTO salts (day, salt) VALUES (?, ?) ON CONFLICT (day) DO NOTHING", day, salt).Exec(ctx); err != nil {
		return nil, err
	}
	if err := s.db.NewRaw("SELECT salt FROM salts WHERE day = ?", day).Scan(ctx, &salt); err != nil {
		return nil, err
	}
	if _, err := s.db.NewRaw("DELETE FROM salts WHERE day < ?", day).Exec(ctx); err != nil {
		return nil, err
	}
	s.day, s.salt = day, salt
	return salt, nil
}

// Record stores e, in the visit its visitor is on or a new one.
func (s *Store) Record(ctx context.Context, e analytics.Event) error {
	now := s.now().Unix()
	salt, err := s.visitorSalt(ctx, now/86400)
	if err != nil {
		return err
	}
	h := sha256.New()
	for _, p := range []string{e.Hostname, e.IP, e.UserAgent} {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	h.Write(salt)
	visitor := hex.EncodeToString(h.Sum(nil)[:16])
	pageview := 0
	if e.Name == "" {
		pageview = 1
	}
	if e.Name == analytics.IdentifyEvent {
		// No visit yet (the pageview not in): the next hits carry the identity.
		_, err := s.db.NewRaw(`UPDATE visits SET identity = ?, identity_props = ? WHERE id = (SELECT id FROM visits
			WHERE visitor = ? AND hostname = ? AND last_at > ? ORDER BY last_at DESC LIMIT 1)`,
			e.Identity, e.Props, visitor, e.Hostname, now-visitGap).Exec(ctx)
		return err
	}
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var visit string
		err := tx.NewRaw("SELECT id FROM visits WHERE visitor = ? AND hostname = ? AND last_at > ? ORDER BY last_at DESC LIMIT 1",
			visitor, e.Hostname, now-visitGap).Scan(ctx, &visit)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			visit = ids.New()
			_, err = tx.NewRaw(`INSERT INTO visits (id, hostname, visitor, started_at, last_at, pageviews, entry, exit, identity)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, visit, e.Hostname, visitor, now, now, pageview, e.Path, e.Path, e.Identity).Exec(ctx)
		case err == nil && pageview == 1:
			_, err = tx.NewRaw("UPDATE visits SET last_at = ?, pageviews = pageviews + 1, exit = ? WHERE id = ?", now, e.Path, visit).Exec(ctx)
		case err == nil:
			_, err = tx.NewRaw("UPDATE visits SET last_at = ? WHERE id = ?", now, visit).Exec(ctx)
		}
		if err == nil && e.Identity != "" {
			_, err = tx.NewRaw("UPDATE visits SET identity = ? WHERE id = ?", e.Identity, visit).Exec(ctx)
		}
		if err != nil {
			return err
		}
		_, err = tx.NewRaw(`INSERT INTO events (id, hostname, created_at, visitor, visit_id, name, path, query, title, referrer,
			utm_source, utm_medium, utm_campaign, utm_term, utm_content, browser, os, device, screen, language, country, props)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			ids.New(), e.Hostname, now, visitor, visit, e.Name, e.Path, e.Query, e.Title, e.Referrer,
			e.UTMSource, e.UTMMedium, e.UTMCampaign, e.UTMTerm, e.UTMContent,
			e.Browser, e.OS, e.Device, e.Screen, e.Language, e.Country, e.Props).Exec(ctx)
		return err
	})
}

// Prune deletes what is older than the retention.
func (s *Store) Prune(ctx context.Context, keep time.Duration) error {
	before := s.now().Add(-keep).Unix()
	if _, err := s.db.NewRaw("DELETE FROM events WHERE created_at < ?", before).Exec(ctx); err != nil {
		return err
	}
	if _, err := s.db.NewRaw("DELETE FROM visits WHERE last_at < ?", before).Exec(ctx); err != nil {
		return err
	}
	return s.pruneErrors(ctx, keep)
}
