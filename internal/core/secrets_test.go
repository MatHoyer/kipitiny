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
