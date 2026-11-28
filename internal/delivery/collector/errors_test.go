package collector

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
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kipit-labs/kipitiny/internal/domain/analytics"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/analyticsdb"
	"github.com/kipit-labs/kipitiny/internal/usecase/ingest"
)

// errorsStore is a store taking errors of projects 7 and 8, its clock, and
// the public handler over it.
func errorsStore(t *testing.T) (*analyticsdb.Store, *time.Time, http.Handler) {
	t.Helper()
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	st, err := analyticsdb.Open(context.Background(), filepath.Join(t.TempDir(), "a.db"), clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.SetErrorProjects(context.Background(), []analytics.ErrorProject{{ID: 7, Key: "k_test"}, {ID: 8, Key: "k_other"}}); err != nil {
		t.Fatal(err)
	}
	return st, &now, Public(ingest.New(st, clock), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

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

	l, counts, err := st.Issues(ctx, analytics.IssueQuery{Project: 7})
	if err != nil {
		t.Fatal(err)
	}
	if len(l) != 2 || counts[analytics.IssueOpen] != 2 {
		t.Fatalf("issues: %+v %v", l, counts)
	}
	byTitle := map[string]analytics.Issue{}
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
	for range ingest.ErrorBurst {
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
