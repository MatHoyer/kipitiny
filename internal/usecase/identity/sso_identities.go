package identity

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

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
