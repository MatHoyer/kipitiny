package core

import (
	"archive/tar"
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestWebhookAuthorized(t *testing.T) {
	secret, body := "s3cret", []byte(`{"ref":"refs/heads/main"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	good := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	for name, tc := range map[string]struct {
		header http.Header
		want   bool
	}{
		"github signature":  {http.Header{"X-Hub-Signature-256": {good}}, true},
		"github wrong body": {http.Header{"X-Hub-Signature-256": {"sha256=00"}}, false},
		"gitlab token":      {http.Header{"X-Gitlab-Token": {secret}}, true},
		"bearer token":      {http.Header{"Authorization": {"Bearer " + secret}}, true},
		"wrong bearer":      {http.Header{"Authorization": {"Bearer nope"}}, false},
		"nothing":           {http.Header{}, false},
		"signature wins":    {http.Header{"X-Hub-Signature-256": {"sha256=00"}, "X-Gitlab-Token": {secret}}, false},
	} {
		if got := webhookAuthorized(secret, tc.header.Get, body); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

func TestValidateGit(t *testing.T) {
	ok := store.Service{Kind: store.ServiceKindApp, Source: store.SourceGit, GitURL: "https://github.com/org/app.git",
		GitBranch: "main", Dockerfile: "docker/Dockerfile", BuildContext: "services/api", Replicas: 1}
	if err := validateService(ok); err != nil {
		t.Fatalf("valid git service rejected: %v", err)
	}
	for name, mutate := range map[string]func(*store.Service){
		"ssh url":          func(s *store.Service) { s.GitURL = "git@github.com:org/app.git" },
		"file url":         func(s *store.Service) { s.GitURL = "file:///etc" },
		"bad branch":       func(s *store.Service) { s.GitBranch = "a b" },
		"escaping context": func(s *store.Service) { s.BuildContext = "../../etc" },
		"absolute file":    func(s *store.Service) { s.Dockerfile = "/Dockerfile" },
		"database":         func(s *store.Service) { s.Kind = store.ServiceKindPostgres },
	} {
		s := ok
		mutate(&s)
		if err := validateService(s); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
}

func TestTarDir(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".git", "objects"), 0o755)
	os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref"), 0o644)
	os.MkdirAll(filepath.Join(dir, "src"), 0o755)
	os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch"), 0o644)
	os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main"), 0o644)
	os.Symlink("main.go", filepath.Join(dir, "src", "link.go"))

	var buf bytes.Buffer
	if err := tarDir(dir, &buf); err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(&buf)
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	slices.Sort(names)
	want := []string{"Dockerfile", "src/", "src/link.go", "src/main.go"}
	if !slices.Equal(names, want) {
		t.Errorf("entries = %v, want %v (.git excluded)", names, want)
	}
}

func TestRedactURL(t *testing.T) {
	if got := redactURL("https://user:tok@github.com/org/app.git"); got != "https://%2A%2A%2A@github.com/org/app.git" && got != "https://***@github.com/org/app.git" {
		t.Errorf("credentials not redacted: %s", got)
	}
	if got := redactURL("https://github.com/org/app.git"); got != "https://github.com/org/app.git" {
		t.Errorf("clean URL changed: %s", got)
	}
}
