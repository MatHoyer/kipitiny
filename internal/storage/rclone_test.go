package storage

import (
	"context"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRcloneConfigRoundTrip(t *testing.T) {
	opts := map[string]string{"token": `{"access_token":"a","expiry":"x"}`, "scope": "drive"}
	data := renderConfig("drive", opts)
	if !strings.HasPrefix(string(data), "[kipitiny]\ntype = drive\n") {
		t.Fatalf("config = %q", data)
	}
	got := parseConfig(append([]byte("[other]\nk = v\n"), data...))
	delete(got, "type")
	if !maps.Equal(got, opts) {
		t.Fatalf("parsed %v, want %v", got, opts)
	}
	// A newline in a value can't inject another option.
	got = parseConfig(renderConfig("drive", map[string]string{"user": "a\ntype = local"}))
	if got["type"] != "drive" || got["user"] != "atype = local" {
		t.Fatalf("injected: %v", got)
	}
}

func TestRcloneErrorMessage(t *testing.T) {
	err := rcloneError("rcat", errors.New("exit status 1"),
		"2026/10/01 12:00:00 ERROR : Attempt 1/3 failed\n2026/10/01 12:00:01 CRITICAL : Failed to rcat: 403 Forbidden\n")
	if err.Error() != "rclone rcat: Failed to rcat: 403 Forbidden" {
		t.Fatalf("err = %v", err)
	}
}

// Options rclone rewrites during a command (a refreshed token) are saved.
func TestRcloneSavesChangedConfig(t *testing.T) {
	var saved map[string]string
	r := &Rclone{
		work:    t.TempDir(),
		id:      "t1",
		backend: "drive",
		config:  map[string]string{"token": "old"},
		save: func(_ context.Context, id string, cfg map[string]string) error {
			saved = cfg
			return nil
		},
	}
	s, err := r.open()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.conf, renderConfig("drive", map[string]string{"token": "new"}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if saved["token"] != "new" || r.Config()["token"] != "new" {
		t.Fatalf("saved %v, config %v", saved, r.Config())
	}
	if entries, _ := os.ReadDir(r.work); len(entries) != 0 {
		t.Fatalf("leftover session dirs: %v", entries)
	}
}

// TestRclone runs the driver against rclone's local backend, e.g.
//
//	KIPITINY_TEST_RCLONE=/path/to/rclone go test ./internal/storage/
func TestRclone(t *testing.T) {
	bin := os.Getenv("KIPITINY_TEST_RCLONE")
	if bin == "" {
		t.Skip("KIPITINY_TEST_RCLONE not set")
	}
	ctx := context.Background()
	dir := t.TempDir()
	var saved map[string]string
	r := &Rclone{
		bin:     bin,
		work:    filepath.Join(dir, "work"),
		id:      "t1",
		backend: "local",
		prefix:  filepath.Join(dir, "remote"),
		config:  map[string]string{"links": "false"},
		save: func(_ context.Context, id string, cfg map[string]string) error {
			saved = cfg
			return nil
		},
	}
	if err := r.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.Put(ctx, "pg/db.dump", strings.NewReader("hello")); err != nil {
		t.Fatal(err)
	}
	rc, err := r.Get(ctx, "pg/db.dump")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(rc)
	if err := rc.Close(); err != nil || string(body) != "hello" {
		t.Fatalf("get = %q, %v", body, err)
	}
	if _, err := r.Get(ctx, "pg/missing.dump"); err == nil {
		t.Fatal("missing object: no error")
	}
	if err := r.Delete(ctx, "pg/db.dump"); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(ctx, "pg/db.dump"); err != nil {
		t.Fatalf("deleting twice: %v", err)
	}
	if _, err := r.path(""); err == nil {
		t.Fatal("empty key accepted")
	}
	if saved != nil {
		t.Fatalf("unchanged config saved: %v", saved)
	}
	if entries, _ := os.ReadDir(r.work); len(entries) != 0 {
		t.Fatalf("leftover session dirs: %v", entries)
	}

}
