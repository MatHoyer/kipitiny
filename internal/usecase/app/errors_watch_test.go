package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/delivery/collector"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/domain/analytics"
	analyticsclient "github.com/kipit-labs/kipitiny/internal/infrastructure/analytics"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/analyticsdb"
	"github.com/kipit-labs/kipitiny/internal/usecase/deploy"
	"github.com/kipit-labs/kipitiny/internal/usecase/ingest"
	"github.com/kipit-labs/kipitiny/internal/usecase/monitoring"
)

func TestErrorAlerts(t *testing.T) {
	ctx := context.Background()
	ast, err := analyticsdb.Open(ctx, filepath.Join(t.TempDir(), "a.db"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer ast.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := ingest.New(ast, time.Now)
	pub := httptest.NewServer(collector.Public(svc, log))
	defer pub.Close()
	priv := httptest.NewServer(collector.Private(svc, "tok"))
	defer priv.Close()
	cl := &analyticsclient.Client{BaseURL: priv.URL, Token: "tok"}

	var mu sync.Mutex
	var sent []string
	hook := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m struct {
			Embeds []struct{ Title, Description, URL string }
		}
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		sent = append(sent, m.Embeds[0].Title+" | "+m.Embeds[0].Description+" | "+m.Embeds[0].URL)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer hook.Close()
	c := newTestCore(t, config.Config{Domain: "kipi.example.com", Traefik: config.Traefik{Enabled: true}})
	c.NotifyHTTP = &http.Client{Transport: redirect{hook}}
	if _, err := c.CreateChannel(ctx, monitoring.ChannelInput{Name: "d", Kind: "discord", Config: map[string]string{"webhookUrl": webhook}, Enabled: true,
		Events: []string{domain.EventErrorNew, domain.EventErrorRegression, domain.EventErrorSpike}}); err != nil {
		t.Fatal(err)
	}
	take := func() []string {
		c.ops.Wait()
		mu.Lock()
		defer mu.Unlock()
		out := sent
		sent = nil
		return out
	}

	p, _ := c.store.CreateProject(ctx, domain.Project{Name: "shop", ServerID: domain.LocalServerID})
	pr, _ := c.store.CreateProject(ctx, domain.Project{Name: "shop-pr-1", ParentID: p.ID, ServerID: domain.LocalServerID, Preview: domain.Preview{Name: "pr-1"}})
	v, _ := c.CreateService(ctx, p.ID, deploy.ServiceInput{Name: "web", Image: "shop:v2", Errors: true})
	web, _ := c.store.GetService(ctx, v.ID)
	v, _ = c.CreateService(ctx, pr.ID, deploy.ServiceInput{Name: "web", Image: "shop:v2", Errors: true})
	preview, _ := c.store.GetService(ctx, v.ID)
	if err := cl.SetErrorProjects(ctx, []analytics.ErrorProject{{ID: web.ErrorsProject, Key: web.ErrorsKey}, {ID: preview.ErrorsProject, Key: preview.ErrorsKey}}); err != nil {
		t.Fatal(err)
	}
	report := func(s domain.Service, msg, release string) {
		t.Helper()
		res, err := http.Post(pub.URL+"/api/"+strconv.FormatInt(s.ErrorsProject, 10)+"/store/?sentry_key="+s.ErrorsKey, "application/json",
			strings.NewReader(`{"message":"`+msg+`","release":"`+release+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("report: %s", res.Status)
		}
	}

	// The first look only takes the cursor: nothing from before alerts.
	report(web, "old", "v1")
	if err := c.AlertErrors(ctx, cl, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := take(); len(got) != 0 {
		t.Fatalf("first look: %q", got)
	}
	report(web, "boom", "v2")
	report(web, "boom", "v2")
	report(preview, "boom", "v2")
	if err := c.AlertErrors(ctx, cl, time.Now()); err != nil {
		t.Fatal(err)
	}
	got := take()
	if len(got) != 1 || !strings.HasPrefix(got[0], "New error in shop/web | boom | https://kipi.example.com/services/"+web.ID+"?tab=errors&issue=") {
		t.Fatalf("new: %q", got)
	}

	// A deploy 15 minutes ago, after v1: 2 errors aren't a spike, 12 are,
	// alerted once.
	now := time.Now()
	finished := func(d time.Duration) *time.Time { t := now.Add(-d); return &t }
	if _, err := c.store.CreateDeployment(ctx, domain.Deployment{ServiceID: web.ID, Status: domain.DeploymentSucceeded, Image: "shop:v1", FinishedAt: finished(48 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	cur, _ := c.store.CreateDeployment(ctx, domain.Deployment{ServiceID: web.ID, Status: domain.DeploymentSucceeded, Image: "shop:v2", FinishedAt: finished(15 * time.Minute)})
	if err := c.AlertErrors(ctx, cl, now); err != nil {
		t.Fatal(err)
	}
	if got := take(); len(got) != 0 {
		t.Fatalf("2 errors: %q", got)
	}
	for range 10 {
		report(web, "boom", "v2")
	}
	de, err := c.DeployErrorsAt(ctx, cl, web, now.Add(time.Minute))
	if err != nil || !de.Spike || de.DeploymentID != cur.ID || de.Release != "v2" || de.PreviousRelease != "v1" || de.Events != 12 || de.NewIssues != 1 {
		t.Fatalf("deploy errors: %+v %v", de, err)
	}
	if err := c.AlertErrors(ctx, cl, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := c.AlertErrors(ctx, cl, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got = take()
	if len(got) != 1 || !strings.HasPrefix(got[0], "Errors up after deploying shop/web | 12 errors in the first") {
		t.Fatalf("spike: %q", got)
	}
	// Too early to tell.
	if de, _ := c.DeployErrorsAt(ctx, cl, web, now.Add(-10*time.Minute)); de.Spike {
		t.Errorf("5 minutes in: %+v", de)
	}

	// A resolved issue coming back.
	l, _ := cl.Issues(ctx, analytics.IssueQuery{Project: web.ErrorsProject, Search: "old"})
	if _, err := cl.SetIssueStatus(ctx, web.ErrorsProject, l.Issues[0].ID, analytics.IssueResolved); err != nil {
		t.Fatal(err)
	}
	report(web, "old", "v2")
	if err := c.AlertErrors(ctx, cl, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := take(); len(got) != 1 || !strings.HasPrefix(got[0], "Error back in shop/web | old") {
		t.Fatalf("regression: %q", got)
	}
}
