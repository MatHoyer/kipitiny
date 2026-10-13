package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakePreviews serves the routes previewCmd uses: preview pr-1 of shop
// exists with web's deployment D0; the update deploys web as D1, which
// ends with outcome on its second poll.
func fakePreviews(t *testing.T, outcome string, got *previewOptions) *httptest.Server {
	t.Helper()
	polls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]string{{"id": "P1", "name": "shop"}})
	})
	mux.HandleFunc("GET /api/projects/P1/previews", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id":"P9","name":"shop-pr-1","preview":{"name":"pr-1"}}]`))
	})
	mux.HandleFunc("GET /api/projects/P9/services", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]string{{"id": "S1", "name": "web"}})
	})
	mux.HandleFunc("GET /api/services/S1/deployments", func(w http.ResponseWriter, r *http.Request) {
		d := deployment{ID: "D0", Status: "succeeded", Image: "app:v1"}
		if got.Tags != nil { // after the PUT
			polls++
			d = deployment{ID: "D1", Status: "running", Image: "app:v2"}
			if polls >= 2 {
				d.Status = outcome
				if outcome == "failed" {
					d.Error = "not ready"
				}
			}
		}
		json.NewEncoder(w).Encode([]deployment{d})
	})
	mux.HandleFunc("PUT /api/projects/P1/previews/pr-1", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(got)
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"id":"P9","urls":{"web":"https://pr-1.preview.example.com"},"plan":{"deploying":["web"],"warnings":["game: published ports left out"]}}`))
	})
	mux.HandleFunc("DELETE /api/projects/P1/previews/pr-1", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestPreviewCmd(t *testing.T) {
	var opts previewOptions
	srv := fakePreviews(t, "succeeded", &opts)
	var out strings.Builder
	c := deployClient{base: srv.URL, token: "kpt_x", out: &out}
	id, err := c.projectID(context.Background(), "shop")
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/projects/" + id + "/previews/pr-1"
	if err := c.preview(context.Background(), path, previewOptions{Tags: map[string]string{"web": "v2"}, Branch: "feat/x"}, true); err != nil {
		t.Fatal(err)
	}
	if opts.Tags["web"] != "v2" || opts.Branch != "feat/x" {
		t.Errorf("sent %+v", opts)
	}
	for _, want := range []string{"warning: game: published ports left out", "web deployed app:v2", "web: https://pr-1.preview.example.com"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}

	opts = previewOptions{}
	srv = fakePreviews(t, "failed", &opts)
	c = deployClient{base: srv.URL, token: "kpt_x", out: &out}
	err = c.preview(context.Background(), path, previewOptions{Tags: map[string]string{"web": "v2"}}, true)
	if err == nil || !strings.Contains(err.Error(), "web: not ready") {
		t.Errorf("a failed deploy fails the command: %v", err)
	}
}

func TestPairs(t *testing.T) {
	p := pairs{}
	if p.Set("web=sha-1") != nil || p.Set("A=b=c") != nil || p["web"] != "sha-1" || p["A"] != "b=c" {
		t.Errorf("pairs = %v", p)
	}
	if p.Set("novalue") == nil || p.Set("=x") == nil {
		t.Error("KEY=value is required")
	}
}
