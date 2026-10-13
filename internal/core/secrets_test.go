package core

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/progress"
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

// useSecretProvider makes p the only password manager, one instance for
// every connection, and returns a function connecting it as name.
func useSecretProvider(t *testing.T, c *Core, p secrets.Provider) func(name string) SecretConnectionView {
	t.Helper()
	c.secretKinds = []secretKind{newSecretKind(func(string) secrets.Provider { return p })}
	return func(name string) SecretConnectionView {
		t.Helper()
		v, err := c.CreateSecretConnection(context.Background(), "fake", name, "good")
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

func TestSecretConnections(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	fakes := map[string]*fakeProvider{}
	c.secretKinds = []secretKind{newSecretKind(func(name string) secrets.Provider {
		fakes[name] = &fakeProvider{}
		return fakes[name]
	})}

	if _, err := c.CreateSecretConnection(ctx, "nope", "", "good"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown provider: %v", err)
	}
	if _, err := c.CreateSecretConnection(ctx, "fake", "", "bad"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad token: %v", err)
	}
	if cs, _ := c.SecretConnections(ctx); len(cs) != 0 {
		t.Errorf("connections after a failed login: %+v", cs)
	}
	def, err := c.CreateSecretConnection(ctx, "fake", "", " good ")
	if err != nil || def.Scheme != "fake" || def.Token != "good" {
		t.Fatalf("default = %+v, %v", def, err)
	}
	if _, err := c.CreateSecretConnection(ctx, "fake", "", "good"); !errors.Is(err, ErrInvalid) {
		t.Errorf("second default: %v", err)
	}
	if _, err := c.CreateSecretConnection(ctx, "fake", "Work!", "good"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad name: %v", err)
	}
	work, err := c.CreateSecretConnection(ctx, "fake", "work", "good")
	if err != nil || work.Scheme != "fake+work" {
		t.Fatalf("work = %+v, %v", work, err)
	}
	if fakes[""] == fakes["work"] || fakes["work"].session != "good" {
		t.Error("connections share a provider")
	}

	// Each reference resolves through its own connection.
	fakes[""].session = "" // expired: logs in again with the stored token
	got, err := c.resolveSecrets(ctx, []string{"fake://a", "fake+work://b"})
	if err != nil {
		t.Fatal(err)
	}
	if got["fake://a"] != "value-of-a" || got["fake+work://b"] != "value-of-b" || fakes[""].logins != 3 {
		t.Errorf("got %v after %d logins", got, fakes[""].logins)
	}
	if _, err := c.resolveSecrets(ctx, []string{"fake+home://a"}); err == nil {
		t.Error("unknown connection resolved")
	}

	if err := c.DeleteSecretConnection(ctx, work.ID); err != nil {
		t.Fatal(err)
	}
	if cs, _ := c.SecretConnections(ctx); len(cs) != 1 || cs[0].ID != def.ID {
		t.Errorf("after delete: %+v", cs)
	}
	if fakes["work"].session != "" {
		t.Error("still logged in after delete")
	}
	if _, err := c.resolveSecrets(ctx, []string{"fake+work://b"}); err == nil {
		t.Error("deleted connection resolved")
	}
}

func TestResolveSecrets(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	connect := useSecretProvider(t, c, &fakeProvider{})

	if _, err := c.resolveSecrets(ctx, []string{"fake://a"}); err == nil {
		t.Errorf("not connected: %v", err)
	}
	if _, err := c.resolveSecrets(ctx, []string{"op://x/y/z"}); err == nil {
		t.Errorf("unknown scheme: %v", err)
	}
	connect("")

	if err := c.checkSecretSchemes(ctx, map[string]string{"A": "{{ fake://x }}"}); err != nil {
		t.Error(err)
	}
	for _, ref := range []string{"op://x/y/z", "fake+work://x"} {
		if err := c.checkSecretSchemes(ctx, map[string]string{"A": "{{ " + ref + " }}"}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s accepted: %v", ref, err)
		}
	}
	connect("work")
	if err := c.checkSecretSchemes(ctx, map[string]string{"A": "{{ fake+work://x }}"}); err != nil {
		t.Error(err)
	}
}

// fakeBrowser lists one vault once logged in, counting the calls.
type fakeBrowser struct {
	fakeProvider
	lists int
}

func (f *fakeBrowser) Vaults(context.Context) ([]string, error) {
	f.lists++
	if f.session == "" {
		return nil, errors.New("not logged in")
	}
	return []string{"Work"}, nil
}

func (f *fakeBrowser) Items(_ context.Context, vault string) ([]secrets.Item, error) {
	f.lists++
	if f.session == "" {
		return nil, errors.New("not logged in")
	}
	return []secrets.Item{{Title: vault + "-item"}}, nil
}

func TestSecretListingsCache(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	fake := &fakeBrowser{}
	id := useSecretProvider(t, c, fake)("").ID
	for range 2 {
		if vs, err := c.SecretVaults(ctx, id, false); err != nil || len(vs) != 1 {
			t.Fatalf("vaults = %v, %v", vs, err)
		}
		if items, err := c.SecretItems(ctx, id, "Work", false); err != nil || items[0].Title != "Work-item" {
			t.Fatalf("items = %v, %v", items, err)
		}
	}
	if fake.lists != 2 {
		t.Errorf("listed %d times, want 2 (cached)", fake.lists)
	}
	if _, err := c.SecretVaults(ctx, id, true); err != nil || fake.lists != 3 {
		t.Errorf("refresh: %v, %d lists", err, fake.lists)
	}
	if _, err := c.SecretItems(ctx, id, "", false); !errors.Is(err, ErrInvalid) {
		t.Errorf("no vault: %v", err)
	}
	// Logging in again drops the listings.
	if _, err := c.UpdateSecretConnection(ctx, id, "good"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SecretVaults(ctx, id, false); err != nil || fake.lists != 4 {
		t.Errorf("after login: %v, %d lists", err, fake.lists)
	}
	// An expired session logs in again with the stored token.
	fake.session = ""
	logins := fake.logins
	if items, err := c.SecretItems(ctx, id, "Work", true); err != nil || len(items) != 1 || fake.logins != logins+1 {
		t.Errorf("expired session: %v, %v, %d logins", items, err, fake.logins-logins)
	}
}

// browsingProvider is a fakeProvider that lists vaults while logged in and
// reports starting its helper when warmed.
type browsingProvider struct{ fakeProvider }

func (b *browsingProvider) Warm(ctx context.Context) error {
	progress.Report(ctx, secrets.StepHelper)
	return nil
}
func (b *browsingProvider) Vaults(context.Context) ([]string, error) {
	if b.session == "" {
		return nil, errors.New("not logged in")
	}
	return []string{"Vault"}, nil
}
func (b *browsingProvider) Items(context.Context, string) ([]secrets.Item, error) { return nil, nil }

func TestTestSecretProviderProgress(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	b := &browsingProvider{}
	id := useSecretProvider(t, c, b)("").ID
	test := func() ([]string, error) {
		var steps []string
		err := c.TestSecretConnection(WithProgress(ctx, func(s string) { steps = append(steps, s) }), id)
		return steps, err
	}

	steps, err := test()
	if want := []string{"helper", "session"}; err != nil || !slices.Equal(steps, want) {
		t.Errorf("live session: steps = %q, %v; want %q", steps, err, want)
	}
	// An expired session signs in again with the saved token.
	b.session = ""
	steps, err = test()
	if want := []string{"helper", "session", "vaults"}; err != nil || !slices.Equal(steps, want) {
		t.Errorf("expired session: steps = %q, %v; want %q", steps, err, want)
	}
	if b.session != "good" {
		t.Error("didn't sign in again")
	}
}
