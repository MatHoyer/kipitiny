package probe

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(204)
		case "/login":
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
		default:
			w.WriteHeader(503)
		}
	}))
	defer ok.Close()
	addr := strings.TrimPrefix(ok.URL, "http://")

	for target, want := range map[string]bool{
		ok.URL + "/health": true,
		ok.URL + "/login":  true,
		ok.URL + "/down":   false,
		"tcp://" + addr:    true,
		"ftp://" + addr:    false,
	} {
		if err := Check(target); (err == nil) != want {
			t.Errorf("Check(%q) = %v, want ok=%v", target, err, want)
		}
	}

	// A closed port fails.
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := l.Addr().String()
	l.Close()
	if Check("tcp://"+closed) == nil {
		t.Error("closed port reported healthy")
	}
}

func TestHTTPExpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
			return
		}
		w.WriteHeader(204)
	}))
	defer srv.Close()
	ctx := context.Background()

	if r, err := HTTP(ctx, NewClient(), srv.URL, 204); err != nil || r.Status != 204 || r.Latency <= 0 {
		t.Errorf("expected 204: %+v %v", r, err)
	}
	if r, err := HTTP(ctx, NewClient(), srv.URL, 200); err == nil || r.Status != 204 {
		t.Errorf("expected 200: %+v %v", r, err)
	}
	if r, err := HTTP(ctx, NewClient(), srv.URL+"/login", 302); err != nil || r.Status != 302 {
		t.Errorf("redirect not followed: %+v %v", r, err)
	}
	if _, err := HTTP(ctx, NewClient(), "http://127.0.0.1:1", 0); err == nil {
		t.Error("closed port reported up")
	}
}
