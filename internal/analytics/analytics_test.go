package analytics

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const chromeUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36"

func open(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	st, err := Open(context.Background(), filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	st.now = func() time.Time { return now }
	if err := st.SetSites(context.Background(), []string{"Shop.example.com"}); err != nil {
		t.Fatal(err)
	}
	return st, &now
}

func hit(t *testing.T, h http.Handler, ua, ip, body string) *http.Response {
	t.Helper()
	req := httptest.NewRequest("POST", "/e", strings.NewReader(body))
	req.Header.Set("User-Agent", ua)
	req.Header.Set("X-Real-Ip", ip)
	req.Header.Set("CF-IPCountry", "FR")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func TestRecordAndStats(t *testing.T) {
	ctx := context.Background()
	st, now := open(t)
	pub := Public(st, slog.New(slog.NewTextHandler(io.Discard, nil)))

	pv := func(ua, ip, url, ref string) {
		res := hit(t, pub, ua, ip, `{"u":"`+url+`","r":"`+ref+`","s":"1920x1080","l":"fr-FR"}`)
		if res.StatusCode != http.StatusNoContent || len(res.Cookies()) > 0 || res.Header.Get("Set-Cookie") != "" {
			t.Fatalf("hit: %d, cookies %v", res.StatusCode, res.Cookies())
		}
	}
	// Visitor A: two pages in one visit, then a new visit after 31 min.
	pv(chromeUA, "1.1.1.1", "https://shop.example.com/?utm_source=news&token=secret", "https://www.google.com/search")
	*now = now.Add(time.Minute)
	pv(chromeUA, "1.1.1.1", "https://shop.example.com/cart", "https://shop.example.com/")
	hit(t, pub, chromeUA, "1.1.1.1", `{"u":"https://shop.example.com/cart","n":"checkout","p":{"plan":"pro","n":2,"x":{"deep":1}}}`)
	*now = now.Add(31 * time.Minute)
	pv(chromeUA, "1.1.1.1", "https://shop.example.com/", "")
	// Visitor B: another UA, same IP.
	pv("Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 Version/17.0 Mobile/15E148 Safari/604.1",
		"1.1.1.1", "https://shop.example.com/", "")
	// Dropped: a bot, an untracked host.
	pv("Googlebot/2.1", "2.2.2.2", "https://shop.example.com/", "")
	pv(chromeUA, "3.3.3.3", "https://other.example.com/", "")

	s, err := st.Stats(ctx, Query{Hosts: []string{"shop.example.com"}, From: now.Add(-time.Hour), To: now.Add(time.Minute), Interval: 30 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if s.Pageviews != 4 || s.Visitors != 2 || s.Visits != 3 || s.Bounces != 2 || s.Duration != 60 || s.Active != 2 {
		t.Errorf("summary = %+v", s)
	}
	top := func(k string) Row {
		if len(s.Breakdowns[k]) == 0 {
			return Row{}
		}
		return s.Breakdowns[k][0]
	}
	if r := top("pages"); r.Value != "/" || r.Count != 3 || r.Visitors != 2 {
		t.Errorf("pages = %+v", s.Breakdowns["pages"])
	}
	if r := top("referrers"); r.Value != "google.com" || len(s.Breakdowns["referrers"]) != 1 {
		t.Errorf("referrers = %+v (the site's own pages aren't referrers)", s.Breakdowns["referrers"])
	}
	if top("queries").Value != "token" || top("utm_source").Value != "news" {
		t.Errorf("queries = %+v, utm = %+v", s.Breakdowns["queries"], s.Breakdowns["utm_source"])
	}
	if top("browsers").Value != "Chrome" || top("countries").Value != "FR" || top("languages").Value != "fr-fr" || top("screens").Value != "1920x1080" {
		t.Errorf("breakdowns = %+v", s.Breakdowns)
	}
	if len(s.Breakdowns["devices"]) != 2 || len(s.Breakdowns["os"]) != 2 {
		t.Errorf("devices = %+v, os = %+v", s.Breakdowns["devices"], s.Breakdowns["os"])
	}
	if r := top("events"); r.Value != "checkout" || r.Count != 1 {
		t.Errorf("events = %+v", s.Breakdowns["events"])
	}
	if p := s.Breakdowns["props"]; len(p) != 2 || p[0].Value != "checkout: n=2" || p[1].Value != "checkout: plan=pro" {
		t.Errorf("props = %+v (nested values dropped)", p)
	}
	if r := top("entries"); r.Value != "/" || r.Count != 3 {
		t.Errorf("entries = %+v", s.Breakdowns["entries"])
	}
	var pvs int
	for _, p := range s.Series {
		pvs += p.Pageviews
	}
	if len(s.Series) != 2 || pvs != 4 {
		t.Errorf("series = %+v", s.Series)
	}

	// Filters: pageview columns keep their events, the others the visits with them.
	filtered := func(fs ...string) Stats {
		t.Helper()
		f, err := ParseFilters(fs)
		if err != nil {
			t.Fatal(err)
		}
		s, err := st.Stats(ctx, Query{Hosts: []string{"shop.example.com"}, From: now.Add(-time.Hour), To: now.Add(time.Minute),
			Interval: 30 * time.Minute, Filters: f})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if f := filtered("pages:/cart"); f.Pageviews != 1 || f.Visitors != 1 || f.Visits != 1 || len(f.Breakdowns["events"]) != 1 {
		t.Errorf("pages:/cart = %+v", f)
	}
	if f := filtered("devices:mobile"); f.Pageviews != 1 || f.Visits != 1 || len(f.Breakdowns["browsers"]) != 1 || f.Breakdowns["browsers"][0].Value != "Safari" {
		t.Errorf("devices:mobile = %+v", f)
	}
	if f := filtered("events:checkout"); f.Pageviews != 2 || f.Visits != 1 || f.Bounces != 0 {
		t.Errorf("events:checkout = %+v (the visit's pageviews)", f)
	}
	if f := filtered("exits:/cart", "countries:FR"); f.Visits != 1 || f.Pageviews != 2 {
		t.Errorf("exits:/cart = %+v", f)
	}
	if f := filtered("countries:DE"); f.Pageviews != 0 || f.Visits != 0 || len(f.Series) != 0 {
		t.Errorf("countries:DE = %+v", f)
	}
	for _, bad := range []string{"props:x", "nokey", "drop:1"} {
		if _, err := ParseFilters([]string{bad}); err == nil {
			t.Errorf("filter %q accepted", bad)
		}
	}

	// Next day: new salt, so the same browser is a new visitor; old salts go.
	*now = now.Add(24 * time.Hour)
	pv(chromeUA, "1.1.1.1", "https://shop.example.com/", "")
	var salts int
	st.db.NewRaw("SELECT COUNT(*) FROM salts").Scan(ctx, &salts)
	if salts != 1 {
		t.Errorf("salts kept = %d", salts)
	}
	s, _ = st.Stats(ctx, Query{Hosts: []string{"shop.example.com"}, From: now.Add(-48 * time.Hour), To: now.Add(time.Minute), Interval: 24 * time.Hour})
	if s.Visitors != 3 {
		t.Errorf("visitors over two days = %d, want 3", s.Visitors)
	}

	if err := st.Prune(ctx, 12*time.Hour); err != nil {
		t.Fatal(err)
	}
	s, _ = st.Stats(ctx, Query{Hosts: []string{"shop.example.com"}, From: now.Add(-48 * time.Hour), To: now.Add(time.Minute), Interval: 24 * time.Hour})
	if s.Pageviews != 1 || s.Visits != 1 {
		t.Errorf("after prune = %+v", s)
	}
}

func TestPrivateNeedsToken(t *testing.T) {
	st, _ := open(t)
	srv := httptest.NewServer(Private(st, "secret"))
	defer srv.Close()
	for _, tok := range []string{"", "wrong"} {
		c := &Client{BaseURL: srv.URL, Token: tok}
		if err := c.SetSites(context.Background(), []string{"x.com"}); err == nil || !strings.Contains(err.Error(), "401") {
			t.Errorf("token %q: %v", tok, err)
		}
	}
	c := &Client{BaseURL: srv.URL, Token: "secret"}
	if err := c.SetSites(context.Background(), []string{"x.com"}); err != nil {
		t.Fatal(err)
	}
	if !st.tracked("x.com") || st.tracked("shop.example.com") {
		t.Error("sites not replaced")
	}
	now := time.Now()
	if _, err := c.Stats(context.Background(), Query{Hosts: []string{"x.com"}, From: now.Add(-time.Hour), To: now, Interval: time.Minute}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Stats(context.Background(), Query{Hosts: []string{"x.com"}, From: now.Add(-30 * 24 * time.Hour), To: now, Interval: time.Minute}); err == nil {
		t.Error("43200 buckets accepted")
	}
}

func TestUA(t *testing.T) {
	for _, c := range []struct{ ua, browser, os, device string }{
		{chromeUA, "Chrome", "Windows", "desktop"},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/605.1.15 Version/17.0 Safari/605.1.15", "Safari", "macOS", "desktop"},
		{"Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0", "Firefox", "Linux", "desktop"},
		{"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 Chrome/130.0 Mobile Safari/537.36 EdgA/130.0", "Edge", "Android", "mobile"},
		{"Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X) AppleWebKit/605.1.15 Version/17.0 Safari/604.1", "Safari", "iOS", "tablet"},
	} {
		if b, o, d := first(c.ua, browsers), first(c.ua, systems), device(c.ua, 0); b != c.browser || o != c.os || d != c.device {
			t.Errorf("%s: %s %s %s", c.ua, b, o, d)
		}
	}
	if !isBot("curl/8.0") || !isBot("") || isBot(chromeUA) {
		t.Error("bot detection")
	}
}
