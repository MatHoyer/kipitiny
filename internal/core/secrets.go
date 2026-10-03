package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/secrets"
	"github.com/MatHoyer/kipitiny/internal/secrets/protonpass"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// SecretProviderView is a password manager's state in Settings.
type SecretProviderView struct {
	secrets.Info
	// Available is false when it can't run here (its CLI is missing).
	Available bool `json:"available"`
	Connected bool `json:"connected"`
}

func secretProviders(cfg config.Config) []secrets.Provider {
	self, err := os.Executable()
	if err != nil {
		self = os.Args[0]
	}
	dir, err := filepath.Abs(filepath.Join(cfg.DataDir, "secrets"))
	if err != nil {
		dir = filepath.Join(cfg.DataDir, "secrets")
	}
	return []secrets.Provider{
		protonpass.New(cfg.ProtonPassCLI, filepath.Join(dir, "protonpass"), []string{self, "secrets-env"}),
	}
}

// secretTokenSetting keeps a provider's token, to log in again when its
// session is lost or expires.
func secretTokenSetting(id string) string { return "secrets_" + id + "_token" }

func (c *Core) secretProvider(id string) (secrets.Provider, error) {
	for _, p := range c.secrets {
		if p.Info().ID == id {
			return p, nil
		}
	}
	return nil, store.ErrNotFound
}

func (c *Core) secretProviderView(ctx context.Context, p secrets.Provider) SecretProviderView {
	token, _ := c.store.GetSetting(ctx, secretTokenSetting(p.Info().ID))
	return SecretProviderView{Info: p.Info(), Available: p.Available(), Connected: token != ""}
}

func (c *Core) SecretProviders(ctx context.Context) []SecretProviderView {
	out := make([]SecretProviderView, len(c.secrets))
	for i, p := range c.secrets {
		out[i] = c.secretProviderView(ctx, p)
	}
	return out
}

// ConnectSecretProvider logs the provider in with token and keeps the token.
func (c *Core) ConnectSecretProvider(ctx context.Context, id, token string) (SecretProviderView, error) {
	p, err := c.secretProvider(id)
	if err != nil {
		return SecretProviderView{}, err
	}
	if !p.Available() {
		return SecretProviderView{}, fmt.Errorf("%w: %s isn't installed on the manager", ErrInvalid, p.Info().Name)
	}
	token = strings.TrimSpace(token)
	if err := p.Connect(ctx, token); err != nil {
		return SecretProviderView{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := c.store.SetSetting(ctx, secretTokenSetting(id), token); err != nil {
		return SecretProviderView{}, err
	}
	return c.secretProviderView(ctx, p), nil
}

// TestSecretProvider checks the session still works, logging in again with
// the saved token if it doesn't, as a deploy would.
func (c *Core) TestSecretProvider(ctx context.Context, id string) error {
	p, err := c.secretProvider(id)
	if err != nil {
		return err
	}
	name := p.Info().Name
	token, _ := c.store.GetSetting(ctx, secretTokenSetting(id))
	if token == "" {
		return fmt.Errorf("%w: %s isn't connected", ErrInvalid, name)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	b, ok := p.(secrets.Browser)
	if ok {
		if _, err := b.Vaults(ctx); err == nil {
			return nil
		}
	}
	if err := p.Connect(ctx, token); err != nil {
		return fmt.Errorf("%w: %s refused the saved token: %v", ErrInvalid, name, err)
	}
	if ok {
		if _, err := b.Vaults(ctx); err != nil {
			return fmt.Errorf("%w: %s: %v", ErrInvalid, name, err)
		}
	}
	return nil
}

// DisconnectSecretProvider logs out and forgets the token. Services that
// reference it can't deploy until it's connected again.
func (c *Core) DisconnectSecretProvider(ctx context.Context, id string) error {
	p, err := c.secretProvider(id)
	if err != nil {
		return err
	}
	if err := p.Disconnect(ctx); err != nil {
		c.log.Warn("secret provider logout failed", "provider", id, "err", err)
	}
	return c.store.SetSetting(ctx, secretTokenSetting(id), "")
}

// resolveSecrets returns the value of each reference from its provider. A
// failure is retried once after logging in again with the stored token, in
// case the session expired or the data dir was restored without it.
func (c *Core) resolveSecrets(ctx context.Context, refs []string) (map[string]string, error) {
	byScheme := map[string][]string{}
	for _, r := range refs {
		s := secrets.SchemeOf(r)
		byScheme[s] = append(byScheme[s], r)
	}
	out := make(map[string]string, len(refs))
	for scheme, rs := range byScheme {
		p := c.providerFor(scheme)
		if p == nil {
			return nil, fmt.Errorf("no password manager handles %s://", scheme)
		}
		name, id := p.Info().Name, p.Info().ID
		token, _ := c.store.GetSetting(ctx, secretTokenSetting(id))
		if token == "" {
			return nil, fmt.Errorf("%s isn't connected (Integrations › Password managers)", name)
		}
		vals, err := p.Resolve(ctx, rs)
		if err != nil {
			if lerr := p.Connect(ctx, token); lerr != nil {
				return nil, fmt.Errorf("%s: %v (logging in again: %v)", name, err, lerr)
			}
			if vals, err = p.Resolve(ctx, rs); err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
		}
		for k, v := range vals {
			out[k] = v
		}
	}
	return out, nil
}

func (c *Core) providerFor(scheme string) secrets.Provider {
	for _, p := range c.secrets {
		if p.Info().Scheme == scheme {
			return p
		}
	}
	return nil
}

func (c *Core) secretBrowser(ctx context.Context, id string) (secrets.Browser, error) {
	p, err := c.secretProvider(id)
	if err != nil {
		return nil, err
	}
	b, ok := p.(secrets.Browser)
	if !ok {
		return nil, fmt.Errorf("%w: %s can't list its items", ErrInvalid, p.Info().Name)
	}
	if !c.secretProviderView(ctx, p).Connected {
		return nil, fmt.Errorf("%w: %s isn't connected", ErrInvalid, p.Info().Name)
	}
	return b, nil
}

// SecretVaults lists the vaults the provider's token can read.
func (c *Core) SecretVaults(ctx context.Context, id string) ([]string, error) {
	b, err := c.secretBrowser(ctx, id)
	if err != nil {
		return nil, err
	}
	vs, err := b.Vaults(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return vs, nil
}

// SecretItems lists a vault's items with the fields env can reference.
func (c *Core) SecretItems(ctx context.Context, id, vault string) ([]secrets.Item, error) {
	b, err := c.secretBrowser(ctx, id)
	if err != nil {
		return nil, err
	}
	items, err := b.Items(ctx, vault)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return items, nil
}
