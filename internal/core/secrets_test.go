package core

import (
	"context"
	"errors"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/secrets"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// fakeProvider resolves fake://X to "value-of-X" once connected with "good".
type fakeProvider struct {
	session string
	logins  int
}

func (f *fakeProvider) Info() secrets.Info {
	return secrets.Info{ID: "fake", Name: "Fake", Scheme: "fake"}
}
func (f *fakeProvider) Available() bool { return true }
func (f *fakeProvider) Connect(_ context.Context, token string) error {
	f.logins++
	if token != "good" {
		return errors.New("bad token")
	}
	f.session = token
	return nil
}
func (f *fakeProvider) Disconnect(context.Context) error { f.session = ""; return nil }
func (f *fakeProvider) Resolve(_ context.Context, refs []string) (map[string]string, error) {
	if f.session == "" {
		return nil, errors.New("not logged in")
	}
	out := map[string]string{}
	for _, r := range refs {
		out[r] = "value-of-" + r[len("fake://"):]
	}
	return out, nil
}

func TestSecretProviderConnect(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	fake := &fakeProvider{}
	c.secrets = []secrets.Provider{fake}

	if _, err := c.ConnectSecretProvider(ctx, "nope", "good"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown provider: %v", err)
	}
	if _, err := c.ConnectSecretProvider(ctx, "fake", "bad"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad token: %v", err)
	}
	if c.SecretProviders(ctx)[0].Connected {
		t.Error("connected after a failed login")
	}
	v, err := c.ConnectSecretProvider(ctx, "fake", " good ")
	if err != nil || !v.Connected || !v.Available {
		t.Fatalf("connect = %+v, %v", v, err)
	}
	if err := c.DisconnectSecretProvider(ctx, "fake"); err != nil {
		t.Fatal(err)
	}
	if c.SecretProviders(ctx)[0].Connected || fake.session != "" {
		t.Error("still connected after disconnect")
	}
}

func TestResolveSecrets(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	fake := &fakeProvider{}
	c.secrets = []secrets.Provider{fake}
	refs := []string{"fake://a", "fake://b"}

	if _, err := c.resolveSecrets(ctx, refs); err == nil {
		t.Errorf("not connected: %v", err)
	}
	if _, err := c.resolveSecrets(ctx, []string{"op://x/y/z"}); err == nil {
		t.Errorf("unknown scheme: %v", err)
	}
	if _, err := c.ConnectSecretProvider(ctx, "fake", "good"); err != nil {
		t.Fatal(err)
	}

	fake.session = "" // expired: logs in again with the stored token
	got, err := c.resolveSecrets(ctx, refs)
	if err != nil {
		t.Fatal(err)
	}
	if got["fake://a"] != "value-of-a" || got["fake://b"] != "value-of-b" || fake.logins != 2 {
		t.Errorf("got %v after %d logins", got, fake.logins)
	}

	if err := c.checkSecretSchemes(map[string]string{"A": "{{ fake://x }}"}); err != nil {
		t.Error(err)
	}
	if err := c.checkSecretSchemes(map[string]string{"A": "{{ op://x/y/z }}"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown scheme accepted: %v", err)
	}
}

// fakeBrowser lists one vault, counting the calls.
type fakeBrowser struct {
	fakeProvider
	lists int
}

func (f *fakeBrowser) Vaults(context.Context) ([]string, error) {
	f.lists++
	return []string{"Work"}, nil
}

func (f *fakeBrowser) Items(_ context.Context, vault string) ([]secrets.Item, error) {
	f.lists++
	return []secrets.Item{{Title: vault + "-item"}}, nil
}

func TestSecretListingsCache(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	fake := &fakeBrowser{}
	c.secrets = []secrets.Provider{fake}
	if _, err := c.ConnectSecretProvider(ctx, "fake", "good"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if vs, err := c.SecretVaults(ctx, "fake", false); err != nil || len(vs) != 1 {
			t.Fatalf("vaults = %v, %v", vs, err)
		}
		if items, err := c.SecretItems(ctx, "fake", "Work", false); err != nil || items[0].Title != "Work-item" {
			t.Fatalf("items = %v, %v", items, err)
		}
	}
	if fake.lists != 2 {
		t.Errorf("listed %d times, want 2 (cached)", fake.lists)
	}
	if _, err := c.SecretVaults(ctx, "fake", true); err != nil || fake.lists != 3 {
		t.Errorf("refresh: %v, %d lists", err, fake.lists)
	}
	if _, err := c.SecretItems(ctx, "fake", "", false); !errors.Is(err, ErrInvalid) {
		t.Errorf("no vault: %v", err)
	}
	// Logging in again drops the listings.
	if _, err := c.ConnectSecretProvider(ctx, "fake", "good"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SecretVaults(ctx, "fake", false); err != nil || fake.lists != 4 {
		t.Errorf("after login: %v, %d lists", err, fake.lists)
	}
}
