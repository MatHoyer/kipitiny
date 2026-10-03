package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestImageRegistry(t *testing.T) {
	for image, want := range map[string]string{
		"postgres:17-alpine":            "docker.io",
		"org/app":                       "docker.io",
		"ghcr.io/org/app:v1":            "ghcr.io",
		"registry.gitlab.com/g/p/app:1": "registry.gitlab.com",
		"localhost:5000/app":            "localhost:5000",
		"Not An Image":                  "",
	} {
		if got := imageRegistry(image); got != want {
			t.Errorf("imageRegistry(%q) = %q, want %q", image, got, want)
		}
	}
	for in, want := range map[string]string{
		"https://index.docker.io/v1/": "docker.io",
		"hub.docker.com":              "docker.io",
		" GHCR.io/ ":                  "ghcr.io",
		"https://registry.gitlab.com": "registry.gitlab.com",
	} {
		if got := normalizeRegistryHost(in); got != want {
			t.Errorf("normalizeRegistryHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRegistryAuth(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	if got := c.registryAuth(ctx, "ghcr.io/org/app"); got != "" {
		t.Fatal("credentials without any registry")
	}
	for _, r := range []store.Registry{
		{Host: "ghcr.io", Username: "me", Password: "ghp_x"},
		{Host: "docker.io", Username: "hub", Password: "dckr_y"},
	} {
		if _, err := c.store.CreateRegistry(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	var auth struct{ Username, Password, ServerAddress string }
	raw, _ := base64.URLEncoding.DecodeString(c.registryAuth(ctx, "ghcr.io/org/app:v1"))
	if json.Unmarshal(raw, &auth); auth.Username != "me" || auth.Password != "ghp_x" || auth.ServerAddress != "ghcr.io" {
		t.Fatalf("ghcr auth = %+v", auth)
	}
	raw, _ = base64.URLEncoding.DecodeString(c.registryAuth(ctx, "postgres:17"))
	if json.Unmarshal(raw, &auth); auth.Username != "hub" || auth.ServerAddress != "https://index.docker.io/v1/" {
		t.Fatalf("hub auth = %+v", auth)
	}
	if c.registryAuth(ctx, "quay.io/org/app") != "" {
		t.Fatal("credential sent to another registry")
	}
}
