package identity

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

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
