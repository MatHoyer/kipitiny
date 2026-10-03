package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/MatHoyer/kipitiny/internal/store"
)

const ceremonyTTL = 5 * time.Minute

// RelyingParty is where the browser reached the manager: passkeys are bound
// to the domain (ID), so one registered at a domain only works there.
type RelyingParty struct {
	ID     string
	Origin string
}

func (rp RelyingParty) webauthn() (*webauthn.WebAuthn, error) {
	w, err := webauthn.New(&webauthn.Config{RPID: rp.ID, RPDisplayName: "kipitiny", RPOrigins: []string{rp.Origin}})
	if err != nil {
		return nil, fmt.Errorf("%w: passkeys need the manager to be opened at a domain name (not an IP address): %v", ErrInvalid, err)
	}
	return w, nil
}

// ceremony is a WebAuthn registration or sign-in between its two steps.
type ceremony struct {
	rp      RelyingParty
	session webauthn.SessionData
	userID  string // registration only
}

// webauthnUser adapts a user and their passkeys to the webauthn library.
type webauthnUser struct {
	u     store.User
	creds []webauthn.Credential
}

func (w webauthnUser) WebAuthnID() []byte                         { return []byte(w.u.ID) }
func (w webauthnUser) WebAuthnName() string                       { return w.u.Username }
func (w webauthnUser) WebAuthnDisplayName() string                { return w.u.Username }
func (w webauthnUser) WebAuthnCredentials() []webauthn.Credential { return w.creds }

func (c *Core) webauthnUser(ctx context.Context, u store.User) (webauthnUser, error) {
	ps, err := c.store.ListPasskeys(ctx, u.ID)
	if err != nil {
		return webauthnUser{}, err
	}
	w := webauthnUser{u: u}
	for _, p := range ps {
		var cred webauthn.Credential
		if err := json.Unmarshal([]byte(p.Credential), &cred); err != nil {
			return webauthnUser{}, fmt.Errorf("passkey %s: %w", p.ID, err)
		}
		w.creds = append(w.creds, cred)
	}
	return w, nil
}

func credentialID(id []byte) string { return base64.RawURLEncoding.EncodeToString(id) }

// PasskeyOptions starts a ceremony: the browser passes Options to
// navigator.credentials and the result back with the Ceremony ID.
type PasskeyOptions struct {
	Ceremony string `json:"ceremony"`
	Options  any    `json:"options"`
}

// BeginPasskeyRegistration adds a passkey to the current user, once they
// re-entered their password.
func (c *Core) BeginPasskeyRegistration(ctx context.Context, rp RelyingParty, password string) (PasskeyOptions, error) {
	u, err := c.confirmedUser(ctx, password)
	if err != nil {
		return PasskeyOptions{}, err
	}
	w, err := rp.webauthn()
	if err != nil {
		return PasskeyOptions{}, err
	}
	wu, err := c.webauthnUser(ctx, u)
	if err != nil {
		return PasskeyOptions{}, err
	}
	creation, session, err := w.BeginRegistration(wu,
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			RequireResidentKey: protocol.ResidentKeyRequired(),
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			UserVerification:   protocol.VerificationRequired,
		}),
		webauthn.WithExclusions(webauthn.Credentials(wu.creds).CredentialDescriptors()),
	)
	if err != nil {
		return PasskeyOptions{}, err
	}
	id := randomToken(16)
	c.ceremonies.put(id, ceremony{rp: rp, session: *session, userID: u.ID}, time.Now().Add(ceremonyTTL))
	return PasskeyOptions{Ceremony: id, Options: creation}, nil
}

func (c *Core) FinishPasskeyRegistration(ctx context.Context, ceremonyID, name string, response []byte) (store.Passkey, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 60 {
		return store.Passkey{}, fmt.Errorf("%w: give the passkey a name (max 60 characters)", ErrInvalid)
	}
	u, err := c.currentUser(ctx)
	if err != nil {
		return store.Passkey{}, err
	}
	cer, _, ok := c.ceremonies.take(ceremonyID)
	if !ok || cer.userID != u.ID {
		return store.Passkey{}, fmt.Errorf("%w: the passkey setup expired, start again", ErrInvalid)
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(response)
	if err != nil {
		return store.Passkey{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	w, err := cer.rp.webauthn()
	if err != nil {
		return store.Passkey{}, err
	}
	wu, err := c.webauthnUser(ctx, u)
	if err != nil {
		return store.Passkey{}, err
	}
	cred, err := w.CreateCredential(wu, cer.session, parsed)
	if err != nil {
		return store.Passkey{}, fmt.Errorf("%w: the passkey was refused: %v", ErrInvalid, err)
	}
	raw, err := json.Marshal(cred)
	if err != nil {
		return store.Passkey{}, err
	}
	return c.store.CreatePasskey(ctx, store.Passkey{UserID: u.ID, Name: name, CredentialID: credentialID(cred.ID), Credential: string(raw)})
}

func (c *Core) DeletePasskey(ctx context.Context, id string) error {
	u, err := c.currentUser(ctx)
	if err != nil {
		return err
	}
	return c.store.DeletePasskey(ctx, u.ID, id)
}

// BeginPasskeyLogin starts a passwordless sign-in: the browser offers the
// passkeys it holds for this domain.
func (c *Core) BeginPasskeyLogin(rp RelyingParty) (PasskeyOptions, error) {
	w, err := rp.webauthn()
	if err != nil {
		return PasskeyOptions{}, err
	}
	assertion, session, err := w.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return PasskeyOptions{}, err
	}
	id := randomToken(16)
	c.ceremonies.put(id, ceremony{rp: rp, session: *session}, time.Now().Add(ceremonyTTL))
	return PasskeyOptions{Ceremony: id, Options: assertion}, nil
}

// FinishPasskeyLogin signs in with a passkey. It counts as both factors (the
// device and its PIN or biometric), so TOTP isn't asked for.
func (c *Core) FinishPasskeyLogin(ctx context.Context, ceremonyID string, response []byte) (store.User, string, error) {
	cer, _, ok := c.ceremonies.take(ceremonyID)
	if !ok || cer.userID != "" {
		return store.User{}, "", fmt.Errorf("%w: the sign-in expired, try again", ErrUnauthorized)
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(response)
	if err != nil {
		return store.User{}, "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	w, err := cer.rp.webauthn()
	if err != nil {
		return store.User{}, "", err
	}
	var user webauthnUser
	_, cred, err := w.ValidatePasskeyLogin(func(rawID, userHandle []byte) (webauthn.User, error) {
		p, err := c.store.GetPasskeyByCredentialID(ctx, credentialID(rawID))
		if err != nil {
			return nil, err
		}
		if p.UserID != string(userHandle) {
			return nil, errors.New("passkey belongs to another user")
		}
		u, err := c.store.GetUser(ctx, p.UserID)
		if err != nil {
			return nil, err
		}
		user, err = c.webauthnUser(ctx, u)
		return user, err
	}, cer.session, parsed)
	if err != nil {
		c.log.Info("passkey sign-in refused", "error", err)
		return store.User{}, "", fmt.Errorf("%w: unknown or invalid passkey", ErrUnauthorized)
	}
	if cred.Authenticator.CloneWarning {
		c.log.Warn("passkey sign count went backwards: it may have been cloned", "user", user.u.Username)
	}
	if p, err := c.store.GetPasskeyByCredentialID(ctx, credentialID(cred.ID)); err == nil {
		if raw, err := json.Marshal(cred); err == nil {
			_ = c.store.PasskeyUsed(ctx, p.ID, string(raw))
		}
	}
	_ = c.store.DeleteExpiredSessions(ctx)
	token, err := c.newSession(ctx, user.u.ID)
	return user.u, token, err
}
