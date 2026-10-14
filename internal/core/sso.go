package core

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// Single sign-on: admins add OpenID Connect providers (Google, Authentik,
// Keycloak, Pocket ID…); users sign in with an identity linked to their
// account. Providers never create accounts: invites and Account › Single
// sign-on link identities.

// ssoTimeout bounds a request to a provider (discovery, keys, code exchange).
const ssoTimeout = 15 * time.Second

// SSOCallbackPath is where providers send the browser back; admins register
// it as the redirect URI.
const SSOCallbackPath = "/api/auth/sso/callback"

// ssoState caches each provider's discovery document and keys.
type ssoState struct {
	mu        sync.Mutex
	providers map[string]*oidc.Provider // by provider ID
}

// SSOProviderView is a provider with its client secret masked.
type SSOProviderView struct {
	store.SSOProvider
}

func ssoProviderView(p store.SSOProvider) SSOProviderView {
	if p.ClientSecret != "" {
		p.ClientSecret = SecretMask
	}
	return SSOProviderView{p}
}

// SSOButton is a provider as the sign-in page shows it.
type SSOButton struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// SSOButtons lists the providers to sign in with; anyone may see them.
func (c *Core) SSOButtons(ctx context.Context) []SSOButton {
	ps, err := c.store.ListSSOProviders(ctx)
	if err != nil {
		c.log.Error("list sso providers", "err", err)
	}
	out := make([]SSOButton, len(ps))
	for i, p := range ps {
		out[i] = SSOButton{ID: p.ID, Name: p.Name}
	}
	return out
}

func (c *Core) ListSSOProviders(ctx context.Context) ([]SSOProviderView, error) {
	if err := RequireAdmin(ctx); err != nil {
		return nil, err
	}
	ps, err := c.store.ListSSOProviders(ctx)
	out := make([]SSOProviderView, len(ps))
	for i, p := range ps {
		out[i] = ssoProviderView(p)
	}
	return out, err
}

// SSOProviderInput configures a provider. A masked secret keeps the stored one.
type SSOProviderInput struct {
	Name         string `json:"name"`
	Issuer       string `json:"issuer"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

// CreateSSOProvider checks the issuer publishes its OpenID configuration
// and saves the provider.
func (c *Core) CreateSSOProvider(ctx context.Context, in SSOProviderInput) (SSOProviderView, error) {
	if err := RequireAdmin(ctx); err != nil {
		return SSOProviderView{}, err
	}
	p, err := c.checkSSOProvider(ctx, store.SSOProvider{}, in)
	if err != nil {
		return SSOProviderView{}, err
	}
	p, err = c.store.CreateSSOProvider(ctx, p)
	return ssoProviderView(p), ssoConflict(err)
}

func (c *Core) UpdateSSOProvider(ctx context.Context, id string, in SSOProviderInput) (SSOProviderView, error) {
	if err := RequireAdmin(ctx); err != nil {
		return SSOProviderView{}, err
	}
	old, err := c.store.GetSSOProvider(ctx, id)
	if err != nil {
		return SSOProviderView{}, err
	}
	p, err := c.checkSSOProvider(ctx, old, in)
	if err != nil {
		return SSOProviderView{}, err
	}
	if p, err = c.store.UpdateSSOProvider(ctx, p); err != nil {
		return SSOProviderView{}, ssoConflict(err)
	}
	c.forgetSSOProvider(id)
	return ssoProviderView(p), nil
}

// DeleteSSOProvider removes a provider; identities linked through it go too.
func (c *Core) DeleteSSOProvider(ctx context.Context, id string) error {
	if err := RequireAdmin(ctx); err != nil {
		return err
	}
	if err := c.store.DeleteSSOProvider(ctx, id); err != nil {
		return err
	}
	c.forgetSSOProvider(id)
	return nil
}

// checkSSOProvider validates in over p and fetches the issuer's discovery
// document, so a typo shows up now rather than at the first sign-in.
func (c *Core) checkSSOProvider(ctx context.Context, p store.SSOProvider, in SSOProviderInput) (store.SSOProvider, error) {
	p.Name, p.Issuer, p.ClientID = strings.TrimSpace(in.Name), strings.TrimSpace(in.Issuer), strings.TrimSpace(in.ClientID)
	if in.ClientSecret != SecretMask {
		p.ClientSecret = strings.TrimSpace(in.ClientSecret)
	}
	if p.Name == "" || utf8.RuneCountInString(p.Name) > 40 {
		return p, fmt.Errorf("%w: give the provider a name (max 40 characters)", ErrInvalid)
	}
	if u, err := url.Parse(p.Issuer); err != nil || u.Scheme != "https" || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return p, fmt.Errorf("%w: the issuer must be an https URL, like https://auth.example.com/application/o/kipitiny/", ErrInvalid)
	}
	if p.ClientID == "" {
		return p, fmt.Errorf("%w: enter the client ID", ErrInvalid)
	}
	if p.ClientSecret == "" {
		return p, fmt.Errorf("%w: enter the client secret", ErrInvalid)
	}
	if _, err := oidc.NewProvider(c.ssoContext(ctx), p.Issuer); err != nil {
		return p, fmt.Errorf("%w: couldn't read the issuer's OpenID configuration: %v", ErrInvalid, err)
	}
	return p, nil
}

func ssoConflict(err error) error {
	if err != nil && strings.Contains(err.Error(), store.ErrConflict.Error()) {
		return fmt.Errorf("%w: a provider with this name exists", ErrInvalid)
	}
	return err
}

// ssoContext makes go-oidc and oauth2 use a client with a timeout.
func (c *Core) ssoContext(ctx context.Context) context.Context {
	if c.ssoHTTP != nil {
		return oidc.ClientContext(ctx, c.ssoHTTP)
	}
	return oidc.ClientContext(ctx, &http.Client{Timeout: ssoTimeout})
}

// oidcProvider returns the provider's endpoints and keys, discovered once
// and kept until it changes (go-oidc refetches rotated keys itself).
func (c *Core) oidcProvider(ctx context.Context, p store.SSOProvider) (*oidc.Provider, error) {
	c.sso.mu.Lock()
	op := c.sso.providers[p.ID]
	c.sso.mu.Unlock()
	if op != nil {
		return op, nil
	}
	op, err := oidc.NewProvider(c.ssoContext(ctx), p.Issuer)
	if err != nil {
		return nil, fmt.Errorf("%w: %s is unreachable: %v", ErrInvalid, p.Name, err)
	}
	c.sso.mu.Lock()
	if c.sso.providers == nil {
		c.sso.providers = map[string]*oidc.Provider{}
	}
	c.sso.providers[p.ID] = op
	c.sso.mu.Unlock()
	return op, nil
}

func (c *Core) forgetSSOProvider(id string) {
	c.sso.mu.Lock()
	delete(c.sso.providers, id)
	c.sso.mu.Unlock()
}
