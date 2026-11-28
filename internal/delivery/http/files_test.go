package http

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

	"github.com/kipit-labs/kipitiny/internal/usecase/data"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/docker"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/sqlite"
)

func TestVolumeFileRoutes(t *testing.T) {
	srv := newServer(t)
	admin := newClient(t, srv)
	expect(t, "setup", admin.do("POST", "/api/auth/setup", `{"setupToken":"setup-secret","username":"admin","password":"correct-horse"}`), 201)
	full := admin.token("admin")
	anon := newClient(t, srv)
	bearer := func(tok string) []string { return []string{"Authorization", "Bearer " + tok} }

	expect(t, "admin token downloads", anon.do("GET", "/api/services/X/files/download?path=data/a", "", bearer(full)...), 404)
	expect(t, "bad modified time", anon.do("PUT", "/api/services/X/files/content?path=data/a&modified=now", "x", bearer(full)...), 400)
	expect(t, "admin writes", anon.do("PUT", "/api/services/X/files/content?path=data/a", "x", bearer(full)...), 404)
	expect(t, "admin moves", anon.do("POST", "/api/services/X/files/move", `{"from":"data/a","to":"data/b"}`, bearer(full)...), 404)
}

func TestConflictReason(t *testing.T) {
	a := &API{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for err, want := range map[error]string{
		fmt.Errorf("%w: data/a changed since it was read", domain.ErrConflict): "data/a changed since it was read",
		domain.ErrConflict:                           "already exists (name or domain taken)",
		fmt.Errorf("insert: %w", domain.ErrConflict): "already exists (name or domain taken)",
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

// KIPITINY_TEST_DOCKER=1 go test ./internal/delivery/http -run VolumeFilesDocker
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
	c := newTestApp(config.Config{DataDir: t.TempDir()}, st, dk, log)
	p, _ := st.CreateProject(ctx, domain.Project{Name: "game", ServerID: domain.LocalServerID})
	svc, err := st.CreateService(ctx, domain.Service{ProjectID: p.ID, Name: "mc", Kind: domain.ServiceKindApp, Replicas: 1, Env: map[string]string{},
		Volumes: []domain.Volume{{Name: "data", Path: "/data"}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.Shutdown(context.Background()) // removes the file helper
		_ = dk.RemoveVolume(context.Background(), data.AppVolume(svc.ID, "data"))
	})
	tok, err := c.CreateAPIToken(identity.WithActor(ctx, identity.Actor{Kind: "user"}), identity.TokenInput{Name: "t"})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(c, log))
	t.Cleanup(srv.Close)
	do := func(method, path string, body io.Reader) (int, http.Header, []byte) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, body)
		req.Header.Set("Authorization", "Bearer "+tok.Token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, res.Header, b
	}
	base := "/api/services/" + svc.ID + "/files"

	jar := bytes.Repeat([]byte{0, 1, 2, 3}, 1<<18) // 1 MiB, binary
	if status, _, b := do("PUT", base+"/content?path=data/mods/fabric.jar", bytes.NewReader(jar)); status != 200 {
		t.Fatalf("upload: %d %s", status, b)
	}
	status, header, got := do("GET", base+"/download?path=data/mods/fabric.jar", nil)
	if status != 200 || !bytes.Equal(got, jar) || !strings.Contains(header.Get("Content-Disposition"), `filename=fabric.jar`) {
		t.Errorf("download: %d, %d bytes, %q", status, len(got), header.Get("Content-Disposition"))
	}
	status, _, b := do("PUT", base+"/content?path=data/mods/fabric.jar", strings.NewReader("x"))
	var e struct{ Error string }
	_ = json.Unmarshal(b, &e)
	if status != 409 || e.Error != "data/mods/fabric.jar already exists" {
		t.Errorf("upload over a file: %d %q", status, e.Error)
	}
	_, header, b = do("GET", base+"/download?path=data/mods", nil)
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil || header.Get("Content-Type") != "application/gzip" {
		t.Fatalf("folder download: %v %q", err, header.Get("Content-Type"))
	}
	hd, err := tar.NewReader(zr).Next()
	if err != nil || hd.Name != "mods/" {
		t.Errorf("folder archive: %v %+v", err, hd)
	}
	if status, _, _ := do("GET", base+"/download?path=data/nope", nil); status != 404 {
		t.Errorf("missing file: %d", status)
	}
}
