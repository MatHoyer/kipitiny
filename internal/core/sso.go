package core

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
)

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
}

// SSOStart is where to send the browser, and the state its cookie holds.
type SSOStart struct {
	URL   string `json:"url"`
	State string `json:"-"`
}

// BeginSSO starts signing in with a provider; managerURL is the origin
// the browser is on and returnTo the page to land on afterwards.
func (c *Core) BeginSSO(ctx context.Context, providerID, managerURL, returnTo string) (SSOStart, error) {
	return c.beginSSO(ctx, ssoFlow{purpose: ssoLogin, providerID: providerID, returnTo: localPath(returnTo)}, managerURL)
}

// BeginSSOConnect starts linking an identity to the signed-in user, once
// they re-entered their password.
func (c *Core) BeginSSOConnect(ctx context.Context, providerID, managerURL, password string) (SSOStart, error) {
	u, err := c.confirmedUser(ctx, password)
	if err != nil {
		return SSOStart{}, err
	}
	return c.beginSSO(ctx, ssoFlow{purpose: ssoConnect, providerID: providerID, userID: u.ID}, managerURL)
}

// BeginSSOInvite starts creating an invited account that signs in with a
// provider instead of a password.
func (c *Core) BeginSSOInvite(ctx context.Context, providerID, managerURL, token, username string) (SSOStart, error) {
	if _, err := c.store.GetInviteByHash(ctx, hashToken(strings.TrimSpace(token))); err != nil {
		return SSOStart{}, inviteErr(err)
	}
	if err := c.checkNewUsername(ctx, username); err != nil {
		return SSOStart{}, err
	}
	return c.beginSSO(ctx, ssoFlow{purpose: ssoInvite, providerID: providerID, invite: strings.TrimSpace(token), username: username}, managerURL)
}

func (c *Core) beginSSO(ctx context.Context, f ssoFlow, managerURL string) (SSOStart, error) {
	p, err := c.store.GetSSOProvider(ctx, f.providerID)
	if err != nil {
		return SSOStart{}, err
	}
	op, err := c.oidcProvider(ctx, p)
	if err != nil {
		return SSOStart{}, err
	}
	f.verifier, f.nonce = oauth2.GenerateVerifier(), randomToken(24)
	f.redirectURL = strings.TrimSuffix(managerURL, "/") + SSOCallbackPath
	state := randomToken(24)
	c.ssoFlows.put(state, f, time.Now().Add(ssoFlowTTL))
	u := ssoOAuth(p, op, f.redirectURL).AuthCodeURL(state, oauth2.S256ChallengeOption(f.verifier), oidc.Nonce(f.nonce))
	return SSOStart{URL: u, State: state}, nil
}

func ssoOAuth(p store.SSOProvider, op *oidc.Provider, redirectURL string) *oauth2.Config {
	return &oauth2.Config{ClientID: p.ClientID, ClientSecret: p.ClientSecret, Endpoint: op.Endpoint(), RedirectURL: redirectURL,
		Scopes: []string{oidc.ScopeOpenID, "email", "profile"}}
}

// SSOResult is a finished flow: a sign-in (a session, or a ticket for the
// second factor) or a linked identity.
type SSOResult struct {
	Purpose   string
	User      store.User
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
func (c *Core) FinishSSO(ctx context.Context, state, cookieState, code string) (SSOResult, error) {
	f, _, ok := c.ssoFlows.take(state)
	if !ok {
		return SSOResult{Purpose: ssoLogin}, fmt.Errorf("%w: this sign-in expired or was already used; start again", ErrInvalid)
	}
	res := SSOResult{Purpose: f.purpose, ReturnTo: f.returnTo, Invite: f.invite}
	if subtle.ConstantTimeCompare([]byte(state), []byte(cookieState)) != 1 {
		return res, fmt.Errorf("%w: this sign-in was started in another browser; start again here", ErrInvalid)
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
	default:
		res.User, res.Session, res.MFATicket, err = c.ssoSignIn(ctx, p, id)
		return res, err
	}
}

// ssoClaims is who the provider says signed in.
type ssoClaims struct {
	Subject string
	Email   string
}

// ssoIdentity exchanges the code and checks the ID token: signature,
// issuer, audience, expiry and the flow's nonce.
func (c *Core) ssoIdentity(ctx context.Context, p store.SSOProvider, f ssoFlow, code string) (ssoClaims, error) {
	op, err := c.oidcProvider(ctx, p)
	if err != nil {
		return ssoClaims{}, err
	}
	hctx := c.ssoContext(ctx)
	tok, err := ssoOAuth(p, op, f.redirectURL).Exchange(hctx, code, oauth2.VerifierOption(f.verifier))
	if err != nil {
		return ssoClaims{}, fmt.Errorf("%w: %s refused the sign-in: %v", ErrInvalid, p.Name, err)
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return ssoClaims{}, fmt.Errorf("%w: %s sent no ID token; is it an OpenID Connect provider?", ErrInvalid, p.Name)
	}
	idt, err := op.Verifier(&oidc.Config{ClientID: p.ClientID}).Verify(hctx, raw)
	if err != nil {
		return ssoClaims{}, fmt.Errorf("%w: %s sent an invalid ID token: %v", ErrInvalid, p.Name, err)
	}
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(f.nonce)) != 1 {
		return ssoClaims{}, fmt.Errorf("%w: %s sent an ID token for another sign-in", ErrInvalid, p.Name)
	}
	var claims struct {
		Email string `json:"email"`
	}
	_ = idt.Claims(&claims)
	return ssoClaims{Subject: idt.Subject, Email: claims.Email}, nil
}

// ssoSignIn signs in the user the identity is linked to. Two-factor
// authentication still applies: the provider stands in for the password.
func (c *Core) ssoSignIn(ctx context.Context, p store.SSOProvider, id ssoClaims) (store.User, string, string, error) {
	link, err := c.store.GetSSOIdentity(ctx, p.ID, id.Subject)
	if errors.Is(err, store.ErrNotFound) {
		who := ""
		if id.Email != "" {
			who = " (" + id.Email + ")"
		}
		return store.User{}, "", "", fmt.Errorf("%w: no kipitiny account is linked to this %s account%s; ask an admin for an invite, or link it from Account once signed in",
			ErrUnauthorized, p.Name, who)
	}
	if err != nil {
		return store.User{}, "", "", err
	}
	u, err := c.store.GetUser(ctx, link.UserID)
	if err != nil {
		return store.User{}, "", "", err
	}
	_ = c.store.SSOIdentityUsed(ctx, link.ID, id.Email)
	if u.TOTPSecret != "" {
		ticket := randomToken(32)
		c.mfaTickets.put(ticket, mfaTicket{userID: u.ID}, time.Now().Add(mfaTicketTTL))
		return u, "", ticket, nil
	}
	_ = c.store.DeleteExpiredSessions(ctx)
	session, err := c.newSession(ctx, u.ID)
	return u, session, "", err
}

func (c *Core) linkSSOIdentity(ctx context.Context, p store.SSOProvider, userID string, id ssoClaims) (store.User, error) {
	u, err := c.store.GetUser(ctx, userID)
	if err != nil {
		return store.User{}, err
	}
	if link, err := c.store.GetSSOIdentity(ctx, p.ID, id.Subject); err == nil {
		if link.UserID == userID {
			return u, nil
		}
		return u, fmt.Errorf("%w: this %s account is linked to another kipitiny user", ErrInvalid, p.Name)
	}
	_, err = c.store.CreateSSOIdentity(ctx, store.SSOIdentity{UserID: userID, ProviderID: p.ID, Subject: id.Subject, Email: id.Email})
	if errors.Is(err, store.ErrConflict) {
		return u, fmt.Errorf("%w: another %s account is linked to yours; unlink it first", ErrInvalid, p.Name)
	}
	return u, err
}

// acceptSSOInvite creates the invited account without a password, linked
// to the identity, and signs it in.
func (c *Core) acceptSSOInvite(ctx context.Context, p store.SSOProvider, f ssoFlow, id ssoClaims) (store.User, string, error) {
	if link, err := c.store.GetSSOIdentity(ctx, p.ID, id.Subject); err == nil {
		owner := "another user"
		if u, err := c.store.GetUser(ctx, link.UserID); err == nil {
			owner = u.Username
		}
		return store.User{}, "", fmt.Errorf("%w: this %s account already signs in as %s", ErrInvalid, p.Name, owner)
	}
	i, err := c.store.GetInviteByHash(ctx, hashToken(f.invite))
	if err != nil {
		return store.User{}, "", inviteErr(err)
	}
	if err := c.checkNewUsername(ctx, f.username); err != nil {
		return store.User{}, "", err
	}
	u, err := c.store.AcceptInvite(ctx, i.ID, store.User{Username: f.username},
		&store.SSOIdentity{ProviderID: p.ID, Subject: id.Subject, Email: id.Email})
	if err != nil {
		return store.User{}, "", inviteErr(err)
	}
	session, err := c.newSession(ctx, u.ID)
	return u, session, err
}

// SSOIdentityView is a linked identity, for the account page.
type SSOIdentityView struct {
	store.SSOIdentity
	ProviderName string `json:"providerName"`
}

func (c *Core) ssoIdentities(ctx context.Context, userID string) ([]SSOIdentityView, error) {
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
func (c *Core) UnlinkSSOIdentity(ctx context.Context, id string) error {
	u, err := c.currentUser(ctx)
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
func (c *Core) keepASignIn(ctx context.Context, u store.User, passkeyID, identityID string) error {
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
	if slices.ContainsFunc(ps, func(p store.Passkey) bool { return p.ID == passkeyID }) ||
		slices.ContainsFunc(is, func(i store.SSOIdentity) bool { return i.ID == identityID }) {
		left--
	}
	if left == 0 {
		return fmt.Errorf("%w: it's the only way you sign in; set a password or add a passkey first", ErrInvalid)
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
