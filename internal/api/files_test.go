package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestVolumeFileRoutes(t *testing.T) {
	srv := newServer(t)
	admin := newClient(t, srv)
	expect(t, "setup", admin.do("POST", "/api/auth/setup", `{"setupToken":"setup-secret","username":"admin","password":"correct-horse"}`), 201)
	read, full := admin.token("read"), admin.token("admin")
	anon := newClient(t, srv)
	bearer := func(tok string) []string { return []string{"Authorization", "Bearer " + tok} }

	// Missing service: the scope check passed.
	expect(t, "read token lists", anon.do("GET", "/api/services/X/files?path=", "", bearer(read)...), 404)
	expect(t, "read token downloads", anon.do("GET", "/api/services/X/files/download?path=data/a", "", bearer(read)...), 404)
	expect(t, "read token can't write", anon.do("PUT", "/api/services/X/files/content?path=data/a", "x", bearer(read)...), 403)
	expect(t, "read token can't delete", anon.do("POST", "/api/services/X/files/delete", `{"paths":["data/a"]}`, bearer(read)...), 403)
	expect(t, "bad modified time", anon.do("PUT", "/api/services/X/files/content?path=data/a&modified=now", "x", bearer(full)...), 400)
	expect(t, "admin writes", anon.do("PUT", "/api/services/X/files/content?path=data/a", "x", bearer(full)...), 404)
	expect(t, "admin moves", anon.do("POST", "/api/services/X/files/move", `{"from":"data/a","to":"data/b"}`, bearer(full)...), 404)
}

func TestConflictReason(t *testing.T) {
	a := &API{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for err, want := range map[error]string{
		fmt.Errorf("%w: data/a changed since it was read", store.ErrConflict): "data/a changed since it was read",
		store.ErrConflict: "already exists (name or domain taken)",
		fmt.Errorf("insert: %w", store.ErrConflict): "already exists (name or domain taken)",
	} {
		rec := httptest.NewRecorder()
		a.fail(rec, err)
		var body struct{ Error string }
		_ = json.NewDecoder(rec.Body).Decode(&body)
		if rec.Code != 409 || body.Error != want {
			t.Errorf("%v: %d %q, want %q", err, rec.Code, body.Error, want)
		}
	}
}
