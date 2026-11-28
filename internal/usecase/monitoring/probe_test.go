package monitoring

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbeHTTPExpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
			return
		}
		w.WriteHeader(204)
	}))
	defer srv.Close()
	ctx := context.Background()

	if r, err := ProbeHTTP(ctx, NewProbeClient(), srv.URL, 204); err != nil || r.Status != 204 || r.Latency <= 0 {
		t.Errorf("expected 204: %+v %v", r, err)
	}
	if r, err := ProbeHTTP(ctx, NewProbeClient(), srv.URL, 200); err == nil || r.Status != 204 {
		t.Errorf("expected 200: %+v %v", r, err)
	}
	if r, err := ProbeHTTP(ctx, NewProbeClient(), srv.URL+"/login", 302); err != nil || r.Status != 302 {
		t.Errorf("redirect not followed: %+v %v", r, err)
	}
	if _, err := ProbeHTTP(ctx, NewProbeClient(), "http://127.0.0.1:1", 0); err == nil {
		t.Error("closed port reported up")
	}
}
