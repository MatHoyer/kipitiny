package identity

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/kipit-labs/kipitiny/internal/domain"
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
	domain.SSOProvider
}

func ssoProviderView(p domain.SSOProvider) SSOProviderView {
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
func (c *Identity) SSOButtons(ctx context.Context) []SSOButton {
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

// ssoContext makes go-oidc and oauth2 use a client with a timeout.
func (c *Identity) ssoContext(ctx context.Context) context.Context {
	if c.ssoHTTP != nil {
		return oidc.ClientContext(ctx, c.ssoHTTP)
	}
	return oidc.ClientContext(ctx, &http.Client{Timeout: ssoTimeout})
}

// oidcProvider returns the provider's endpoints and keys, discovered once
// and kept until it changes (go-oidc refetches rotated keys itself).
func (c *Identity) oidcProvider(ctx context.Context, p domain.SSOProvider) (*oidc.Provider, error) {
	c.sso.mu.Lock()
	op := c.sso.providers[p.ID]
	c.sso.mu.Unlock()
	if op != nil {
		return op, nil
	}
	op, err := oidc.NewProvider(c.ssoContext(ctx), p.Issuer)
	if err != nil {
		return nil, fmt.Errorf("%w: %s is unreachable: %s", domain.ErrInvalid, p.Name, err.Error())
	}
	c.sso.mu.Lock()
	if c.sso.providers == nil {
		c.sso.providers = map[string]*oidc.Provider{}
	}
	c.sso.providers[p.ID] = op
	c.sso.mu.Unlock()
	return op, nil
}

func (c *Identity) forgetSSOProvider(id string) {
	c.sso.mu.Lock()
	delete(c.sso.providers, id)
	c.sso.mu.Unlock()
}

// A sign-in at a provider is an authorization code flow with PKCE and a
// nonce. Its state is kept here and in a cookie of the browser that
// started it, so a link carrying someone else's code can't sign you in.

// ssoFlowTTL bounds the time spent at the provider.
const ssoFlowTTL = 10 * time.Minute

// What a flow is for.
const (
	ssoLogin   = "login"   // sign in
	ssoConnect = "connect" // link the identity to the signed-in user
	ssoInvite  = "invite"  // create the invited account, linked to the identity
	ssoConfirm = "confirm" // sign in again, freshly, to confirm a change
)

// ssoFreshness bounds how long before the flow started a sign-in counted
// as fresh may have happened (clock skew between the provider and us).
const ssoFreshness = time.Minute

type ssoFlow struct {
	purpose     string
	providerID  string
	verifier    string
	nonce       string
	redirectURL string
	// Connect: the user to link the identity to.
	userID string
	// Login: the page to land on once signed in.
	returnTo string
	// Invite: its token and the username picked.
	invite, username string
	// Confirm: the session to confirm (its token's hash), and when the flow
	// started, which the sign-in must not predate.
	session string
	started time.Time
}

// SSOStart is where to send the browser, and the state its cookie holds.
type SSOStart struct {
	URL   string `json:"url"`
	State string `json:"-"`
}

// BeginSSO starts signing in with a provider; managerURL is the origin
// the browser is on and returnTo the page to land on afterwards.
func (c *Identity) BeginSSO(ctx context.Context, providerID, managerURL, returnTo string) (SSOStart, error) {
	return c.beginSSO(ctx, ssoFlow{purpose: ssoLogin, providerID: providerID, returnTo: localPath(returnTo)}, managerURL)
}

// BeginSSOConnect starts linking an identity to the signed-in user, once
// they re-entered their password.
func (c *Identity) BeginSSOConnect(ctx context.Context, providerID, managerURL, password string) (SSOStart, error) {
	u, err := c.ConfirmedUser(ctx, password)
	if err != nil {
		return SSOStart{}, err
	}
	return c.beginSSO(ctx, ssoFlow{purpose: ssoConnect, providerID: providerID, userID: u.ID}, managerURL)
}

// BeginSSOInvite starts creating an invited account that signs in with a
// provider instead of a password.
func (c *Identity) BeginSSOInvite(ctx context.Context, providerID, managerURL, token, username string) (SSOStart, error) {
	if _, err := c.store.GetInviteByHash(ctx, hashToken(strings.TrimSpace(token))); err != nil {
		return SSOStart{}, inviteErr(err)
	}
	if err := c.checkNewUsername(ctx, username); err != nil {
		return SSOStart{}, err
	}
	return c.beginSSO(ctx, ssoFlow{purpose: ssoInvite, providerID: providerID, invite: strings.TrimSpace(token), username: username}, managerURL)
}

// BeginSSOConfirm starts a fresh sign-in at a provider the signed-in user
// is linked at, to confirm a change on an account without a password. The
// provider is asked to sign in again even if it has a session.
func (c *Identity) BeginSSOConfirm(ctx context.Context, providerID, managerURL, returnTo string) (SSOStart, error) {
	a, ok := ActorFrom(ctx)
	if !ok || a.Session == "" {
		return SSOStart{}, ErrUnauthorized
	}
	is, err := c.store.ListSSOIdentities(ctx, a.UserID)
	if err != nil {
		return SSOStart{}, err
	}
	if !slices.ContainsFunc(is, func(i domain.SSOIdentity) bool { return i.ProviderID == providerID }) {
		return SSOStart{}, fmt.Errorf("%w: your account isn't linked at this provider", domain.ErrInvalid)
	}
	return c.beginSSO(ctx, ssoFlow{purpose: ssoConfirm, providerID: providerID, userID: a.UserID, session: a.Session,
		started: time.Now(), returnTo: localPath(returnTo)}, managerURL)
}

func (c *Identity) beginSSO(ctx context.Context, f ssoFlow, managerURL string) (SSOStart, error) {
	p, err := c.store.GetSSOProvider(ctx, f.providerID)
	if err != nil {
		return SSOStart{}, err
	}
	op, err := c.oidcProvider(ctx, p)
	if err != nil {
		return SSOStart{}, err
	}
	f.verifier, f.nonce = oauth2.GenerateVerifier(), RandomToken(24)
	f.redirectURL = strings.TrimSuffix(managerURL, "/") + SSOCallbackPath
	state := RandomToken(24)
	c.ssoFlows.Put(state, f, time.Now().Add(ssoFlowTTL))
	opts := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(f.verifier), oidc.Nonce(f.nonce)}
	if f.purpose == ssoConfirm {
		// Ask for the credentials again; max_age=0 also makes providers
		// send auth_time, checked on return.
		opts = append(opts, oauth2.SetAuthURLParam("prompt", "login"), oauth2.SetAuthURLParam("max_age", "0"))
	}
	u := ssoOAuth(p, op, f.redirectURL).AuthCodeURL(state, opts...)
	return SSOStart{URL: u, State: state}, nil
}

func ssoOAuth(p domain.SSOProvider, op *oidc.Provider, redirectURL string) *oauth2.Config {
	return &oauth2.Config{ClientID: p.ClientID, ClientSecret: p.ClientSecret, Endpoint: op.Endpoint(), RedirectURL: redirectURL,
		Scopes: []string{oidc.ScopeOpenID, "email", "profile"}}
}

// SSOResult is a finished flow: a sign-in (a session, or a ticket for the
// second factor) or a linked identity.
type SSOResult struct {
	Purpose   string
	User      domain.User
	Session   string
	MFATicket string
	// ReturnTo is where a sign-in lands.
	ReturnTo string
	// Invite is the token of the invite being accepted, to go back to it.
	Invite string
}

// FinishSSO redeems the code the provider sent the browser back with.
// cookieState is the state the browser's cookie holds. Purpose is set even
// on failure once the flow is known, to send the browser back to its page.
func (c *Identity) FinishSSO(ctx context.Context, state, cookieState, code string) (SSOResult, error) {
	f, _, ok := c.ssoFlows.Take(state)
	if !ok {
		return SSOResult{Purpose: ssoLogin}, fmt.Errorf("%w: this sign-in expired or was already used; start again", domain.ErrInvalid)
	}
	res := SSOResult{Purpose: f.purpose, ReturnTo: f.returnTo, Invite: f.invite}
	if subtle.ConstantTimeCompare([]byte(state), []byte(cookieState)) != 1 {
		return res, fmt.Errorf("%w: this sign-in was started in another browser; start again here", domain.ErrInvalid)
	}
	p, err := c.store.GetSSOProvider(ctx, f.providerID)
	if err != nil {
		return res, err
	}
	id, err := c.ssoIdentity(ctx, p, f, code)
	if err != nil {
		return res, err
	}
	switch f.purpose {
	case ssoConnect:
		res.User, err = c.linkSSOIdentity(ctx, p, f.userID, id)
		return res, err
	case ssoInvite:
		res.User, res.Session, err = c.acceptSSOInvite(ctx, p, f, id)
		return res, err
	case ssoConfirm:
		res.User, err = c.confirmSSO(ctx, p, f, id)
		return res, err
	default:
		res.User, res.Session, res.MFATicket, err = c.ssoSignIn(ctx, p, id)
		return res, err
	}
}

// ssoClaims is who the provider says signed in.
type ssoClaims struct {
	Subject string
	Email   string
	// AuthTime is when the user last entered their credentials there; zero
	// when the provider didn't say.
	AuthTime time.Time
}

// ssoIdentity exchanges the code and checks the ID token: signature,
// issuer, audience, expiry and the flow's nonce.
func (c *Identity) ssoIdentity(ctx context.Context, p domain.SSOProvider, f ssoFlow, code string) (ssoClaims, error) {
	op, err := c.oidcProvider(ctx, p)
	if err != nil {
		return ssoClaims{}, err
	}
	hctx := c.ssoContext(ctx)
	tok, err := ssoOAuth(p, op, f.redirectURL).Exchange(hctx, code, oauth2.VerifierOption(f.verifier))
	if err != nil {
		return ssoClaims{}, fmt.Errorf("%w: %s refused the sign-in: %s", domain.ErrInvalid, p.Name, err.Error())
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return ssoClaims{}, fmt.Errorf("%w: %s sent no ID token; is it an OpenID Connect provider?", domain.ErrInvalid, p.Name)
	}
	idt, err := op.Verifier(&oidc.Config{ClientID: p.ClientID}).Verify(hctx, raw)
	if err != nil {
		return ssoClaims{}, fmt.Errorf("%w: %s sent an invalid ID token: %s", domain.ErrInvalid, p.Name, err.Error())
	}
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(f.nonce)) != 1 {
		return ssoClaims{}, fmt.Errorf("%w: %s sent an ID token for another sign-in", domain.ErrInvalid, p.Name)
	}
	var claims struct {
		Email    string `json:"email"`
		AuthTime int64  `json:"auth_time"`
	}
	_ = idt.Claims(&claims)
	out := ssoClaims{Subject: idt.Subject, Email: claims.Email}
	if claims.AuthTime > 0 {
		out.AuthTime = time.Unix(claims.AuthTime, 0)
	}
	return out, nil
}

// ssoSignIn signs in the user the identity is linked to. Two-factor
// authentication still applies: the provider stands in for the password.
func (c *Identity) ssoSignIn(ctx context.Context, p domain.SSOProvider, id ssoClaims) (domain.User, string, string, error) {
	link, err := c.store.GetSSOIdentity(ctx, p.ID, id.Subject)
	if errors.Is(err, domain.ErrNotFound) {
		who := ""
		if id.Email != "" {
			who = " (" + id.Email + ")"
		}
		return domain.User{}, "", "", fmt.Errorf("%w: no kipitiny account is linked to this %s account%s; ask an admin for an invite, or link it from Account once signed in",
			ErrUnauthorized, p.Name, who)
	}
	if err != nil {
		return domain.User{}, "", "", err
	}
	u, err := c.store.GetUser(ctx, link.UserID)
	if err != nil {
		return domain.User{}, "", "", err
	}
	_ = c.store.SSOIdentityUsed(ctx, link.ID, id.Email)
	if u.TOTPSecret != "" {
		ticket := RandomToken(32)
		c.mfaTickets.Put(ticket, mfaTicket{userID: u.ID, sso: true}, time.Now().Add(mfaTicketTTL))
		return u, "", ticket, nil
	}
	_ = c.store.DeleteExpiredSessions(ctx)
	session, err := c.newSession(ctx, u.ID)
	return u, session, "", err
}

func (c *Identity) linkSSOIdentity(ctx context.Context, p domain.SSOProvider, userID string, id ssoClaims) (domain.User, error) {
	u, err := c.store.GetUser(ctx, userID)
	if err != nil {
		return domain.User{}, err
	}
	if link, err := c.store.GetSSOIdentity(ctx, p.ID, id.Subject); err == nil {
		if link.UserID == userID {
			return u, nil
		}
		return u, fmt.Errorf("%w: this %s account is linked to another kipitiny user", domain.ErrInvalid, p.Name)
	}
	_, err = c.store.CreateSSOIdentity(ctx, domain.SSOIdentity{UserID: userID, ProviderID: p.ID, Subject: id.Subject, Email: id.Email})
	if errors.Is(err, domain.ErrConflict) {
		return u, fmt.Errorf("%w: another %s account is linked to yours; unlink it first", domain.ErrInvalid, p.Name)
	}
	return u, err
}

// acceptSSOInvite creates the invited account without a password, linked
// to the identity, and signs it in.
func (c *Identity) acceptSSOInvite(ctx context.Context, p domain.SSOProvider, f ssoFlow, id ssoClaims) (domain.User, string, error) {
	if link, err := c.store.GetSSOIdentity(ctx, p.ID, id.Subject); err == nil {
		owner := "another user"
		if u, err := c.store.GetUser(ctx, link.UserID); err == nil {
			owner = u.Username
		}
		return domain.User{}, "", fmt.Errorf("%w: this %s account already signs in as %s", domain.ErrInvalid, p.Name, owner)
	}
	i, err := c.store.GetInviteByHash(ctx, hashToken(f.invite))
	if err != nil {
		return domain.User{}, "", inviteErr(err)
	}
	if err := c.checkNewUsername(ctx, f.username); err != nil {
		return domain.User{}, "", err
	}
	u, err := c.store.AcceptInvite(ctx, i.ID, domain.User{Username: f.username},
		&domain.SSOIdentity{ProviderID: p.ID, Subject: id.Subject, Email: id.Email})
	if err != nil {
		return domain.User{}, "", inviteErr(err)
	}
	session, err := c.newSession(ctx, u.ID)
	return u, session, err
}

// confirmSSO marks the flow's session as confirmed, if the identity is the
// user's and the provider asked for its credentials during the flow.
func (c *Identity) confirmSSO(ctx context.Context, p domain.SSOProvider, f ssoFlow, id ssoClaims) (domain.User, error) {
	link, err := c.store.GetSSOIdentity(ctx, p.ID, id.Subject)
	if err != nil || link.UserID != f.userID {
		return domain.User{}, fmt.Errorf("%w: that %s account isn't the one linked to yours", domain.ErrInvalid, p.Name)
	}
	if id.AuthTime.IsZero() || id.AuthTime.Before(f.started.Add(-ssoFreshness)) {
		return domain.User{}, fmt.Errorf("%w: %s didn't ask for your credentials again, so it can't confirm it's you; "+
			"sign out of %s and retry", domain.ErrInvalid, p.Name, p.Name)
	}
	u, err := c.store.GetUser(ctx, f.userID)
	if err != nil {
		return domain.User{}, err
	}
	c.confirmations.Put(f.session, struct{}{}, time.Now().Add(confirmWindow))
	return u, nil
}

// SSOIdentityView is a linked identity, for the account page.
type SSOIdentityView struct {
	domain.SSOIdentity
	ProviderName string `json:"providerName"`
}

func (c *Identity) ssoIdentities(ctx context.Context, userID string) ([]SSOIdentityView, error) {
	is, err := c.store.ListSSOIdentities(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]SSOIdentityView, len(is))
	for i, id := range is {
		out[i] = SSOIdentityView{SSOIdentity: id}
		if p, err := c.store.GetSSOProvider(ctx, id.ProviderID); err == nil {
			out[i].ProviderName = p.Name
		}
	}
	return out, nil
}

// UnlinkSSOIdentity removes one of the signed-in user's identities.
func (c *Identity) UnlinkSSOIdentity(ctx context.Context, id string) error {
	u, err := c.CurrentUser(ctx)
	if err != nil {
		return err
	}
	if err := c.keepASignIn(ctx, u, "", id); err != nil {
		return err
	}
	return c.store.DeleteSSOIdentity(ctx, u.ID, id)
}

// keepASignIn fails when removing that passkey or identity would leave an
// account without a password no way to sign in.
func (c *Identity) keepASignIn(ctx context.Context, u domain.User, passkeyID, identityID string) error {
	if u.PasswordHash != "" {
		return nil
	}
	ps, err := c.store.ListPasskeys(ctx, u.ID)
	if err != nil {
		return err
	}
	is, err := c.store.ListSSOIdentities(ctx, u.ID)
	if err != nil {
		return err
	}
	left := len(ps) + len(is)
	if slices.ContainsFunc(ps, func(p domain.Passkey) bool { return p.ID == passkeyID }) ||
		slices.ContainsFunc(is, func(i domain.SSOIdentity) bool { return i.ID == identityID }) {
		left--
	}
	if left == 0 {
		return fmt.Errorf("%w: it's the only way you sign in; set a password or add a passkey first", domain.ErrInvalid)
	}
	return nil
}

// localPath keeps p if it is a path on this host, so a sign-in can't be
// made to land elsewhere.
func localPath(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.ContainsAny(p, "\\\r\n") {
		return "/"
	}
	return p
}

// Requiring single sign-on turns password and passkey sign-in off: users
// sign in through a provider only (API tokens keep working). An admin turns
// it on while linked at a provider, so someone can still sign in; the CLI's
// allow-password-login turns it off when no provider works.

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
