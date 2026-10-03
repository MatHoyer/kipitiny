package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeManager serves the routes deployCmd uses; the deployment succeeds or
// fails on its second poll.
func fakeManager(t *testing.T, outcome string, gotOpts *deployOptions) *httptest.Server {
	t.Helper()
	polls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]string{{"id": "P1", "name": "other"}, {"id": "P2", "name": "shop"}})
	})
	mux.HandleFunc("GET /api/projects/P2/services", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]string{{"id": "S1", "name": "db"}, {"id": "S2", "name": "web"}})
	})
	mux.HandleFunc("POST /api/services/S2/deploy", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer kpt_x" {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "bad token"})
			return
		}
		json.NewDecoder(r.Body).Decode(gotOpts)
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(deployment{ID: "D1", Status: "running"})
	})
	mux.HandleFunc("GET /api/deployments/D1", func(w http.ResponseWriter, r *http.Request) {
		polls++
		d := deployment{ID: "D1", Status: "running", Image: "app:v2"}
		if polls >= 2 {
			d.Status = outcome
			if outcome == "failed" {
				d.Error = "pull app:v2: not found"
			}
		}
		json.NewEncoder(w).Encode(d)
	})
	mux.HandleFunc("GET /api/deployments/D1/log", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[12:00:00] Pulling app:v2\n"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestDeployCmd(t *testing.T) {
	var opts deployOptions
	srv := fakeManager(t, "succeeded", &opts)
	var out strings.Builder
	c := deployClient{base: srv.URL, token: "kpt_x", out: &out}
	if err := c.deploy(context.Background(), "shop/web", deployOptions{Tag: "v2", Commit: "abc1234"}, true); err != nil {
		t.Fatal(err)
	}
	if opts.Tag != "v2" || opts.Commit != "abc1234" {
		t.Errorf("sent %+v", opts)
	}
	if got := out.String(); strings.Count(got, "Pulling") != 1 || !strings.Contains(got, "Deployed app:v2") {
		t.Errorf("output:\n%s", got)
	}

	srv = fakeManager(t, "failed", &opts)
	c = deployClient{base: srv.URL, token: "kpt_x", out: &out}
	if err := c.deploy(context.Background(), "S2", deployOptions{}, true); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("failed deployment: %v", err)
	}
	c.token = "nope"
	if err := c.deploy(context.Background(), "S2", deployOptions{}, true); err == nil || !strings.Contains(err.Error(), "bad token") {
		t.Errorf("refused deploy: %v", err)
	}
	if err := c.deploy(context.Background(), "shop/missing", deployOptions{}, true); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("unknown service: %v", err)
	}
}
