package deploy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/monitoring"
)

func replica(name, deploy string, state string, health string) domain.Container {
	ct := domain.Container{
		ID:     name,
		Names:  []string{"/" + name},
		State:  state,
		Status: "Exited (1) 2 minutes ago",
		Labels: map[string]string{domain.LabelDeploy: deploy},
	}
	if health != "" {
		ct.Health = health
	}
	return ct
}

func TestHealthOf(t *testing.T) {
	svc := domain.Service{CurrentDeploymentID: "d2"}
	for _, tc := range []struct {
		name    string
		svc     domain.Service
		cts     []domain.Container
		judged  bool
		healthy bool
		detail  string
	}{
		{"not deployed", domain.Service{}, nil, false, false, ""},
		{"stopped", domain.Service{CurrentDeploymentID: "d2", Stopped: true}, nil, false, false, ""},
		{"no healthcheck", svc, []domain.Container{replica("web-1", "d2", domain.StateRunning, "")}, true, true, ""},
		{"one healthy is enough", svc, []domain.Container{
			replica("web-1", "d2", domain.StateRunning, domain.Unhealthy),
			replica("web-2", "d2", domain.StateRunning, domain.Healthy),
		}, true, true, ""},
		{"all unhealthy", svc, []domain.Container{
			replica("web-2", "d2", domain.StateRunning, domain.Unhealthy),
			replica("web-1", "d2", domain.StateExited, domain.Unhealthy),
			replica("web-old", "d1", domain.StateRunning, domain.Healthy), // previous deployment
		}, true, false, "web-1: exited (1) 2 minutes ago, web-2: unhealthy"},
		{"starting", svc, []domain.Container{replica("web-1", "d2", domain.StateRunning, domain.HealthStarting)}, true, false, "web-1: starting"},
		{"none left", svc, nil, true, false, "no replica is running"},
	} {
		judged, healthy, detail := healthOf(tc.svc, tc.cts)
		if judged != tc.judged || healthy != tc.healthy || detail != tc.detail {
			t.Errorf("%s: got %v %v %q", tc.name, judged, healthy, detail)
		}
	}
}

func TestHealthGrace(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	h := &svcHealth{}
	at := t0.Add
	// A blip shorter than the grace period is not reported.
	if down, up := h.observe(false, "x", at(0)); down || up {
		t.Fatal("reported at once")
	}
	if down, up := h.observe(true, "", at(time.Minute)); down || up {
		t.Fatal("blip reported")
	}
	h.observe(false, "x", at(2*time.Minute))
	if down, _ := h.observe(false, "x", at(2*time.Minute+healthGrace-time.Second)); down {
		t.Fatal("reported before the grace period")
	}
	if down, _ := h.observe(false, "x", at(2*time.Minute+healthGrace)); !down {
		t.Fatal("not reported after the grace period")
	}
	if down, _ := h.observe(false, "x", at(10*time.Minute)); down {
		t.Fatal("reported twice")
	}
	if _, up := h.observe(true, "", at(11*time.Minute)); !up {
		t.Fatal("recovery not reported")
	}
}

func TestTrackHealthNotifies(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	var sent []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m struct {
			Embeds []struct{ Title, Description string }
		}
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		sent = append(sent, m.Embeds[0].Title+": "+m.Embeds[0].Description)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := newTestCore(t, config.Config{})
	c.NotifyHTTP = &http.Client{Transport: redirect{srv}}
	if _, err := c.CreateChannel(ctx, monitoring.ChannelInput{Name: "ops", Kind: "discord", Config: map[string]string{"webhookUrl": webhook}, Events: []string{domain.EventServiceUnhealthy, domain.EventServiceHealthy}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	p := domain.Project{ID: "p1", Name: "shop"}
	svc := domain.Service{ID: "s1", Name: "api", ServerID: "local", CurrentDeploymentID: "d1"}
	bad := []domain.Container{replica("api-1", "d1", domain.StateRunning, domain.Unhealthy)}
	good := []domain.Container{replica("api-1", "d1", domain.StateRunning, domain.Healthy)}
	t0 := time.Now()

	c.TrackHealth(p, svc, bad, t0)
	c.TrackHealth(p, svc, bad, t0.Add(healthGrace))
	c.ops.Wait() // deliveries are asynchronous
	// Busy (a deploy): not judged, nothing changes.
	unlock, _ := c.LockService(svc.ID)
	c.TrackHealth(p, svc, good, t0.Add(healthGrace+time.Minute))
	unlock()
	c.TrackHealth(p, svc, good, t0.Add(healthGrace+2*time.Minute))
	c.ops.Wait()

	want := []string{
		"shop/api is unhealthy: No healthy replica for 2m0s (api-1: unhealthy).",
		"shop/api is healthy again: Unhealthy for 4m0s.",
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sent) != len(want) {
		t.Fatalf("sent %q", sent)
	}
	for i := range want {
		if sent[i] != want[i] {
			t.Errorf("message %d: %q, want %q", i, sent[i], want[i])
		}
	}

	c.ForgetHealth("local", map[string]bool{})
	if len(c.health.svcs) != 0 {
		t.Error("deleted service still tracked")
	}
}
