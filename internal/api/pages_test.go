package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/core"
)

func TestWritePageStarting(t *testing.T) {
	starting := core.Page{Starting: true, Body: []byte("<html>starting</html>")}
	for _, tc := range []struct {
		accept, wantType, wantBody string
	}{
		{"text/html,application/xhtml+xml,*/*;q=0.8", "text/html; charset=utf-8", "starting</html>"},
		{"application/json", "application/json", `"error":"starting"`},
		{"", "application/json", `"error":"starting"`},
	} {
		r := httptest.NewRequest(http.MethodGet, "/api/pages/x", nil)
		r.Header.Set("Accept", tc.accept)
		w := httptest.NewRecorder()
		writePage(w, r, starting)
		if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "5" || w.Header().Get("Refresh") != "3" {
			t.Errorf("accept %q: status %d, Retry-After %q; want 503 and 5", tc.accept, w.Code, w.Header().Get("Retry-After"))
		}
		if got := w.Header().Get("Content-Type"); got != tc.wantType || !strings.Contains(w.Body.String(), tc.wantBody) {
			t.Errorf("accept %q: %s %q", tc.accept, got, w.Body.String())
		}
	}

	w := httptest.NewRecorder()
	writePage(w, httptest.NewRequest(http.MethodGet, "/api/pages/x", nil), core.Page{Body: []byte("down")})
	if w.Header().Get("Retry-After") != "300" || w.Body.String() != "down" {
		t.Errorf("a down app keeps its page for every client, got %q", w.Body.String())
	}
}
