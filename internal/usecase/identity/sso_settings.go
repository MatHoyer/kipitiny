package identity

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

// SSOProviderView is a provider with its client secret masked.
type SSOProviderView struct {
	domain.SSOProvider
}

func ssoProviderView(p domain.SSOProvider) SSOProviderView {
	if p.ClientSecret != "" {
		p.ClientSecret = SecretMask
	}
	return SSOProviderView{p}
}

func (c *Identity) ListSSOProviders(ctx context.Context) ([]SSOProviderView, error) {
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
func (c *Identity) CreateSSOProvider(ctx context.Context, in SSOProviderInput) (SSOProviderView, error) {
	if err := RequireAdmin(ctx); err != nil {
		return SSOProviderView{}, err
	}
	p, err := c.checkSSOProvider(ctx, domain.SSOProvider{}, in)
	if err != nil {
		return SSOProviderView{}, err
	}
	p, err = c.store.CreateSSOProvider(ctx, p)
	return ssoProviderView(p), ssoConflict(err)
}

func (c *Identity) UpdateSSOProvider(ctx context.Context, id string, in SSOProviderInput) (SSOProviderView, error) {
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
func (c *Identity) DeleteSSOProvider(ctx context.Context, id string) error {
	if err := RequireAdmin(ctx); err != nil {
		return err
	}
	if c.SSORequired(ctx) {
		if ps, err := c.store.ListSSOProviders(ctx); err != nil {
			return err
		} else if len(ps) == 1 && ps[0].ID == id {
			return fmt.Errorf("%w: single sign-on is required and this is the last provider; stop requiring it first", domain.ErrInvalid)
		}
	}
	if err := c.store.DeleteSSOProvider(ctx, id); err != nil {
		return err
	}
	c.forgetSSOProvider(id)
	return nil
}

// checkSSOProvider validates in over p and fetches the issuer's discovery
// document, so a typo shows up now rather than at the first sign-in.
func (c *Identity) checkSSOProvider(ctx context.Context, p domain.SSOProvider, in SSOProviderInput) (domain.SSOProvider, error) {
	p.Name, p.Issuer, p.ClientID = strings.TrimSpace(in.Name), strings.TrimSpace(in.Issuer), strings.TrimSpace(in.ClientID)
	if in.ClientSecret != SecretMask {
		p.ClientSecret = strings.TrimSpace(in.ClientSecret)
	}
	if p.Name == "" || utf8.RuneCountInString(p.Name) > 40 {
		return p, fmt.Errorf("%w: give the provider a name (max 40 characters)", domain.ErrInvalid)
	}
	if u, err := url.Parse(p.Issuer); err != nil || u.Scheme != "https" || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return p, fmt.Errorf("%w: the issuer must be an https URL, like https://auth.example.com/application/o/kipitiny/", domain.ErrInvalid)
	}
	if p.ClientID == "" {
		return p, fmt.Errorf("%w: enter the client ID", domain.ErrInvalid)
	}
	if p.ClientSecret == "" {
		return p, fmt.Errorf("%w: enter the client secret", domain.ErrInvalid)
	}
	if _, err := oidc.NewProvider(c.ssoContext(ctx), p.Issuer); err != nil {
		return p, fmt.Errorf("%w: couldn't read the issuer's OpenID configuration: %s", domain.ErrInvalid, err.Error())
	}
	return p, nil
}

func ssoConflict(err error) error {
	if err != nil && strings.Contains(err.Error(), domain.ErrConflict.Error()) {
		return fmt.Errorf("%w: a provider with this name exists", domain.ErrInvalid)
	}
	return err
}

const ssoRequiredSetting = "sso_required"

// ErrSSORequired refuses a password or passkey sign-in while single sign-on
// is required.
var ErrSSORequired = fmt.Errorf("%w: password and passkey sign-in are off on this server; sign in with single sign-on", domain.ErrInvalid)

// SSORequired reports whether sign-in goes through a provider only.
func (c *Identity) SSORequired(ctx context.Context) bool {
	v, err := c.store.GetSetting(ctx, ssoRequiredSetting)
	return err == nil && v == "true"
}

// SetSSORequired turns password and passkey sign-in off or back on. Turning
// it off is allowed; turning it on needs a provider the admin is linked at.
func (c *Identity) SetSSORequired(ctx context.Context, on bool) error {
	if err := RequireAdmin(ctx); err != nil {
		return err
	}
	if !on {
		return c.store.SetSetting(ctx, ssoRequiredSetting, "")
	}
	u, err := c.CurrentUser(ctx)
	if err != nil {
		return err
	}
	is, err := c.store.ListSSOIdentities(ctx, u.ID)
	if err != nil {
		return err
	}
	if len(is) == 0 {
		return fmt.Errorf("%w: link your own account at a provider first (Account › Single sign-on), so you can still sign in", domain.ErrInvalid)
	}
	return c.store.SetSetting(ctx, ssoRequiredSetting, "true")
}

// AllowPasswordLogin turns password and passkey sign-in back on, for the CLI
// when no provider works.
func (c *Identity) AllowPasswordLogin(ctx context.Context) error {
	return c.store.SetSetting(ctx, ssoRequiredSetting, "")
}
