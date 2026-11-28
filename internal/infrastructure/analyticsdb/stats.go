package analyticsdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/kipit-labs/kipitiny/internal/domain/analytics"
	"github.com/uptrace/bun"
)

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
func filters(q analytics.Query) (ev, vi cond) {
	hosts := bun.List(q.Hosts)
	for _, f := range q.Filters {
		if col, ok := analytics.PageviewBreakdowns[f.Key]; ok {
			ev.add(col+" = ?", f.Value)
			vi.add("visits.id IN (SELECT visit_id FROM events WHERE hostname IN (?) AND "+col+" = ?)", hosts, f.Value)
		} else if col, ok := analytics.VisitFilters[f.Key]; ok {
			ev.add("visit_id IN (SELECT id FROM visits WHERE hostname IN (?) AND "+col+" = ?)", hosts, f.Value)
			vi.add(col+" = ?", f.Value)
		} else if f.Key == "events" {
			ev.add("visit_id IN (SELECT visit_id FROM events WHERE hostname IN (?) AND name = ?)", hosts, f.Value)
			vi.add("visits.id IN (SELECT visit_id FROM events WHERE hostname IN (?) AND name = ?)", hosts, f.Value)
		}
	}
	return ev, vi
}

// topN is how many values each breakdown lists.
const topN = 20

func (s *Store) Stats(ctx context.Context, q analytics.Query) (analytics.Stats, error) {
	st := analytics.Stats{Series: []analytics.Point{}, Breakdowns: map[string][]analytics.Row{}}
	if len(q.Hosts) == 0 {
		return st, nil
	}
	hosts := bun.List(q.Hosts)
	from, to := q.From.Unix(), q.To.Unix()
	evf, vif := filters(q)
	// ev and vi select the range's events and visits, filtered; args go first.
	ev := "hostname IN (?) AND created_at >= ? AND created_at < ?" + evf.sql
	vi := "hostname IN (?) AND started_at >= ? AND started_at < ?" + vif.sql
	evArgs := func(more ...any) []any { return append(append([]any{hosts, from, to}, evf.args...), more...) }
	viArgs := func(more ...any) []any { return append(append([]any{hosts, from, to}, vif.args...), more...) }

	summary := func(from, to int64) (analytics.Summary, error) {
		var sm analytics.Summary
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
	for key, col := range analytics.PageviewBreakdowns {
		rows := []analytics.Row{}
		err := s.db.NewRaw(`SELECT ? AS value, COUNT(*) AS count, COUNT(DISTINCT visitor) AS visitors FROM events
			WHERE `+ev+` AND name = '' AND ? <> '' GROUP BY value ORDER BY count DESC, value LIMIT ?`,
			append([]any{bun.Ident(col)}, evArgs(bun.Ident(col), topN)...)...).Scan(ctx, &rows)
		if err != nil {
			return st, err
		}
		st.Breakdowns[key] = rows
	}
	for key, col := range analytics.VisitFilters {
		rows := []analytics.Row{}
		err := s.db.NewRaw(`SELECT ? AS value, COUNT(*) AS count, COUNT(DISTINCT visitor) AS visitors FROM visits
			WHERE `+vi+` AND ? <> '' GROUP BY value ORDER BY count DESC, value LIMIT ?`,
			append([]any{bun.Ident(col)}, viArgs(bun.Ident(col), topN)...)...).Scan(ctx, &rows)
		if err != nil {
			return st, err
		}
		st.Breakdowns[key] = rows
	}
	rows := []analytics.Row{}
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
func (s *Store) props(ctx context.Context, where string, args []any) ([]analytics.Row, error) {
	var evs []struct {
		Name, Visitor, Props string
	}
	err := s.db.NewRaw(`SELECT name, visitor, props FROM events WHERE `+where+` AND name <> '' AND props <> ''
		ORDER BY created_at DESC LIMIT ?`, append(args, propsScan)...).Scan(ctx, &evs)
	if err != nil {
		return nil, err
	}
	counts := map[string]*analytics.Row{}
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
				r = &analytics.Row{Value: key}
				counts[key] = r
			}
			r.Count++
			if !seen[[2]string{key, e.Visitor}] {
				seen[[2]string{key, e.Visitor}] = true
				r.Visitors++
			}
		}
	}
	rows := make([]analytics.Row, 0, len(counts))
	for _, r := range counts {
		rows = append(rows, *r)
	}
	slices.SortFunc(rows, func(a, b analytics.Row) int {
		if a.Count != b.Count {
			return b.Count - a.Count
		}
		return strings.Compare(a.Value, b.Value)
	})
	return rows[:min(len(rows), topN)], nil
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
func (s *Store) Visits(ctx context.Context, q analytics.Query, before string, limit int) ([]analytics.Visit, error) {
	out := []analytics.Visit{}
	if len(q.Hosts) == 0 {
		return out, nil
	}
	if limit <= 0 || limit > maxVisits {
		limit = maxVisits
	}
	_, vif := filters(q)
	where := "visits.hostname IN (?) AND visits.started_at >= ? AND visits.started_at < ?" + vif.sql
	args := append([]any{bun.List(q.Hosts), q.From.Unix(), q.To.Unix()}, vif.args...)
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

// Visit is one of hosts' visits, with its journey.
func (s *Store) Visit(ctx context.Context, hosts []string, id string) (analytics.VisitDetail, error) {
	var d analytics.VisitDetail
	if len(hosts) == 0 {
		return d, analytics.ErrNotFound
	}
	err := s.db.NewRaw("SELECT "+visitCols+" WHERE visits.id = ? AND visits.hostname IN (?)", id, bun.List(hosts)).Scan(ctx, &d.Visit)
	if errors.Is(err, sql.ErrNoRows) {
		return d, analytics.ErrNotFound
	}
	if err != nil {
		return d, err
	}
	d.Props = decodeProps(d.IdentityRaw)
	d.Journey = []analytics.VisitEvent{}
	if err := s.db.NewRaw(`SELECT created_at AS at, name, path, query, title, props FROM events WHERE visit_id = ?
		ORDER BY created_at, id`, id).Scan(ctx, &d.Journey); err != nil {
		return d, err
	}
	for i := range d.Journey {
		d.Journey[i].Props = decodeProps(d.Journey[i].Raw)
	}
	return d, nil
}
