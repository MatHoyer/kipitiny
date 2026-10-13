package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestSecureHeaders(t *testing.T) {
	proxy := netip.MustParseAddr("10.0.0.2")
	trusted := func(_ context.Context, ip netip.Addr) bool { return ip == proxy }
	h := SecureHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), trusted)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	for _, k := range []string{"Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy"} {
		if w.Header().Get(k) == "" {
			t.Errorf("%s missing", k)
		}
	}
	if w.Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS must not be sent over plain HTTP")
	}

	w = httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.2:4321"
	r.Header.Set("X-Forwarded-Proto", "https")
	h.ServeHTTP(w, r)
	if w.Header().Get("Strict-Transport-Security") == "" {
		t.Error("HSTS missing behind the HTTPS proxy")
	}

	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	h.ServeHTTP(w, r)
	if w.Header().Get("Strict-Transport-Security") != "" || r.Header.Get("X-Forwarded-Proto") != "" {
		t.Error("X-Forwarded-Proto trusted from a client that isn't a proxy")
	}
}
