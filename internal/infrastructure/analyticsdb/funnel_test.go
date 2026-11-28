package analyticsdb

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kipit-labs/kipitiny/internal/delivery/collector"
	"github.com/kipit-labs/kipitiny/internal/domain/analytics"
	analyticsclient "github.com/kipit-labs/kipitiny/internal/infrastructure/analytics"
	"github.com/kipit-labs/kipitiny/internal/usecase/ingest"
)

func TestFunnel(t *testing.T) {
	ctx := context.Background()
	st, now := open(t)
	pub := collector.Public(ingest.New(st, st.now), slog.New(slog.NewTextHandler(io.Discard, nil)))
	start := *now
	send := func(ip, body string) { hit(t, pub, chromeUA, ip, body) }
	pv := func(ip, path string) { send(ip, `{"u":"https://shop.example.com`+path+`"}`) }
	ev := func(ip, name string) { send(ip, `{"u":"https://shop.example.com/","n":"`+name+`"}`) }
	// A: / → /pricing → signup. B: / → /pricing. C: /pricing before /, then
	// signup: only step 1. D: signup with nothing before. E: / → signup, no /pricing.
	pv("1.0.0.1", "/")
	pv("1.0.0.1", "/pricing")
	ev("1.0.0.1", "signup")
	pv("1.0.0.2", "/")
	pv("1.0.0.2", "/pricing")
	pv("1.0.0.3", "/pricing")
	pv("1.0.0.3", "/")
	ev("1.0.0.4", "signup")
	pv("1.0.0.5", "/")
	ev("1.0.0.5", "signup")
	// A's next visit, after 31 minutes, starts over.
	*now = now.Add(31 * time.Minute)
	pv("1.0.0.1", "/pricing")
	ev("1.0.0.1", "signup")

	q := analytics.Query{Hosts: []string{"shop.example.com"}, From: start.Add(-time.Hour), To: now.Add(time.Minute)}
	steps, err := analytics.ParseSteps([]string{"page:/", "page:/pricing", "event:signup"})
	if err != nil {
		t.Fatal(err)
	}
	fs, err := st.Funnel(ctx, q, steps)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 3 || fs[0].Visits != 4 || fs[1].Visits != 2 || fs[2].Visits != 1 || fs[2].Step != "event:signup" {
		t.Errorf("funnel = %+v, want 4 → 2 → 1", fs)
	}
	// The same page twice needs two pageviews of it.
	twice, _ := analytics.ParseSteps([]string{"page:/pricing", "page:/pricing"})
	if fs, _ := st.Funnel(ctx, q, twice); fs[0].Visits != 4 || fs[1].Visits != 0 {
		t.Errorf("twice = %+v", fs)
	}
	// Step 1 in the range: the later visit only.
	q.From = now.Add(-time.Minute)
	if fs, _ := st.Funnel(ctx, q, steps[1:]); fs[0].Visits != 1 || fs[1].Visits != 1 {
		t.Errorf("later range = %+v", fs)
	}
	for _, bad := range [][]string{{"page:/"}, {"page:/", "click:x"}, {"page:/", "event:"}} {
		if _, err := analytics.ParseSteps(bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}

	srv := httptest.NewServer(collector.Private(ingest.New(st, st.now), "secret"))
	defer srv.Close()
	c := &analyticsclient.Client{BaseURL: srv.URL, Token: "secret"}
	q.Interval = time.Hour
	q.From = start.Add(-time.Hour)
	if fs, err := c.Funnel(ctx, q, []string{"page:/", "event:signup"}); err != nil || fs[0].Visits != 4 || fs[1].Visits != 2 {
		t.Errorf("client: %+v %v", fs, err)
	}
	if _, err := c.Funnel(ctx, q, []string{"page:/"}); err == nil {
		t.Error("one step accepted")
	}
}
