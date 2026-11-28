package deploy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
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
	for _, tc := range []struct{ in, want string }{
		{"https://index.docker.io/v1/", "docker.io"},
		{"hub.docker.com", "docker.io"},
		{" GHCR.io/ ", "ghcr.io"},
		{"https://registry.gitlab.com", "registry.gitlab.com"},
	} {
		if got := normalizeRegistryHost(tc.in); got != tc.want {
			t.Errorf("normalizeRegistryHost(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRegistryAuth(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	if got := c.RegistryAuth(ctx, "ghcr.io/org/app"); got != "" {
		t.Fatal("credentials without any registry")
	}
	for _, r := range []domain.Registry{
		{Host: "ghcr.io", Username: "me", Password: "ghp_x"},
		{Host: "docker.io", Username: "hub", Password: "dckr_y"},
	} {
		if _, err := c.store.CreateRegistry(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	var auth struct{ Username, Password, ServerAddress string }
	raw, _ := base64.URLEncoding.DecodeString(c.RegistryAuth(ctx, "ghcr.io/org/app:v1"))
	if err := json.Unmarshal(raw, &auth); err != nil || auth.Username != "me" || auth.Password != "ghp_x" || auth.ServerAddress != "ghcr.io" {
		t.Fatalf("ghcr auth = %+v, %v", auth, err)
	}
	raw, _ = base64.URLEncoding.DecodeString(c.RegistryAuth(ctx, "postgres:17"))
	if err := json.Unmarshal(raw, &auth); err != nil || auth.Username != "hub" || auth.ServerAddress != "https://index.docker.io/v1/" {
		t.Fatalf("hub auth = %+v, %v", auth, err)
	}
	if c.RegistryAuth(ctx, "quay.io/org/app") != "" {
		t.Fatal("credential sent to another registry")
	}
}
