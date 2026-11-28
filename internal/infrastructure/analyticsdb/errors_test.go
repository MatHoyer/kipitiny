package analyticsdb

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kipit-labs/kipitiny/internal/delivery/collector"
	"github.com/kipit-labs/kipitiny/internal/domain/analytics"
	"github.com/kipit-labs/kipitiny/internal/usecase/ingest"
)

func errorsStore(t *testing.T) (*Store, *time.Time, http.Handler) {
	t.Helper()
	st, now := open(t)
	if err := st.SetErrorProjects(context.Background(), []analytics.ErrorProject{{ID: 7, Key: "k_test"}, {ID: 8, Key: "k_other"}}); err != nil {
		t.Fatal(err)
	}
	return st, now, collector.Public(ingest.New(st, st.now), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func record(t *testing.T, st *Store, project int64, event string) analytics.Recorded {
	t.Helper()
	svc := ingest.New(st, st.now)
	p, err := svc.ParseError([]byte(event))
	if err != nil {
		t.Fatal(err)
	}
	r, err := svc.RecordError(context.Background(), project, p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func jsError(file string, line int) string {
	return `{"platform":"javascript","release":"r1","exception":{"values":[{"type":"TypeError","value":"x is undefined","stacktrace":{"frames":[
		{"function":"vendor","filename":"https://shop.example.com/assets/vendor.js","in_app":false},
		{"function":"load","filename":"` + file + `","lineno":` + strconv.Itoa(line) + `,"in_app":true}]}}]}}`
}

func TestGrouping(t *testing.T) {
	st, _, _ := errorsStore(t)
	// A bundle's hash and the line change with each build: same issue.
	a := record(t, st, 7, jsError("https://shop.example.com/assets/main.3f9a2c71.js?v=1", 1))
	b := record(t, st, 7, jsError("https://cdn.example.com/assets/main.88aa11bb.js", 2))
	if !a.New || b.New || a.Issue != b.Issue {
		t.Errorf("same error, new build: %+v %+v", a, b)
	}
	// Messages differing by ids and numbers: same issue.
	m1 := record(t, st, 7, `{"message":"Payment 1234 failed for order 9f8e7d6c5b4a1203"}`)
	m2 := record(t, st, 7, `{"logentry":{"formatted":"Payment 99 failed for order 0a0b0c0d0e0f0a0b"}}`)
	if m1.Issue != m2.Issue || m1.Issue == a.Issue {
		t.Errorf("messages: %+v %+v", m1, m2)
	}
	// Another project: its own issue.
	if o := record(t, st, 8, jsError("main.js", 1)); !o.New {
		t.Errorf("other project: %+v", o)
	}
	// The SDK's fingerprint wins; {{ default }} extends the default.
	c1 := record(t, st, 7, `{"message":"a","fingerprint":["payments"]}`)
	c2 := record(t, st, 7, `{"message":"b","fingerprint":["payments"]}`)
	d := record(t, st, 7, `{"message":"a","fingerprint":["{{ default }}","eu"]}`)
	if c1.Issue != c2.Issue || d.Issue == c1.Issue || d.Issue == m1.Issue {
		t.Errorf("custom fingerprints: %+v %+v %+v", c1, c2, d)
	}
}

func TestIssueLifecycle(t *testing.T) {
	ctx := context.Background()
	st, now, _ := errorsStore(t)
	first := record(t, st, 7, `{"message":"boom","release":"r1","user":{"id":"u1"}}`)
	record(t, st, 7, `{"message":"boom","release":"r1","user":{"id":"u1"}}`)
	record(t, st, 7, `{"message":"boom","release":"r1","user":{"email":"x@y.z"}}`)
	is, err := st.Issue(ctx, 7, first.Issue)
	if err != nil || is.Events != 3 || is.Users != 2 {
		t.Fatalf("counts: %+v %v", is, err)
	}
	if ev, _ := st.Event(ctx, 7, first.Issue, ""); ev.Event.User != "" {
		t.Errorf("email kept as the user: %+v", ev.Event)
	}
	if _, err := st.Issue(ctx, 8, first.Issue); err == nil {
		t.Error("another project's issue found")
	}
	if _, err := st.SetIssueStatus(ctx, 7, first.Issue, "done"); err == nil {
		t.Error("bad status accepted")
	}
	if is, err = st.SetIssueStatus(ctx, 7, first.Issue, analytics.IssueResolved); err != nil || is.ResolvedAt == 0 {
		t.Fatalf("resolve: %+v %v", is, err)
	}
	*now = now.Add(time.Hour)
	r := record(t, st, 7, `{"message":"boom","release":"r2"}`)
	is, _ = st.Issue(ctx, 7, first.Issue)
	if !r.Regressed || is.Status != analytics.IssueOpen || is.RegressedAt != now.Unix() || is.LastRelease != "r2" || is.FirstRelease != "r1" {
		t.Errorf("regression: %+v %+v", r, is)
	}
	// Ignored stays ignored.
	if _, err := st.SetIssueStatus(ctx, 7, first.Issue, analytics.IssueIgnored); err != nil {
		t.Fatal(err)
	}
	if r := record(t, st, 7, `{"message":"boom"}`); r.Regressed {
		t.Error("ignored issue regressed")
	}
	if is, _ = st.Issue(ctx, 7, first.Issue); is.Status != analytics.IssueIgnored {
		t.Errorf("ignored: %+v", is)
	}

	// Lists: counts per status, search, sort.
	record(t, st, 7, `{"message":"other thing"}`)
	l, counts, err := st.Issues(ctx, analytics.IssueQuery{Project: 7, Status: analytics.IssueOpen})
	if err != nil || len(l) != 1 || l[0].Title != "other thing" || counts[analytics.IssueIgnored] != 1 || counts[analytics.IssueOpen] != 1 {
		t.Errorf("open: %+v %v %v", l, counts, err)
	}
	if l, _, _ := st.Issues(ctx, analytics.IssueQuery{Project: 7, Search: "BOO"}); len(l) != 1 || l[0].ID != first.Issue {
		t.Errorf("search: %+v", l)
	}
	if l, _, _ := st.Issues(ctx, analytics.IssueQuery{Project: 7, Search: "%"}); len(l) != 0 {
		t.Errorf("search %%: %+v", l)
	}
	if l, _, _ := st.Issues(ctx, analytics.IssueQuery{Project: 7, Sort: "events"}); l[0].ID != first.Issue {
		t.Errorf("by events: %+v", l)
	}
	if _, _, err := st.Issues(ctx, analytics.IssueQuery{Project: 7, Sort: "title; DROP TABLE"}); err == nil {
		t.Error("bad sort accepted")
	}
}

func TestKeptEvents(t *testing.T) {
	ctx := context.Background()
	st, now, _ := errorsStore(t)
	var issue int64
	// A crash loop: 15 in a minute are counted, 10 kept.
	for range 15 {
		issue = record(t, st, 7, `{"message":"loop"}`).Issue
	}
	ev, err := st.Event(ctx, 7, issue, "")
	if err != nil || ev.Kept != keptPerMinute || ev.Index != 1 || ev.Newer != "" || ev.Older == "" {
		t.Fatalf("crash loop: %+v %v", ev, err)
	}
	older, err := st.Event(ctx, 7, issue, ev.Older)
	if err != nil || older.Index != 2 || older.Newer != ev.Event.ID && older.Newer == "" {
		t.Errorf("older: %+v %v", older, err)
	}
	// Over minutes, the latest 100.
	for range 12 {
		*now = now.Add(time.Minute)
		for range keptPerMinute {
			record(t, st, 7, `{"message":"loop"}`)
		}
	}
	ev, _ = st.Event(ctx, 7, issue, "")
	is, _ := st.Issue(ctx, 7, issue)
	if ev.Kept != keptEvents || is.Events != 15+12*keptPerMinute {
		t.Errorf("kept %d of %d", ev.Kept, is.Events)
	}

	// Series: per 10 minutes over the last hour.
	from := now.Add(-time.Hour).Truncate(time.Minute)
	ps, err := st.ErrorSeries(ctx, 7, from, now.Add(time.Minute), 10*time.Minute)
	total := 0
	for _, p := range ps {
		total += p.N
	}
	if err != nil || len(ps) != 7 || total != is.Events || ps[0].T != from.Unix() {
		t.Errorf("series: %+v %v", ps, err)
	}
	if _, err := st.ErrorSeries(ctx, 7, from, *now, 30*time.Second); err == nil {
		t.Error("sub-minute buckets accepted")
	}

	// Prune: events and counts after 30 days, the issue once unseen for keep.
	*now = now.Add(31 * 24 * time.Hour)
	if err := st.Prune(ctx, 365*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Event(ctx, 7, issue, ""); err == nil {
		t.Error("old events kept")
	}
	if _, err := st.Issue(ctx, 7, issue); err != nil {
		t.Errorf("issue pruned early: %v", err)
	}
	if err := st.Prune(ctx, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Issue(ctx, 7, issue); err == nil {
		t.Error("stale issue kept")
	}
}

func TestErrorProjectsReload(t *testing.T) {
	st, _, _ := errorsStore(t)
	if st.ErrorKey(7) != "k_test" {
		t.Fatal("not loaded")
	}
	if err := st.SetErrorProjects(context.Background(), []analytics.ErrorProject{{ID: 0, Key: "x"}}); err == nil {
		t.Error("project 0 accepted")
	}
	if err := st.SetErrorProjects(context.Background(), nil); err != nil || st.ErrorKey(7) != "" {
		t.Errorf("cleared: %v", err)
	}
}

func TestErrorChangesAndReleases(t *testing.T) {
	ctx := context.Background()
	st, now, _ := errorsStore(t)
	cs, last, err := st.ErrorChanges(ctx, 0)
	if err != nil || len(cs) != 0 || last != 0 {
		t.Fatalf("empty: %v %d %v", cs, last, err)
	}
	a := record(t, st, 7, `{"message":"a","release":"r1"}`)
	record(t, st, 7, `{"message":"a","release":"r1"}`)
	if _, err := st.SetIssueStatus(ctx, 7, a.Issue, analytics.IssueResolved); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(10 * time.Minute)
	deploy := *now
	record(t, st, 7, `{"message":"a","release":"r2"}`)
	record(t, st, 8, `{"message":"b","release":"r2"}`)
	record(t, st, 7, `{"message":"c","release":"r2"}`)
	cs, last, err = st.ErrorChanges(ctx, 0)
	kinds := []string{}
	for _, c := range cs {
		kinds = append(kinds, c.Kind+":"+c.Title)
	}
	if err != nil || strings.Join(kinds, " ") != "new:a regression:a new:b new:c" || last != cs[3].Seq || cs[1].Release != "r2" || cs[2].Project != 8 {
		t.Fatalf("changes: %v %d %v", kinds, last, err)
	}
	if cs, _, _ := st.ErrorChanges(ctx, cs[1].Seq); len(cs) != 2 {
		t.Errorf("after: %v", cs)
	}
	r1, _ := st.ReleaseErrors(ctx, 7, "r1", deploy.Add(-time.Hour), deploy)
	r2, err := st.ReleaseErrors(ctx, 7, "r2", deploy, now.Add(time.Minute))
	if err != nil || r1.Events != 2 || r1.NewIssues != 1 || r2.Events != 2 || r2.NewIssues != 1 {
		t.Errorf("releases: %+v %+v %v", r1, r2, err)
	}
}
