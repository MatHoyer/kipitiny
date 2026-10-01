package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
