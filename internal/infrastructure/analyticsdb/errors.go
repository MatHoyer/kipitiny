package analyticsdb

import (
	"github.com/kipit-labs/kipitiny/internal/domain/analytics"

	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/kipit-labs/kipitiny/internal/ids"
)

// Caps of what is kept per issue: the latest events, and a minute's.
const (
	keptEvents      = 100
	keptPerMinute   = 10
	errorEventsDays = 30
)

// SetErrorProjects replaces the projects taking errors. Issues of the
// others stay.
func (s *Store) SetErrorProjects(ctx context.Context, ps []analytics.ErrorProject) error {
	m := make(map[int64]string, len(ps))
	for _, p := range ps {
		if p.ID < 1 || p.Key == "" {
			return fmt.Errorf("%w: a project needs an id and a key", analytics.ErrInvalid)
		}
		m[p.ID] = p.Key
	}
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw("DELETE FROM error_projects").Exec(ctx); err != nil {
			return err
		}
		for id, key := range m {
			if _, err := tx.NewRaw("INSERT INTO error_projects (id, key) VALUES (?, ?)", id, key).Exec(ctx); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.errorKeys = m
	s.mu.Unlock()
	return nil
}

func (s *Store) loadErrorProjects(ctx context.Context) error {
	var ps []analytics.ErrorProject
	if err := s.db.NewRaw("SELECT id, key FROM error_projects").Scan(ctx, &ps); err != nil {
		return err
	}
	s.errorKeys = make(map[int64]string, len(ps))
	for _, p := range ps {
		s.errorKeys[p.ID] = p.Key
	}
	return nil
}

// ErrorKey is project's DSN key, "" when it takes no errors.
func (s *Store) ErrorKey(project int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.errorKeys[project]
}

// AddError adds r to its issue in project, opening one for a new
// fingerprint and reopening a resolved one.
func (s *Store) AddError(ctx context.Context, project int64, r analytics.ErrorRecord) (analytics.Recorded, error) {
	now := s.now().Unix()
	ev, fp, title, culprit := r.Event, r.Fingerprint, r.Title, r.Culprit
	var rec analytics.Recorded
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var cur struct {
			ID     int64
			Status string
		}
		err := tx.NewRaw("SELECT id, status FROM error_issues WHERE project = ? AND fingerprint = ?", project, fp).Scan(ctx, &cur)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			rec.New = true
			err = tx.NewRaw(`INSERT INTO error_issues (project, fingerprint, title, culprit, level, platform, status,
				first_seen, last_seen, events, users, first_release, last_release, resolved_at, regressed_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, 0, ?, ?, 0, 0) RETURNING id`,
				project, fp, title, culprit, ev.Level, ev.Platform, analytics.IssueOpen, now, now, ev.Release, ev.Release).Scan(ctx, &rec.Issue)
		case err == nil:
			rec.Issue = cur.ID
			status := cur.Status
			if status == analytics.IssueResolved {
				status, rec.Regressed = analytics.IssueOpen, true
			}
			_, err = tx.NewRaw(`UPDATE error_issues SET title = ?, culprit = ?, level = ?, status = ?, last_seen = ?,
				events = events + 1, last_release = ?, regressed_at = CASE WHEN ? THEN ? ELSE regressed_at END WHERE id = ?`,
				title, culprit, ev.Level, status, now, ev.Release, rec.Regressed, now, cur.ID).Exec(ctx)
		}
		if err != nil {
			return err
		}
		kind := ""
		switch {
		case rec.New:
			kind = "new"
		case rec.Regressed:
			kind = "regression"
		}
		if kind != "" {
			if _, err := tx.NewRaw("INSERT INTO error_changes (project, issue, kind, at) VALUES (?, ?, ?, ?)",
				project, rec.Issue, kind, now).Exec(ctx); err != nil {
				return err
			}
		}
		if r.User != "" {
			res, err := tx.NewRaw("INSERT INTO error_users (issue, user) VALUES (?, ?) ON CONFLICT DO NOTHING", rec.Issue, r.User).Exec(ctx)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				if _, err := tx.NewRaw("UPDATE error_issues SET users = users + 1 WHERE id = ?", rec.Issue).Exec(ctx); err != nil {
					return err
				}
			}
		}
		var n int
		if err := tx.NewRaw(`INSERT INTO error_counts (issue, release, minute, n) VALUES (?, ?, ?, 1)
			ON CONFLICT (issue, release, minute) DO UPDATE SET n = error_counts.n + 1 RETURNING n`,
			rec.Issue, ev.Release, now-now%60).Scan(ctx, &n); err != nil {
			return err
		}
		if n > keptPerMinute {
			return nil // a crash loop: counted, not kept
		}
		data, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		if _, err := tx.NewRaw("INSERT INTO error_events (id, issue, created_at, data) VALUES (?, ?, ?, ?)",
			ids.New(), rec.Issue, now, string(data)).Exec(ctx); err != nil {
			return err
		}
		_, err = tx.NewRaw(`DELETE FROM error_events WHERE issue = ? AND id <= (SELECT id FROM error_events
			WHERE issue = ? ORDER BY id DESC LIMIT 1 OFFSET ?)`, rec.Issue, rec.Issue, keptEvents).Exec(ctx)
		return err
	})
	return rec, err
}

var issueSorts = map[string]string{"": "last_seen", "lastSeen": "last_seen", "firstSeen": "first_seen", "events": "events", "users": "users"}

const issueColumns = `id, title, culprit, level, platform, status, first_seen, last_seen, events, users,
	first_release, last_release, resolved_at, regressed_at`

// Issues lists q's issues with their last 24 hours, and how many each
// status has.
func (s *Store) Issues(ctx context.Context, q analytics.IssueQuery) ([]analytics.Issue, map[string]int, error) {
	sort, ok := issueSorts[q.Sort]
	if !ok {
		return nil, nil, fmt.Errorf("%w: sort by lastSeen, firstSeen, events or users", analytics.ErrInvalid)
	}
	if q.Status != "" && q.Status != analytics.IssueOpen && q.Status != analytics.IssueResolved && q.Status != analytics.IssueIgnored {
		return nil, nil, fmt.Errorf("%w: status is open, resolved or ignored", analytics.ErrInvalid)
	}
	if q.Limit < 1 || q.Limit > 100 {
		q.Limit = 50
	}
	where, args := "project = ?", []any{q.Project}
	if q.Search != "" {
		pat := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(strings.ToLower(q.Search)) + "%"
		where += ` AND (lower(title) LIKE ? ESCAPE '\' OR lower(culprit) LIKE ? ESCAPE '\')`
		args = append(args, pat, pat)
	}
	counts := map[string]int{analytics.IssueOpen: 0, analytics.IssueResolved: 0, analytics.IssueIgnored: 0}
	var rows []struct {
		Status string
		N      int
	}
	if err := s.db.NewRaw("SELECT status, count(*) AS n FROM error_issues WHERE "+where+" GROUP BY status", args...).Scan(ctx, &rows); err != nil {
		return nil, nil, err
	}
	for _, r := range rows {
		counts[r.Status] = r.N
	}
	if q.Status != "" {
		where += " AND status = ?"
		args = append(args, q.Status)
	}
	issues := []analytics.Issue{}
	if err := s.db.NewRaw("SELECT "+issueColumns+" FROM error_issues WHERE "+where+" ORDER BY "+sort+" DESC, id DESC LIMIT ? OFFSET ?",
		append(args, q.Limit, q.Offset)...).Scan(ctx, &issues); err != nil {
		return nil, nil, err
	}
	if len(issues) == 0 {
		return issues, counts, nil
	}
	idx := map[int64]int{}
	in := make([]any, len(issues))
	for i, is := range issues {
		idx[is.ID], in[i] = i, is.ID
		issues[i].Spark = make([]int, 24)
	}
	now := s.now().Unix()
	start := now - now%3600 - 23*3600
	var spark []struct {
		Issue int64
		Hour  int64
		N     int
	}
	if err := s.db.NewRaw("SELECT issue, (minute - ?) / 3600 AS hour, sum(n) AS n FROM error_counts WHERE minute >= ? AND issue IN (?) GROUP BY issue, hour",
		start, start, bun.List(in)).Scan(ctx, &spark); err != nil {
		return nil, nil, err
	}
	for _, p := range spark {
		if p.Hour >= 0 && p.Hour < 24 {
			issues[idx[p.Issue]].Spark[p.Hour] += p.N
		}
	}
	return issues, counts, nil
}

// Issue is one of project's issues.
func (s *Store) Issue(ctx context.Context, project, id int64) (analytics.Issue, error) {
	var is analytics.Issue
	err := s.db.NewRaw("SELECT "+issueColumns+" FROM error_issues WHERE project = ? AND id = ?", project, id).Scan(ctx, &is)
	if errors.Is(err, sql.ErrNoRows) {
		return is, fmt.Errorf("%w: issue %d", analytics.ErrNotFound, id)
	}
	return is, err
}

// SetIssueStatus resolves, ignores or reopens an issue.
func (s *Store) SetIssueStatus(ctx context.Context, project, id int64, status string) (analytics.Issue, error) {
	if status != analytics.IssueOpen && status != analytics.IssueResolved && status != analytics.IssueIgnored {
		return analytics.Issue{}, fmt.Errorf("%w: status is open, resolved or ignored", analytics.ErrInvalid)
	}
	resolved := int64(0)
	if status == analytics.IssueResolved {
		resolved = s.now().Unix()
	}
	res, err := s.db.NewRaw("UPDATE error_issues SET status = ?, resolved_at = ? WHERE project = ? AND id = ?", status, resolved, project, id).Exec(ctx)
	if err != nil {
		return analytics.Issue{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return analytics.Issue{}, fmt.Errorf("%w: issue %d", analytics.ErrNotFound, id)
	}
	return s.Issue(ctx, project, id)
}

// Event is the issue's kept event id, its latest when id is empty.
func (s *Store) Event(ctx context.Context, project, issue int64, id string) (analytics.IssueEvent, error) {
	if _, err := s.Issue(ctx, project, issue); err != nil {
		return analytics.IssueEvent{}, err
	}
	var row struct {
		ID   string
		Data string
	}
	q := "SELECT id, data FROM error_events WHERE issue = ? ORDER BY id DESC LIMIT 1"
	args := []any{issue}
	if id != "" {
		q, args = "SELECT id, data FROM error_events WHERE issue = ? AND id = ?", []any{issue, id}
	}
	err := s.db.NewRaw(q, args...).Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return analytics.IssueEvent{}, fmt.Errorf("%w: event", analytics.ErrNotFound)
	}
	if err != nil {
		return analytics.IssueEvent{}, err
	}
	var out analytics.IssueEvent
	if err := json.Unmarshal([]byte(row.Data), &out.Event); err != nil {
		return analytics.IssueEvent{}, err
	}
	for _, x := range []struct {
		dst *string
		q   string
	}{
		{&out.Older, "SELECT id FROM error_events WHERE issue = ? AND id < ? ORDER BY id DESC LIMIT 1"},
		{&out.Newer, "SELECT id FROM error_events WHERE issue = ? AND id > ? ORDER BY id LIMIT 1"},
	} {
		if err := s.db.NewRaw(x.q, issue, row.ID).Scan(ctx, x.dst); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return analytics.IssueEvent{}, err
		}
	}
	if err := s.db.NewRaw("SELECT count(*) FROM error_events WHERE issue = ? AND id >= ?", issue, row.ID).Scan(ctx, &out.Index); err != nil {
		return analytics.IssueEvent{}, err
	}
	err = s.db.NewRaw("SELECT count(*) FROM error_events WHERE issue = ?", issue).Scan(ctx, &out.Kept)
	return out, err
}

// ErrorSeries counts project's events in [from, to), in buckets of
// interval (a whole number of minutes) aligned on from; empty ones too.
func (s *Store) ErrorSeries(ctx context.Context, project int64, from, to time.Time, interval time.Duration) ([]analytics.ErrorPoint, error) {
	iv := int64(interval.Seconds())
	f, t := from.Unix(), to.Unix()
	if iv < 60 || iv%60 != 0 || t <= f || (t-f)/iv > 1000 {
		return nil, fmt.Errorf("%w: range: from < to, at most 1000 buckets of whole minutes", analytics.ErrInvalid)
	}
	var rows []struct {
		B int64
		N int
	}
	if err := s.db.NewRaw(`SELECT (c.minute - ?) / ? AS b, sum(c.n) AS n FROM error_counts c JOIN error_issues i ON i.id = c.issue
		WHERE i.project = ? AND c.minute >= ? AND c.minute < ? GROUP BY b`, f, iv, project, f, t).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	out := make([]analytics.ErrorPoint, (t-f+iv-1)/iv)
	for i := range out {
		out[i].T = f + int64(i)*iv
	}
	for _, r := range rows {
		if r.B >= 0 && int(r.B) < len(out) {
			out[r.B].N += r.N
		}
	}
	return out, nil
}

// pruneErrors deletes events and counts after 30 days, and issues not seen
// for keep.
func (s *Store) pruneErrors(ctx context.Context, keep time.Duration) error {
	now := s.now()
	recent := now.Add(-errorEventsDays * 24 * time.Hour).Unix()
	stale := now.Add(-keep).Unix()
	for _, q := range []struct {
		sql string
		arg int64
	}{
		{"DELETE FROM error_events WHERE created_at < ?", recent},
		{"DELETE FROM error_changes WHERE at < ?", now.Add(-7 * 24 * time.Hour).Unix()},
		{"DELETE FROM error_counts WHERE minute < ?", recent},
		{"DELETE FROM error_users WHERE issue IN (SELECT id FROM error_issues WHERE last_seen < ?)", stale},
		{"DELETE FROM error_events WHERE issue IN (SELECT id FROM error_issues WHERE last_seen < ?)", stale},
		{"DELETE FROM error_counts WHERE issue IN (SELECT id FROM error_issues WHERE last_seen < ?)", stale},
		{"DELETE FROM error_issues WHERE last_seen < ?", stale},
	} {
		if _, err := s.db.NewRaw(q.sql, q.arg).Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

// ErrorChanges are the changes after seq (at most 100, oldest first), and
// the last seq there is.
func (s *Store) ErrorChanges(ctx context.Context, after int64) ([]analytics.ErrorChange, int64, error) {
	cs := []analytics.ErrorChange{}
	if err := s.db.NewRaw(`SELECT c.seq, c.project, c.issue, c.kind, c.at, i.title, i.culprit, i.last_release AS release
		FROM error_changes c JOIN error_issues i ON i.id = c.issue WHERE c.seq > ? ORDER BY c.seq LIMIT 100`, after).Scan(ctx, &cs); err != nil {
		return nil, 0, err
	}
	var last int64
	err := s.db.NewRaw("SELECT coalesce(max(seq), 0) FROM error_changes").Scan(ctx, &last)
	return cs, last, err
}

// ReleaseErrors counts project's events of release in [from, to).
func (s *Store) ReleaseErrors(ctx context.Context, project int64, release string, from, to time.Time) (analytics.ReleaseErrors, error) {
	var r analytics.ReleaseErrors
	if err := s.db.NewRaw(`SELECT coalesce(sum(c.n), 0) FROM error_counts c JOIN error_issues i ON i.id = c.issue
		WHERE i.project = ? AND c.release = ? AND c.minute >= ? AND c.minute < ?`, project, release, from.Unix(), to.Unix()).Scan(ctx, &r.Events); err != nil {
		return r, err
	}
	err := s.db.NewRaw("SELECT count(*) FROM error_issues WHERE project = ? AND first_release = ? AND first_seen >= ? AND first_seen < ?",
		project, release, from.Unix(), to.Unix()).Scan(ctx, &r.NewIssues)
	return r, err
}
