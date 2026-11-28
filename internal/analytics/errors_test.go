package analytics

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Fixtures are envelopes captured from sentry-python 3.0.1 and
// @sentry/node 11.19.0 (server_name renamed).
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func errorsStore(t *testing.T) (*Store, *time.Time, http.Handler) {
	t.Helper()
	st, now := open(t)
	if err := st.SetErrorProjects(context.Background(), []ErrorProject{{ID: 7, Key: "k_test"}, {ID: 8, Key: "k_other"}}); err != nil {
		t.Fatal(err)
	}
	return st, now, Public(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func post(t *testing.T, h http.Handler, target string, body []byte, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", target, bytes.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func gz(b []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write(b) // into a bytes.Buffer: never fails
	w.Close()
	return buf.Bytes()
}

const pyAuth = "Sentry sentry_key=k_test, sentry_version=7, sentry_client=sentry.python/3.0.1"

func TestIngestSDKEnvelopes(t *testing.T) {
	ctx := context.Background()
	st, _, pub := errorsStore(t)

	res := post(t, pub, "/api/7/envelope/", gz(fixture(t, "python-exception.envelope")),
		map[string]string{"Content-Encoding": "gzip", "X-Sentry-Auth": pyAuth, "CF-IPCountry": "FR"})
	var out struct{ ID string }
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if res.Code != 200 || out.ID != "577a9f35aa76458f898743e8c9cde3f3" || res.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("python: %d %+v", res.Code, out)
	}
	// Node: query auth, items without length; its session envelope is dropped.
	q := "?sentry_version=7&sentry_key=k_test&sentry_client=sentry.javascript.node%2F11.19.0"
	for _, f := range []string{"node-session.envelope", "node-exception.envelope"} {
		if res := post(t, pub, "/api/7/envelope/"+q, fixture(t, f), nil); res.Code != 200 {
			t.Fatalf("%s: %d %s", f, res.Code, res.Header().Get("X-Sentry-Error"))
		}
	}

	l, counts, err := st.Issues(ctx, IssueQuery{Project: 7})
	if err != nil {
		t.Fatal(err)
	}
	if len(l) != 2 || counts[IssueOpen] != 2 {
		t.Fatalf("issues: %+v %v", l, counts)
	}
	byTitle := map[string]Issue{}
	for _, is := range l {
		byTitle[is.Title] = is
	}
	py, node := byTitle["KeyError: 'profile'"], byTitle["TypeError: Cannot read properties of undefined (reading 'id')"]
	if py.Culprit != "get_user · app.py:7" || py.Users != 1 || py.FirstRelease != "sha-py1" || py.Platform != "python" || py.Level != "error" {
		t.Errorf("python issue: %+v", py)
	}
	if node.Culprit != "getUser · /app/app.mjs:4" || node.LastRelease != "sha-node1" || node.Spark[23] != 1 {
		t.Errorf("node issue: %+v", node)
	}

	ev, err := st.Event(ctx, 7, py.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	e := ev.Event
	x := e.Exceptions[0]
	top := x.Frames[len(x.Frames)-1]
	if e.User != "u_42" || e.Country != "FR" || e.Tags["plan"] != "pro" || e.Contexts["runtime"] != "CPython 3.13.18" ||
		len(e.Breadcrumbs) != 1 || e.Breadcrumbs[0].Message != "GET /api/session" || e.Breadcrumbs[0].At == 0 ||
		top.Function != "get_user" || !top.InApp || top.Context[top.ContextLine] != `    return u["profile"]["id"]` ||
		x.Handled == nil || !*x.Handled || ev.Index != 1 || ev.Kept != 1 || e.At == 0 {
		t.Errorf("event: %+v", ev)
	}
	raw, _ := json.Marshal(e)
	for _, leak := range []string{"certifi", "__builtins__", "sys.argv"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("kept %q: %s", leak, raw)
		}
	}
}

func TestIngestRefusals(t *testing.T) {
	_, _, pub := errorsStore(t)
	env := fixture(t, "python-exception.envelope")
	for _, c := range []struct {
		name, target string
		hdr          map[string]string
		body         []byte
		want         int
	}{
		{"unknown project", "/api/9/envelope/", map[string]string{"X-Sentry-Auth": pyAuth}, env, 403},
		{"other project's key", "/api/8/envelope/", map[string]string{"X-Sentry-Auth": pyAuth}, env, 403},
		{"no key", "/api/7/envelope/", nil, env, 403},
		{"keys differ", "/api/7/envelope/?sentry_key=k_other", map[string]string{"X-Sentry-Auth": pyAuth}, env, 400},
		{"brotli", "/api/7/envelope/", map[string]string{"X-Sentry-Auth": pyAuth, "Content-Encoding": "br"}, env, 415},
		{"not gzip", "/api/7/envelope/", map[string]string{"X-Sentry-Auth": pyAuth, "Content-Encoding": "gzip"}, env, 400},
		{"too big", "/api/7/envelope/", map[string]string{"X-Sentry-Auth": pyAuth}, make([]byte, maxEnvelope+1), 413},
		{"bomb", "/api/7/envelope/", map[string]string{"X-Sentry-Auth": pyAuth, "Content-Encoding": "gzip"}, gz(make([]byte, maxDecoded+1)), 413},
		{"bad length", "/api/7/envelope/", map[string]string{"X-Sentry-Auth": pyAuth}, []byte("{}\n{\"type\":\"event\",\"length\":2}\n{}x"), 400},
	} {
		if res := post(t, pub, c.target, c.body, c.hdr); res.Code != c.want {
			t.Errorf("%s: %d (%s), want %d", c.name, res.Code, res.Header().Get("X-Sentry-Error"), c.want)
		}
	}
	// The envelope header's DSN authenticates too (the legacy /store/ takes
	// a bare event, with its key in the query).
	body := []byte(`{"dsn":"https://k_test@stats.example.com/7"}` + "\n" + `{"type":"event"}` + "\n" + `{"message":"hi"}`)
	if res := post(t, pub, "/api/7/envelope/", body, nil); res.Code != 200 {
		t.Errorf("dsn header: %d", res.Code)
	}
	if res := post(t, pub, "/api/7/store/?sentry_key=k_test", []byte(`{"message":"hi"}`), nil); res.Code != 200 {
		t.Errorf("store: %d", res.Code)
	}
}

func TestIngestRateLimit(t *testing.T) {
	_, now, pub := errorsStore(t)
	body := []byte(`{"message":"loop"}`)
	for range errorBurst {
		if res := post(t, pub, "/api/7/store/?sentry_key=k_test", body, nil); res.Code != 200 {
			t.Fatalf("within the burst: %d", res.Code)
		}
	}
	res := post(t, pub, "/api/7/store/?sentry_key=k_test", body, nil)
	if res.Code != 429 || res.Header().Get("Retry-After") != "10" || res.Header().Get("X-Sentry-Rate-Limits") != "10::key" {
		t.Fatalf("over: %d %v", res.Code, res.Header())
	}
	if res := post(t, pub, "/api/8/store/?sentry_key=k_other", body, nil); res.Code != 200 {
		t.Errorf("another project: %d", res.Code)
	}
	*now = now.Add(time.Second)
	if res := post(t, pub, "/api/7/store/?sentry_key=k_test", body, nil); res.Code != 200 {
		t.Errorf("refilled: %d", res.Code)
	}
}

func record(t *testing.T, st *Store, project int64, event string) Recorded {
	t.Helper()
	p, err := parseEvent([]byte(event), st.now())
	if err != nil {
		t.Fatal(err)
	}
	r, err := st.RecordError(context.Background(), project, p)
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
	if is, err = st.SetIssueStatus(ctx, 7, first.Issue, IssueResolved); err != nil || is.ResolvedAt == 0 {
		t.Fatalf("resolve: %+v %v", is, err)
	}
	*now = now.Add(time.Hour)
	r := record(t, st, 7, `{"message":"boom","release":"r2"}`)
	is, _ = st.Issue(ctx, 7, first.Issue)
	if !r.Regressed || is.Status != IssueOpen || is.RegressedAt != now.Unix() || is.LastRelease != "r2" || is.FirstRelease != "r1" {
		t.Errorf("regression: %+v %+v", r, is)
	}
	// Ignored stays ignored.
	if _, err := st.SetIssueStatus(ctx, 7, first.Issue, IssueIgnored); err != nil {
		t.Fatal(err)
	}
	if r := record(t, st, 7, `{"message":"boom"}`); r.Regressed {
		t.Error("ignored issue regressed")
	}
	if is, _ = st.Issue(ctx, 7, first.Issue); is.Status != IssueIgnored {
		t.Errorf("ignored: %+v", is)
	}

	// Lists: counts per status, search, sort.
	record(t, st, 7, `{"message":"other thing"}`)
	l, counts, err := st.Issues(ctx, IssueQuery{Project: 7, Status: IssueOpen})
	if err != nil || len(l) != 1 || l[0].Title != "other thing" || counts[IssueIgnored] != 1 || counts[IssueOpen] != 1 {
		t.Errorf("open: %+v %v %v", l, counts, err)
	}
	if l, _, _ := st.Issues(ctx, IssueQuery{Project: 7, Search: "BOO"}); len(l) != 1 || l[0].ID != first.Issue {
		t.Errorf("search: %+v", l)
	}
	if l, _, _ := st.Issues(ctx, IssueQuery{Project: 7, Search: "%"}); len(l) != 0 {
		t.Errorf("search %%: %+v", l)
	}
	if l, _, _ := st.Issues(ctx, IssueQuery{Project: 7, Sort: "events"}); l[0].ID != first.Issue {
		t.Errorf("by events: %+v", l)
	}
	if _, _, err := st.Issues(ctx, IssueQuery{Project: 7, Sort: "title; DROP TABLE"}); err == nil {
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
	if st.errorKey(7) != "k_test" {
		t.Fatal("not loaded")
	}
	if err := st.SetErrorProjects(context.Background(), []ErrorProject{{ID: 0, Key: "x"}}); err == nil {
		t.Error("project 0 accepted")
	}
	if err := st.SetErrorProjects(context.Background(), nil); err != nil || st.errorKey(7) != "" {
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
	if _, err := st.SetIssueStatus(ctx, 7, a.Issue, IssueResolved); err != nil {
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
