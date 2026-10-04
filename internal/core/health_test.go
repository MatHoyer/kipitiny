package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func replica(name, deploy string, state container.ContainerState, health container.HealthStatus) container.Summary {
	ct := container.Summary{
		ID:     name,
		Names:  []string{"/" + name},
		State:  state,
		Status: "Exited (1) 2 minutes ago",
		Labels: map[string]string{docker.LabelDeploy: deploy},
	}
	if health != "" {
		ct.Health = &container.HealthSummary{Status: health}
	}
	return ct
}

func TestHealthOf(t *testing.T) {
	svc := store.Service{CurrentDeploymentID: "d2"}
	for _, tc := range []struct {
		name    string
		svc     store.Service
		cts     []container.Summary
		judged  bool
		healthy bool
		detail  string
	}{
		{"not deployed", store.Service{}, nil, false, false, ""},
		{"stopped", store.Service{CurrentDeploymentID: "d2", Stopped: true}, nil, false, false, ""},
		{"no healthcheck", svc, []container.Summary{replica("web-1", "d2", container.StateRunning, "")}, true, true, ""},
		{"one healthy is enough", svc, []container.Summary{
			replica("web-1", "d2", container.StateRunning, container.Unhealthy),
			replica("web-2", "d2", container.StateRunning, container.Healthy),
		}, true, true, ""},
		{"all unhealthy", svc, []container.Summary{
			replica("web-2", "d2", container.StateRunning, container.Unhealthy),
			replica("web-1", "d2", container.StateExited, container.Unhealthy),
			replica("web-old", "d1", container.StateRunning, container.Healthy), // previous deployment
		}, true, false, "web-1: exited (1) 2 minutes ago, web-2: unhealthy"},
		{"starting", svc, []container.Summary{replica("web-1", "d2", container.StateRunning, container.Starting)}, true, false, "web-1: starting"},
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
	at := func(d time.Duration) time.Time { return t0.Add(d) }
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
		json.NewDecoder(r.Body).Decode(&m)
		mu.Lock()
		sent = append(sent, m.Embeds[0].Title+": "+m.Embeds[0].Description)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := newTestCore(t, config.Config{})
	c.notifyHTTP = &http.Client{Transport: redirect{srv}}
	if _, err := c.CreateChannel(ctx, ChannelInput{Name: "ops", Kind: "discord", Config: map[string]string{"webhookUrl": webhook}, Events: []string{EventServiceUnhealthy, EventServiceHealthy}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	p := store.Project{ID: "p1", Name: "shop"}
	svc := store.Service{ID: "s1", Name: "api", ServerID: "local", CurrentDeploymentID: "d1"}
	bad := []container.Summary{replica("api-1", "d1", container.StateRunning, container.Unhealthy)}
	good := []container.Summary{replica("api-1", "d1", container.StateRunning, container.Healthy)}
	t0 := time.Now()

	c.trackHealth(p, svc, bad, t0)
	c.trackHealth(p, svc, bad, t0.Add(healthGrace))
	c.ops.Wait() // deliveries are asynchronous
	// Busy (a deploy): not judged, nothing changes.
	unlock, _ := c.lockService(svc.ID)
	c.trackHealth(p, svc, good, t0.Add(healthGrace+time.Minute))
	unlock()
	c.trackHealth(p, svc, good, t0.Add(healthGrace+2*time.Minute))
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

	c.forgetHealth("local", map[string]bool{})
	if len(c.health.svcs) != 0 {
		t.Error("deleted service still tracked")
	}
}
