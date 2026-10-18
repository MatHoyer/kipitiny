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

	mu       sync.Mutex
	sites    map[string]bool
	trackers map[string]Tracker // by slug
	day      int64
	salt     []byte
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
	if err := s.loadTrackers(ctx); err != nil {
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
	// Identity is what the page passed to kipitiny.identify, kept on the visit.
	Identity      string
	IP, UserAgent string // hashed into the visitor, never stored
}

// IdentifyEvent is the hit of kipitiny.identify: it names the visit's
// visitor (its props kept with it) instead of being counted.
const IdentifyEvent = "$identify"

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
	if e.Name == IdentifyEvent {
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
	Filters  []Filter
}

// Filter narrows stats to a value of a breakdown (props aside): the
// matching events for a pageview column, the visits with it otherwise.
type Filter struct{ Key, Value string }

// visitFilters are on the visits' own columns.
var visitFilters = map[string]string{"entries": "entry", "exits": "exit", "identities": "identity"}

// ParseFilters reads "<breakdown>:<value>" filters.
func ParseFilters(vs []string) ([]Filter, error) {
	if len(vs) > 10 {
		return nil, errors.New("at most 10 filters")
	}
	fs := []Filter{}
	for _, v := range vs {
		k, val, ok := strings.Cut(v, ":")
		_, pv := pageviewBreakdowns[k]
		_, vi := visitFilters[k]
		if !ok || !(pv || vi || k == "events") || len(val) > 512 {
			return nil, fmt.Errorf("filter %q: want <breakdown>:<value>", v)
		}
		fs = append(fs, Filter{k, val})
	}
	return fs, nil
}

// cond is SQL appended to a WHERE, with its args.
type cond struct {
	sql  string
	args []any
}

func (c *cond) add(sql string, args ...any) {
	c.sql += " AND " + sql
	c.args = append(c.args, args...)
}

// filters are the conditions of q.Filters on events and on visits.
func (q Query) filters() (ev, vi cond) {
	hosts := bun.In(q.Hosts)
	for _, f := range q.Filters {
		if col, ok := pageviewBreakdowns[f.Key]; ok {
			ev.add(col+" = ?", f.Value)
			vi.add("visits.id IN (SELECT visit_id FROM events WHERE hostname IN (?) AND "+col+" = ?)", hosts, f.Value)
		} else if col, ok := visitFilters[f.Key]; ok {
			ev.add("visit_id IN (SELECT id FROM visits WHERE hostname IN (?) AND "+col+" = ?)", hosts, f.Value)
			vi.add(col+" = ?", f.Value)
		} else if f.Key == "events" {
			ev.add("visit_id IN (SELECT visit_id FROM events WHERE hostname IN (?) AND name = ?)", hosts, f.Value)
			vi.add("visits.id IN (SELECT visit_id FROM events WHERE hostname IN (?) AND name = ?)", hosts, f.Value)
		}
	}
	return ev, vi
}

// Summary is a range's totals.
type Summary struct {
	Pageviews int `json:"pageviews"`
	Visitors  int `json:"visitors"`
	Visits    int `json:"visits"`
	Bounces   int `json:"bounces"`
	// Duration sums the visits' lengths, in seconds.
	Duration int64 `json:"duration"`
}

type Stats struct {
	Summary
	// Previous is the same span just before, to compare with.
	Previous Summary `json:"previous"`
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
	evf, vif := q.filters()
	// ev and vi select the range's events and visits, filtered; args go first.
	ev := "hostname IN (?) AND created_at >= ? AND created_at < ?" + evf.sql
	vi := "hostname IN (?) AND started_at >= ? AND started_at < ?" + vif.sql
	evArgs := func(more ...any) []any { return append(append([]any{hosts, from, to}, evf.args...), more...) }
	viArgs := func(more ...any) []any { return append(append([]any{hosts, from, to}, vif.args...), more...) }

	summary := func(from, to int64) (Summary, error) {
		var sm Summary
		args := func(f cond) []any { return append([]any{hosts, from, to}, f.args...) }
		err := s.db.NewRaw(`SELECT COUNT(CASE WHEN name = '' THEN 1 END), COUNT(DISTINCT visitor) FROM events WHERE `+ev,
			args(evf)...).Scan(ctx, &sm.Pageviews, &sm.Visitors)
		if err != nil {
			return sm, err
		}
		err = s.db.NewRaw(`SELECT COUNT(*), COUNT(CASE WHEN pageviews <= 1 THEN 1 END), COALESCE(SUM(last_at - started_at), 0) FROM visits WHERE `+vi,
			args(vif)...).Scan(ctx, &sm.Visits, &sm.Bounces, &sm.Duration)
		return sm, err
	}
	var err error
	if st.Summary, err = summary(from, to); err != nil {
		return st, err
	}
	if st.Previous, err = summary(2*from-to, from); err != nil {
		return st, err
	}
	err = s.db.NewRaw("SELECT COUNT(DISTINCT visitor) FROM events WHERE hostname IN (?) AND created_at >= ?"+evf.sql,
		append([]any{hosts, s.now().Unix() - visitGap}, evf.args...)...).Scan(ctx, &st.Active)
	if err != nil {
		return st, err
	}
	iv, off := max(int64(q.Interval.Seconds()), 60), int64(q.Offset)
	err = s.db.NewRaw(`SELECT (created_at + ?) / ? * ? - ? AS t, COUNT(CASE WHEN name = '' THEN 1 END) AS pageviews,
		COUNT(DISTINCT visitor) AS visitors FROM events WHERE `+ev+` GROUP BY t ORDER BY t`,
		append([]any{off, iv, iv, off}, evArgs()...)...).Scan(ctx, &st.Series)
	if err != nil {
		return st, err
	}
	for key, col := range pageviewBreakdowns {
		rows := []Row{}
		err := s.db.NewRaw(`SELECT ? AS value, COUNT(*) AS count, COUNT(DISTINCT visitor) AS visitors FROM events
			WHERE `+ev+` AND name = '' AND ? <> '' GROUP BY value ORDER BY count DESC, value LIMIT ?`,
			append([]any{bun.Ident(col)}, evArgs(bun.Ident(col), topN)...)...).Scan(ctx, &rows)
		if err != nil {
			return st, err
		}
		st.Breakdowns[key] = rows
	}
	for key, col := range visitFilters {
		rows := []Row{}
		err := s.db.NewRaw(`SELECT ? AS value, COUNT(*) AS count, COUNT(DISTINCT visitor) AS visitors FROM visits
			WHERE `+vi+` AND ? <> '' GROUP BY value ORDER BY count DESC, value LIMIT ?`,
			append([]any{bun.Ident(col)}, viArgs(bun.Ident(col), topN)...)...).Scan(ctx, &rows)
		if err != nil {
			return st, err
		}
		st.Breakdowns[key] = rows
	}
	rows := []Row{}
	err = s.db.NewRaw(`SELECT name AS value, COUNT(*) AS count, COUNT(DISTINCT visitor) AS visitors FROM events
		WHERE `+ev+` AND name <> '' GROUP BY value ORDER BY count DESC, value LIMIT ?`, evArgs(topN)...).Scan(ctx, &rows)
	if err != nil {
		return st, err
	}
	st.Breakdowns["events"] = rows
	st.Breakdowns["props"], err = s.props(ctx, ev, evArgs())
	return st, err
}

// propsScan caps the custom events props are counted from.
// shortcut: counted in Go from the newest 10000 events with props (JSON
// functions differ between SQLite and Postgres); move to SQL if sites send
// far more.
const propsScan = 10000

// props counts "event: key=value" over the custom events' props.
func (s *Store) props(ctx context.Context, where string, args []any) ([]Row, error) {
	var evs []struct {
		Name, Visitor, Props string
	}
	err := s.db.NewRaw(`SELECT name, visitor, props FROM events WHERE `+where+` AND name <> '' AND props <> ''
		ORDER BY created_at DESC LIMIT ?`, append(args, propsScan)...).Scan(ctx, &evs)
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

// Visit is one visit, as its first event saw the visitor.
type Visit struct {
	ID        string `json:"id"`
	StartedAt int64  `json:"startedAt"`
	LastAt    int64  `json:"lastAt"`
	Pageviews int    `json:"pageviews"`
	// Events counts the custom events.
	Events      int            `json:"events"`
	Entry       string         `json:"entry"`
	Exit        string         `json:"exit"`
	Referrer    string         `json:"referrer"`
	UTMSource   string         `json:"utmSource"`
	UTMMedium   string         `json:"utmMedium"`
	UTMCampaign string         `json:"utmCampaign"`
	Country     string         `json:"country"`
	Browser     string         `json:"browser"`
	OS          string         `json:"os"`
	Device      string         `json:"device"`
	Screen      string         `json:"screen"`
	Language    string         `json:"language"`
	Identity    string         `json:"identity"`
	IdentityRaw string         `json:"-" bun:"identity_props"`
	Props       map[string]any `json:"identityProps,omitempty" bun:"-"`
}

// VisitEvent is a step of a visit's journey: a pageview (Name empty) or a
// custom event.
type VisitEvent struct {
	At    int64          `json:"at"`
	Name  string         `json:"name"`
	Path  string         `json:"path"`
	Query string         `json:"query"`
	Title string         `json:"title"`
	Raw   string         `json:"-" bun:"props"`
	Props map[string]any `json:"props,omitempty" bun:"-"`
}

type VisitDetail struct {
	Visit
	Journey []VisitEvent `json:"journey"`
}

// visitCols select a Visit from visits v and its first event f.
const visitCols = `visits.id, visits.started_at, visits.last_at, visits.pageviews, visits.entry, visits.exit,
	visits.identity, visits.identity_props, (SELECT COUNT(*) FROM events WHERE visit_id = visits.id AND name <> '') AS events,
	f.referrer, f.utm_source, f.utm_medium, f.utm_campaign, f.country, f.browser, f.os, f.device, f.screen, f.language
	FROM visits JOIN events f ON f.id = (SELECT MIN(id) FROM events WHERE visit_id = visits.id)`

func decodeProps(raw string) map[string]any {
	var m map[string]any
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &m)
	}
	return m
}

// maxVisits caps a page of Visits.
const maxVisits = 100

// Visits lists q's visits, newest first, limit of them started before the
// visit before (all when empty).
func (s *Store) Visits(ctx context.Context, q Query, before string, limit int) ([]Visit, error) {
	out := []Visit{}
	if len(q.Hosts) == 0 {
		return out, nil
	}
	if limit <= 0 || limit > maxVisits {
		limit = maxVisits
	}
	_, vif := q.filters()
	where := "visits.hostname IN (?) AND visits.started_at >= ? AND visits.started_at < ?" + vif.sql
	args := append([]any{bun.In(q.Hosts), q.From.Unix(), q.To.Unix()}, vif.args...)
	if before != "" {
		where += " AND visits.id < ?"
		args = append(args, before)
	}
	err := s.db.NewRaw("SELECT "+visitCols+" WHERE "+where+" ORDER BY visits.id DESC LIMIT ?", append(args, limit)...).Scan(ctx, &out)
	for i := range out {
		out[i].Props = decodeProps(out[i].IdentityRaw)
	}
	return out, err
}

// ErrNotFound is a visit that isn't there, or not on the hosts asked.
var ErrNotFound = errors.New("not found")

// Visit is one of hosts' visits, with its journey.
func (s *Store) Visit(ctx context.Context, hosts []string, id string) (VisitDetail, error) {
	var d VisitDetail
	if len(hosts) == 0 {
		return d, ErrNotFound
	}
	err := s.db.NewRaw("SELECT "+visitCols+" WHERE visits.id = ? AND visits.hostname IN (?)", id, bun.In(hosts)).Scan(ctx, &d.Visit)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	if err != nil {
		return d, err
	}
	d.Props = decodeProps(d.IdentityRaw)
	d.Journey = []VisitEvent{}
	if err := s.db.NewRaw(`SELECT created_at AS at, name, path, query, title, props FROM events WHERE visit_id = ?
		ORDER BY created_at, id`, id).Scan(ctx, &d.Journey); err != nil {
		return d, err
	}
	for i := range d.Journey {
		d.Journey[i].Props = decodeProps(d.Journey[i].Raw)
	}
	return d, nil
}
