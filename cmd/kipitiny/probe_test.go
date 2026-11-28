package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProbeCheck(t *testing.T) {
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
		if err := probeCheck(target); (err == nil) != want {
			t.Errorf("probeCheck(%q) = %v, want ok=%v", target, err, want)
		}
	}

	// A closed port fails.
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := l.Addr().String()
	l.Close()
	if probeCheck("tcp://"+closed) == nil {
		t.Error("closed port reported healthy")
	}
}
