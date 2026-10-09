package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/docker"
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

// secretHelperIdle is how long a password manager's helper container stays
// after its last call, and its listings stay cached.
const secretHelperIdle = 5 * time.Minute

// secretProviders are the password managers this build offers. Their CLIs
// run in helper containers on the manager's own Docker (none without one, in
// tests).
func secretProviders(cfg config.Config, local *docker.Client) []secrets.Provider {
	if local == nil {
		return nil
	}
	return []secrets.Provider{
		protonpass.New(protonpass.NewContainerRunner(local, cfg.ProtonPass.Image, secretHelperIdle)),
	}
}

// closeSecretProviders removes the providers' helper containers: left by a
// crash at startup, still idling at shutdown.
func (c *Core) closeSecretProviders(ctx context.Context) error {
	var errs []error
	for _, p := range c.secrets {
		if cl, ok := p.(interface{ Close(context.Context) error }); ok {
			errs = append(errs, cl.Close(ctx))
		}
	}
	return errors.Join(errs...)
}

// secretListings caches what providers list for the env picker (names and
// references, never values), so browsing doesn't run the CLI every time.
type secretListings struct {
	mu sync.Mutex
	m  map[string]secretListing // provider ID + "\x00" + vault ("" for vaults)
}

type secretListing struct {
	at  time.Time
	val any
}

func (l *secretListings) get(id, vault string) (any, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.m[id+"\x00"+vault]
	if !ok || time.Since(e.at) > secretHelperIdle {
		return nil, false
	}
	return e.val, true
}

func (l *secretListings) put(id, vault string, val any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[string]secretListing{}
	}
	l.m[id+"\x00"+vault] = secretListing{at: time.Now(), val: val}
}

// forget drops a provider's listings, after it logs in or out.
func (l *secretListings) forget(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k := range l.m {
		if strings.HasPrefix(k, id+"\x00") {
			delete(l.m, k)
		}
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
	c.secretListings.forget(id)
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
	// Long enough to pull a helper image on first use.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
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
	c.secretListings.forget(id)
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

// SecretVaults lists the vaults the provider's token can read, from a
// listing of the last few minutes unless refresh.
func (c *Core) SecretVaults(ctx context.Context, id string, refresh bool) ([]string, error) {
	b, err := c.secretBrowser(ctx, id)
	if err != nil {
		return nil, err
	}
	if v, ok := c.secretListings.get(id, ""); ok && !refresh {
		return v.([]string), nil
	}
	vs, err := b.Vaults(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	c.secretListings.put(id, "", vs)
	return vs, nil
}

// SecretItems lists a vault's items with the fields env can reference, from
// a listing of the last few minutes unless refresh.
func (c *Core) SecretItems(ctx context.Context, id, vault string, refresh bool) ([]secrets.Item, error) {
	if vault == "" {
		return nil, fmt.Errorf("%w: vault is required", ErrInvalid)
	}
	b, err := c.secretBrowser(ctx, id)
	if err != nil {
		return nil, err
	}
	if v, ok := c.secretListings.get(id, vault); ok && !refresh {
		return v.([]secrets.Item), nil
	}
	items, err := b.Items(ctx, vault)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	c.secretListings.put(id, vault, items)
	return items, nil
}
