// Package analytics is kipitiny's cookieless web analytics: the server run
// as its own container (cmd/kipitiny-analytics), with its events in its own
// SQLite file, and the client the manager reads stats through.
package analytics

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"

	"github.com/MatHoyer/kipitiny/internal/ids"
	"github.com/MatHoyer/kipitiny/internal/sqlitedb"
)

//go:embed migrations/*.sql
var migrations embed.FS

// visitGap ends a visit: the next event after it starts a new one.
const visitGap = 30 * 60

type Store struct {
	db  *bun.DB
	now func() time.Time

	mu    sync.Mutex
	sites map[string]bool
	day   int64
	salt  []byte
}

func Open(ctx context.Context, path string) (*Store, error) {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, err
	}
	sqldb, err := sqlitedb.Open(ctx, path, sub)
	if err != nil {
		return nil, err
	}
	s := &Store{db: bun.NewDB(sqldb, sqlitedialect.New()), now: time.Now}
	var hosts []string
	if err := s.db.NewRaw("SELECT hostname FROM sites").Scan(ctx, &hosts); err != nil {
		s.db.Close()
		return nil, err
	}
	s.sites = setOf(hosts)
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

// SetSites replaces the tracked hostnames. Their events stay.
func (s *Store) SetSites(ctx context.Context, hosts []string) error {
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw("DELETE FROM sites").Exec(ctx); err != nil {
			return err
		}
		for h := range setOf(hosts) {
			if _, err := tx.NewRaw("INSERT INTO sites (hostname) VALUES (?)", h).Exec(ctx); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.sites = setOf(hosts)
	s.mu.Unlock()
	return nil
}

func (s *Store) tracked(host string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sites[host]
}

// Event is one pageview (Name empty) or custom event, as the server
// prepared it from a hit.
type Event struct {
	Hostname, Name, Path, Query, Title, Referrer           string
	UTMSource, UTMMedium, UTMCampaign, UTMTerm, UTMContent string
	Browser, OS, Device, Screen, Language, Country, Props  string
	IP, UserAgent                                          string // hashed into the visitor, never stored
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
func (s *Store) Record(ctx context.Context, e Event) error {
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
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var visit string
		err := tx.NewRaw("SELECT id FROM visits WHERE visitor = ? AND hostname = ? AND last_at > ? ORDER BY last_at DESC LIMIT 1",
			visitor, e.Hostname, now-visitGap).Scan(ctx, &visit)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			visit = ids.New()
			_, err = tx.NewRaw("INSERT INTO visits (id, hostname, visitor, started_at, last_at, pageviews, entry, exit) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
				visit, e.Hostname, visitor, now, now, pageview, e.Path, e.Path).Exec(ctx)
		case err == nil && pageview == 1:
			_, err = tx.NewRaw("UPDATE visits SET last_at = ?, pageviews = pageviews + 1, exit = ? WHERE id = ?", now, e.Path, visit).Exec(ctx)
		case err == nil:
			_, err = tx.NewRaw("UPDATE visits SET last_at = ? WHERE id = ?", now, visit).Exec(ctx)
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
	_, err := s.db.NewRaw("DELETE FROM visits WHERE last_at < ?", before).Exec(ctx)
	return err
}

// Query selects the events of Stats.
type Query struct {
	Hosts    []string
	From, To time.Time
	// Interval is the series' bucket; Offset (seconds east of UTC) aligns
	// day buckets to the viewer's midnight.
	Interval time.Duration
	Offset   int
}

type Stats struct {
	Pageviews int `json:"pageviews"`
	Visitors  int `json:"visitors"`
	Visits    int `json:"visits"`
	Bounces   int `json:"bounces"`
	// Duration sums the visits' lengths, in seconds.
	Duration int64 `json:"duration"`
	// Active counts visitors of the last 30 minutes, whatever the range.
	Active     int              `json:"active"`
	Series     []Point          `json:"series"`
	Breakdowns map[string][]Row `json:"breakdowns"`
}

type Point struct {
	T         int64 `json:"t"`
	Pageviews int   `json:"pageviews"`
	Visitors  int   `json:"visitors"`
}

type Row struct {
	Value    string `json:"value"`
	Count    int    `json:"count"`
	Visitors int    `json:"visitors"`
}

// pageviewBreakdowns are columns of pageviews; Stats counts each value's
// pageviews and visitors.
var pageviewBreakdowns = map[string]string{
	"pages": "path", "titles": "title", "queries": "query", "referrers": "referrer",
	"utm_source": "utm_source", "utm_medium": "utm_medium", "utm_campaign": "utm_campaign",
	"utm_term": "utm_term", "utm_content": "utm_content",
	"browsers": "browser", "os": "os", "devices": "device", "screens": "screen",
	"languages": "language", "countries": "country",
}

// topN is how many values each breakdown lists.
const topN = 20

func (s *Store) Stats(ctx context.Context, q Query) (Stats, error) {
	st := Stats{Series: []Point{}, Breakdowns: map[string][]Row{}}
	if len(q.Hosts) == 0 {
		return st, nil
	}
	hosts := bun.In(q.Hosts)
	from, to := q.From.Unix(), q.To.Unix()
	err := s.db.NewRaw(`SELECT COUNT(CASE WHEN name = '' THEN 1 END), COUNT(DISTINCT visitor) FROM events
		WHERE hostname IN (?) AND created_at >= ? AND created_at < ?`, hosts, from, to).Scan(ctx, &st.Pageviews, &st.Visitors)
	if err != nil {
		return st, err
	}
	err = s.db.NewRaw(`SELECT COUNT(*), COUNT(CASE WHEN pageviews <= 1 THEN 1 END), COALESCE(SUM(last_at - started_at), 0) FROM visits
		WHERE hostname IN (?) AND started_at >= ? AND started_at < ?`, hosts, from, to).Scan(ctx, &st.Visits, &st.Bounces, &st.Duration)
	if err != nil {
		return st, err
	}
	err = s.db.NewRaw("SELECT COUNT(DISTINCT visitor) FROM events WHERE hostname IN (?) AND created_at >= ?",
		hosts, s.now().Unix()-visitGap).Scan(ctx, &st.Active)
	if err != nil {
		return st, err
	}
	iv, off := max(int64(q.Interval.Seconds()), 60), int64(q.Offset)
	err = s.db.NewRaw(`SELECT (created_at + ?) / ? * ? - ? AS t, COUNT(CASE WHEN name = '' THEN 1 END) AS pageviews,
		COUNT(DISTINCT visitor) AS visitors
		FROM events WHERE hostname IN (?) AND created_at >= ? AND created_at < ? GROUP BY t ORDER BY t`,
		off, iv, iv, off, hosts, from, to).Scan(ctx, &st.Series)
	if err != nil {
		return st, err
	}
	for key, col := range pageviewBreakdowns {
		rows := []Row{}
		err := s.db.NewRaw(`SELECT ? AS value, COUNT(*) AS count, COUNT(DISTINCT visitor) AS visitors FROM events
			WHERE hostname IN (?) AND created_at >= ? AND created_at < ? AND name = '' AND ? <> ''
			GROUP BY value ORDER BY count DESC, value LIMIT ?`,
			bun.Ident(col), hosts, from, to, bun.Ident(col), topN).Scan(ctx, &rows)
		if err != nil {
			return st, err
		}
		st.Breakdowns[key] = rows
	}
	for key, col := range map[string]string{"entries": "entry", "exits": "exit"} {
		rows := []Row{}
		err := s.db.NewRaw(`SELECT ? AS value, COUNT(*) AS count, COUNT(DISTINCT visitor) AS visitors FROM visits
			WHERE hostname IN (?) AND started_at >= ? AND started_at < ? AND ? <> ''
			GROUP BY value ORDER BY count DESC, value LIMIT ?`,
			bun.Ident(col), hosts, from, to, bun.Ident(col), topN).Scan(ctx, &rows)
		if err != nil {
			return st, err
		}
		st.Breakdowns[key] = rows
	}
	rows := []Row{}
	err = s.db.NewRaw(`SELECT name AS value, COUNT(*) AS count, COUNT(DISTINCT visitor) AS visitors FROM events
		WHERE hostname IN (?) AND created_at >= ? AND created_at < ? AND name <> ''
		GROUP BY value ORDER BY count DESC, value LIMIT ?`, hosts, from, to, topN).Scan(ctx, &rows)
	if err != nil {
		return st, err
	}
	st.Breakdowns["events"] = rows
	st.Breakdowns["props"], err = s.props(ctx, hosts, from, to)
	return st, err
}

// propsScan caps the custom events props are counted from.
// shortcut: counted in Go from the newest 10000 events with props (JSON
// functions differ between SQLite and Postgres); move to SQL if sites send
// far more.
const propsScan = 10000

// props counts "event: key=value" over the custom events' props.
func (s *Store) props(ctx context.Context, hosts any, from, to int64) ([]Row, error) {
	var evs []struct {
		Name, Visitor, Props string
	}
	err := s.db.NewRaw(`SELECT name, visitor, props FROM events
		WHERE hostname IN (?) AND created_at >= ? AND created_at < ? AND name <> '' AND props <> ''
		ORDER BY created_at DESC LIMIT ?`, hosts, from, to, propsScan).Scan(ctx, &evs)
	if err != nil {
		return nil, err
	}
	counts := map[string]*Row{}
	seen := map[[2]string]bool{}
	for _, e := range evs {
		var p map[string]any
		if json.Unmarshal([]byte(e.Props), &p) != nil {
			continue
		}
		for k, v := range p {
			key := fmt.Sprintf("%s: %s=%v", e.Name, k, v)
			r := counts[key]
			if r == nil {
				r = &Row{Value: key}
				counts[key] = r
			}
			r.Count++
			if !seen[[2]string{key, e.Visitor}] {
				seen[[2]string{key, e.Visitor}] = true
				r.Visitors++
			}
		}
	}
	rows := make([]Row, 0, len(counts))
	for _, r := range counts {
		rows = append(rows, *r)
	}
	slices.SortFunc(rows, func(a, b Row) int {
		if a.Count != b.Count {
			return b.Count - a.Count
		}
		return strings.Compare(a.Value, b.Value)
	})
	return rows[:min(len(rows), topN)], nil
}
