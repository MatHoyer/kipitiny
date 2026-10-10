package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/core"
	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
	"github.com/MatHoyer/kipitiny/internal/store/sqlite"
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
	expect(t, "read token can't download", anon.do("GET", "/api/services/X/files/download?path=data/a", "", bearer(read)...), 403)
	expect(t, "read token can't read", anon.do("GET", "/api/services/X/files/content?path=data/a", "", bearer(read)...), 403)
	expect(t, "admin token downloads", anon.do("GET", "/api/services/X/files/download?path=data/a", "", bearer(full)...), 404)
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

// KIPITINY_TEST_DOCKER=1 go test ./internal/api -run VolumeFilesDocker
func TestVolumeFilesDockerHTTP(t *testing.T) {
	if os.Getenv("KIPITINY_TEST_DOCKER") == "" {
		t.Skip("KIPITINY_TEST_DOCKER not set")
	}
	ctx := context.Background()
	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	dk, err := docker.New()
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := core.New(config.Config{DataDir: t.TempDir()}, st, dk, log)
	p, _ := st.CreateProject(ctx, store.Project{Name: "game", ServerID: store.LocalServerID})
	svc, err := st.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "mc", Kind: store.ServiceKindApp, Replicas: 1, Env: map[string]string{},
		Volumes: []store.Volume{{Name: "data", Path: "/data"}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.Shutdown(context.Background()) // removes the file helper
		_ = dk.RemoveVolume(context.Background(), core.AppVolume(svc.ID, "data"))
	})
	tok, err := c.CreateAPIToken(core.WithActor(ctx, core.Actor{Kind: "user", Scope: store.ScopeAdmin}), "t", store.ScopeAdmin)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(c, log))
	t.Cleanup(srv.Close)
	do := func(method, path string, body io.Reader) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, body)
		req.Header.Set("Authorization", "Bearer "+tok.Token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { res.Body.Close() })
		return res
	}
	base := "/api/services/" + svc.ID + "/files"

	jar := bytes.Repeat([]byte{0, 1, 2, 3}, 1<<18) // 1 MiB, binary
	if res := do("PUT", base+"/content?path=data/mods/fabric.jar", bytes.NewReader(jar)); res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("upload: %d %s", res.StatusCode, b)
	}
	res := do("GET", base+"/download?path=data/mods/fabric.jar", nil)
	got, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || !bytes.Equal(got, jar) || !strings.Contains(res.Header.Get("Content-Disposition"), `filename=fabric.jar`) {
		t.Errorf("download: %d, %d bytes, %q", res.StatusCode, len(got), res.Header.Get("Content-Disposition"))
	}
	res = do("PUT", base+"/content?path=data/mods/fabric.jar", strings.NewReader("x"))
	var e struct{ Error string }
	_ = json.NewDecoder(res.Body).Decode(&e)
	if res.StatusCode != 409 || e.Error != "data/mods/fabric.jar already exists" {
		t.Errorf("upload over a file: %d %q", res.StatusCode, e.Error)
	}
	res = do("GET", base+"/download?path=data/mods", nil)
	zr, err := gzip.NewReader(res.Body)
	if err != nil || res.Header.Get("Content-Type") != "application/gzip" {
		t.Fatalf("folder download: %v %q", err, res.Header.Get("Content-Type"))
	}
	hd, err := tar.NewReader(zr).Next()
	if err != nil || hd.Name != "mods/" {
		t.Errorf("folder archive: %v %+v", err, hd)
	}
	if res := do("GET", base+"/download?path=data/nope", nil); res.StatusCode != 404 {
		t.Errorf("missing file: %d", res.StatusCode)
	}
}
