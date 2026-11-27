package deploy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/secrets"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"
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
func useSecretProvider(t *testing.T, c *testCore, p secrets.Provider) func(name string) identity.SecretConnectionView {
	t.Helper()
	c.Identity = identity.New(*c.cfg, c.store, []identity.SecretKind{identity.NewSecretKind(func(string) secrets.Provider { return p })}, c.Monitoring, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return func(name string) identity.SecretConnectionView {
		t.Helper()
		v, err := c.CreateSecretConnection(context.Background(), "fake", name, "good")
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}
