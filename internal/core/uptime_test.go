package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestUptimeDebounce(t *testing.T) {
	r := &uptimeRun{}
	steps := []struct {
		ok      bool
		changed bool
	}{
		{false, false}, {false, false}, {true, false}, // a blip
		{false, false}, {false, false}, {false, true}, // down
		{false, false}, {true, false}, {false, false}, {true, false}, // flapping: still down
		{true, true}, // up
		{false, false},
	}
	for i, s := range steps {
		if got := r.observe(UptimeResult{OK: s.ok, Error: "boom"}); got != s.changed {
			t.Fatalf("step %d: changed=%v, want %v", i, got, s.changed)
		}
	}
	if r.down || len(r.recent) != len(steps) {
		t.Errorf("end: down=%v recent=%d", r.down, len(r.recent))
	}
	for range uptimeRecent {
		r.observe(UptimeResult{OK: true})
	}
	if len(r.recent) != uptimeRecent {
		t.Errorf("recent: %d", len(r.recent))
	}
}

func TestUptimeOver(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 30, 0, 0, time.UTC)
	hours := []store.UptimeHour{
		{Hour: now.Add(-48 * time.Hour).Truncate(time.Hour), Checks: 60, Failures: 60},
		{Hour: now.Add(-2 * time.Hour).Truncate(time.Hour), Checks: 60, Failures: 0, LatencyMS: 6000},
		{Hour: now.Truncate(time.Hour), Checks: 40, Failures: 10, LatencyMS: 3000},
	}
	pct, avg := uptimeOver(hours, now.Add(-24*time.Hour))
	if pct == nil || *pct != 90 || avg == nil || *avg != 100 {
		t.Errorf("24h: %v %v", pct, avg)
	}
	if pct, _ := uptimeOver(hours, now.Add(-7*24*time.Hour)); pct == nil || *pct != 56.25 {
		t.Errorf("7d: %v", pct)
	}
	if pct, avg := uptimeOver(hours[:1], now.Add(-7*24*time.Hour)); *pct != 0 || avg != nil {
		t.Errorf("all down: %v %v", pct, avg)
	}
	if pct, _ := uptimeOver(nil, now); pct != nil {
		t.Errorf("no checks: %v", *pct)
	}
}

func TestUptimeChecks(t *testing.T) {
	ctx := context.Background()
	var status atomic.Int32
	status.Store(503)
	site := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "shop.example.com" || r.URL.Path != "/health" {
			t.Errorf("requested %s%s", r.Host, r.URL.Path)
		}
		w.WriteHeader(int(status.Load()))
	}))
	defer site.Close()
	var mu sync.Mutex
	var sent []string
	discord := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m struct {
			Embeds []struct{ Title, Description string }
		}
		json.NewDecoder(r.Body).Decode(&m)
		mu.Lock()
		sent = append(sent, m.Embeds[0].Title)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer discord.Close()

	c := newTestCore(t, config.Config{})
	c.notifyHTTP = &http.Client{Transport: redirect{discord}}
	c.uptimeHTTP = &http.Client{Transport: redirect{site}}
	if _, err := c.CreateChannel(ctx, ChannelInput{Name: "ops", Kind: "discord", Config: map[string]string{"webhookUrl": webhook}, Events: []string{EventUptimeDown, EventUptimeUp}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	p, err := c.store.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	db, err := c.store.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "db", Kind: store.ServiceKindPostgres, Image: "postgres", Replicas: 1})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := c.store.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "web", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 1, Domain: "shop.example.com"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := c.SetUptime(ctx, db.ID, UptimeInput{Enabled: true}); !errors.Is(err, ErrInvalid) {
		t.Errorf("database: %v", err)
	}
	for _, in := range []UptimeInput{
		{Path: "health"},
		{IntervalSec: 10},
		{IntervalSec: 30, TimeoutSec: 30},
		{ExpectedStatus: 42},
	} {
		if _, err := c.SetUptime(ctx, svc.ID, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", in, err)
		}
	}
	v, err := c.SetUptime(ctx, svc.ID, UptimeInput{Path: "/health", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if v.Check.IntervalSec != defaultUptimeInterval || v.Check.TimeoutSec != defaultUptimeTimeout || v.Problem != "the service is not deployed yet" {
		t.Fatalf("defaults: %+v", v)
	}

	// Not deployed: nothing is requested.
	r := &uptimeRun{}
	c.runUptime(ctx, *v.Check, r)
	if len(r.recent) != 0 {
		t.Fatal("checked an undeployed service")
	}
	dep, err := c.store.CreateDeployment(ctx, store.Deployment{ServiceID: svc.ID, Status: store.DeploymentSucceeded})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.store.SetCurrentDeployment(ctx, svc.ID, dep.ID); err != nil {
		t.Fatal(err)
	}

	run := func(n int) {
		for range n {
			chk, err := c.store.GetUptimeCheck(ctx, svc.ID)
			if err != nil {
				t.Fatal(err)
			}
			c.runUptime(ctx, chk, r)
		}
		c.ops.Wait()
	}
	run(uptimeDownAfter)
	if chk, _ := c.store.GetUptimeCheck(ctx, svc.ID); !chk.Down || chk.ChangedAt == nil {
		t.Errorf("not down: %+v", chk)
	}
	if r.recent[0].Status != 503 || r.recent[0].Error != "status 503" {
		t.Errorf("result: %+v", r.recent[0])
	}
	status.Store(200)
	run(uptimeUpAfter + 1)
	mu.Lock()
	if len(sent) != 2 || sent[0] != "shop/web is down" || sent[1] != "shop/web is back up" {
		t.Errorf("sent %q", sent)
	}
	mu.Unlock()

	c.uptime.runs = map[string]*uptimeRun{svc.ID: r}
	v, err = c.Uptime(ctx, svc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v.URL != "https://shop.example.com/health" || v.Problem != "" || len(v.Recent) != 6 || v.Uptime24h == nil || *v.Uptime24h != 50 || v.Check.Down {
		t.Errorf("view: %+v", v)
	}

	if err := c.DeleteUptime(ctx, svc.ID); err != nil {
		t.Fatal(err)
	}
	if v, err := c.Uptime(ctx, svc.ID); err != nil || v.Check != nil || len(v.Recent) != 0 {
		t.Errorf("after delete: %+v %v", v, err)
	}
}
