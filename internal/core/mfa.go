package core

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// ErrWrongPassword is a failed password confirmation on an account change.
// It's not ErrUnauthorized: the session is still valid.
var ErrWrongPassword = fmt.Errorf("%w: wrong password", ErrInvalid)

// ErrConfirmFirst asks a user without a password to confirm who they are
// at their provider (or with a passkey) before a sensitive change.
var ErrConfirmFirst = fmt.Errorf("%w: confirm it's you by signing in at your provider, then retry within %d minutes",
	ErrInvalid, int(confirmWindow.Minutes()))

// confirmWindow is how long a fresh sign-in at a provider (or a passkey
// sign-in) confirms the changes of an account without a password, in place
// of the password.
const confirmWindow = 10 * time.Minute

const (
	mfaTicketTTL = 5 * time.Minute
	// mfaTicketAttempts bounds the codes tried per password check.
	mfaTicketAttempts = 5
	totpSetupTTL      = 10 * time.Minute
	recoveryCodeCount = 10
)

type mfaTicket struct {
	userID   string
	attempts int
	// sso: the first factor was a provider, not the password.
	sso bool
}

// LoginMFA completes a password sign-in with a TOTP code or a recovery code.
func (c *Core) LoginMFA(ctx context.Context, ticket, code string) (store.User, string, error) {
	t, exp, ok := c.mfaTickets.take(ticket)
	if !ok {
		return store.User{}, "", fmt.Errorf("%w: the sign-in expired, enter your password again", ErrUnauthorized)
	}
	if !t.sso && c.SSORequired(ctx) {
		return store.User{}, "", ErrSSORequired
	}
	u, err := c.store.GetUser(ctx, t.userID)
	if err != nil {
		return store.User{}, "", err
	}
	if err := c.checkSecondFactor(ctx, u, code); err != nil {
		if t.attempts++; t.attempts < mfaTicketAttempts && errors.Is(err, ErrUnauthorized) {
			c.mfaTickets.put(ticket, t, exp)
		}
		return store.User{}, "", err
	}
	_ = c.store.DeleteExpiredSessions(ctx)
	token, err := c.newSession(ctx, u.ID)
	return u, token, err
}

// checkSecondFactor accepts a current TOTP code or an unused recovery code.
func (c *Core) checkSecondFactor(ctx context.Context, u store.User, code string) error {
	if u.TOTPSecret == "" {
		return ErrUnauthorized
	}
	if step, ok := totpMatch(u.TOTPSecret, code, time.Now()); ok {
		err := c.store.UseTOTPStep(ctx, u.ID, step)
		if errors.Is(err, store.ErrConflict) {
			return fmt.Errorf("%w: that code was already used, wait for the next one", ErrUnauthorized)
		}
		return err
	}
	if norm := normalizeRecoveryCode(code); len(norm) == recoveryCodeLen {
		err := c.store.UseRecoveryCode(ctx, u.ID, hashToken(norm))
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("%w: wrong code", ErrUnauthorized)
		}
		return err
	}
	return fmt.Errorf("%w: wrong code", ErrUnauthorized)
}

// Recovery codes: 10 letters and digits from an unambiguous alphabet, shown
// as xxxxx-xxxxx (~50 bits each).
const (
	recoveryAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	recoveryCodeLen  = 10
)

func newRecoveryCodes() (codes, hashes []string) {
	for range recoveryCodeCount {
		b := make([]byte, recoveryCodeLen)
		_, _ = rand.Read(b)
		for i := range b {
			// 256 % 31 leaves a slight bias, irrelevant at this length.
			b[i] = recoveryAlphabet[int(b[i])%len(recoveryAlphabet)]
		}
		code := string(b)
		codes = append(codes, code[:5]+"-"+code[5:])
		hashes = append(hashes, hashToken(code))
	}
	return codes, hashes
}

func normalizeRecoveryCode(code string) string {
	return strings.ToLower(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
}

// Account is the signed-in user's security settings.
type Account struct {
	Username    string `json:"username"`
	TOTPEnabled bool   `json:"totpEnabled"`
	// RecoveryCodes is how many unused recovery codes are left.
	RecoveryCodes int             `json:"recoveryCodes"`
	Passkeys      []store.Passkey `json:"passkeys"`
	// HasPassword is false for an account that signs in with single sign-on
	// (or passkeys) only: a fresh sign-in confirms its changes instead, and
	// Confirmed reports one for this session.
	HasPassword bool `json:"hasPassword"`
	Confirmed   bool `json:"confirmed"`
	// SSO lists the identities linked at single sign-on providers, and
	// SSOProviders those to link one at.
	SSO          []SSOIdentityView `json:"sso"`
	SSOProviders []SSOButton       `json:"ssoProviders"`
	// SSORequired: passkeys don't sign in for now.
	SSORequired bool `json:"ssoRequired"`
	// Role, Features and Grants say what the user may do, for the UI.
	Role     store.Role          `json:"role"`
	Features []string            `json:"features"`
	Grants   map[string][]string `json:"grants"`
}

// confirmed reports whether the request's session was confirmed by a
// fresh sign-in within confirmWindow.
func (c *Core) confirmed(ctx context.Context) bool {
	a, _ := ActorFrom(ctx)
	if a.Session == "" {
		return false
	}
	_, ok := c.confirmations.peek(a.Session)
	return ok
}

// confirm marks a session as confirmed for confirmWindow.
func (c *Core) confirm(session string) {
	c.confirmations.put(hashToken(session), struct{}{}, time.Now().Add(confirmWindow))
}

// currentUser is the signed-in user making the request; API tokens have none.
func (c *Core) currentUser(ctx context.Context) (store.User, error) {
	a, ok := ActorFrom(ctx)
	if !ok || a.UserID == "" {
		return store.User{}, ErrUnauthorized
	}
	return c.store.GetUser(ctx, a.UserID)
}

// SetPreferences replaces the current user's preferences.
func (c *Core) SetPreferences(ctx context.Context, p store.Preferences) (store.Preferences, error) {
	if p.MapView != "" && p.MapView != "3d" {
		return store.Preferences{}, fmt.Errorf("%w: map view must be empty (2D) or 3d", ErrInvalid)
	}
	u, err := c.currentUser(ctx)
	if err != nil {
		return store.Preferences{}, err
	}
	return p, c.store.SetUserPreferences(ctx, u.ID, p)
}

// confirmedUser is the current user, once they re-entered their password:
// a stolen session alone can't change how the account signs in.
func (c *Core) confirmedUser(ctx context.Context, password string) (store.User, error) {
	u, err := c.currentUser(ctx)
	if err != nil {
		return store.User{}, err
	}
	if u.PasswordHash == "" {
		if !c.confirmed(ctx) {
			return store.User{}, ErrConfirmFirst
		}
		return u, nil
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return store.User{}, ErrWrongPassword
	}
	return u, nil
}

func (c *Core) Account(ctx context.Context) (Account, error) {
	u, err := c.currentUser(ctx)
	if err != nil {
		return Account{}, err
	}
	n, err := c.store.CountRecoveryCodes(ctx, u.ID)
	if err != nil {
		return Account{}, err
	}
	ps, err := c.store.ListPasskeys(ctx, u.ID)
	if err != nil {
		return Account{}, err
	}
	ids, err := c.ssoIdentities(ctx, u.ID)
	if err != nil {
		return Account{}, err
	}
	acc := Account{Username: u.Username, TOTPEnabled: u.TOTPSecret != "", RecoveryCodes: n, Passkeys: ps, SSO: ids, SSOProviders: c.SSOButtons(ctx), SSORequired: c.SSORequired(ctx),
		HasPassword: u.PasswordHash != "", Confirmed: c.confirmed(ctx),
		Role: u.Role, Features: u.Features, Grants: map[string][]string{}}
	if u.Role != store.RoleAdmin {
		v, err := c.userView(ctx, u)
		if err != nil {
			return Account{}, err
		}
		acc.Grants = v.Grants
	}
	return acc, nil
}

// ChangePassword signs the user out everywhere and returns a new session
// for the caller.
func (c *Core) ChangePassword(ctx context.Context, current, password string) (string, error) {
	u, err := c.confirmedUser(ctx, current)
	if err != nil {
		return "", err
	}
	if err := validatePassword(password); err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	if err := c.store.SetPassword(ctx, u.ID, string(hash)); err != nil {
		return "", err
	}
	return c.newSession(ctx, u.ID)
}

type TOTPSetup struct {
	Secret string `json:"secret"`
	// URI is the otpauth:// link the QR code encodes.
	URI string `json:"uri"`
}

// BeginTOTP generates a secret; it's enabled once a code from it is
// confirmed with EnableTOTP.
func (c *Core) BeginTOTP(ctx context.Context, password string) (TOTPSetup, error) {
	u, err := c.confirmedUser(ctx, password)
	if err != nil {
		return TOTPSetup{}, err
	}
	if u.TOTPSecret != "" {
		return TOTPSetup{}, fmt.Errorf("%w: two-factor authentication is already on", ErrInvalid)
	}
	secret := newTOTPSecret()
	c.totpSetups.put(u.ID, secret, time.Now().Add(totpSetupTTL))
	return TOTPSetup{Secret: secret, URI: totpURI(secret, u.Username)}, nil
}

// EnableTOTP turns two-factor authentication on and returns the recovery
// codes, shown once.
func (c *Core) EnableTOTP(ctx context.Context, code string) ([]string, error) {
	u, err := c.currentUser(ctx)
	if err != nil {
		return nil, err
	}
	secret, exp, ok := c.totpSetups.take(u.ID)
	if !ok {
		return nil, fmt.Errorf("%w: the setup expired, start again", ErrInvalid)
	}
	step, ok := totpMatch(secret, code, time.Now())
	if !ok {
		c.totpSetups.put(u.ID, secret, exp)
		return nil, fmt.Errorf("%w: wrong code, check the authenticator app's clock", ErrInvalid)
	}
	codes, hashes := newRecoveryCodes()
	if err := c.store.SetTOTP(ctx, u.ID, secret, hashes); err != nil {
		return nil, err
	}
	// The confirmation code can't be used again to sign in.
	_ = c.store.UseTOTPStep(ctx, u.ID, step)
	return codes, nil
}

func (c *Core) DisableTOTP(ctx context.Context, password string) error {
	u, err := c.confirmedUser(ctx, password)
	if err != nil {
		return err
	}
	return c.store.SetTOTP(ctx, u.ID, "", nil)
}

func (c *Core) RegenerateRecoveryCodes(ctx context.Context, password string) ([]string, error) {
	u, err := c.confirmedUser(ctx, password)
	if err != nil {
		return nil, err
	}
	if u.TOTPSecret == "" {
		return nil, fmt.Errorf("%w: two-factor authentication is off", ErrInvalid)
	}
	codes, hashes := newRecoveryCodes()
	return codes, c.store.SetRecoveryCodes(ctx, u.ID, hashes)
}

// ResetTOTP turns two-factor authentication off, for an admin who lost both
// the authenticator and the recovery codes (CLI only).
func (c *Core) ResetTOTP(ctx context.Context, username string) error {
	u, err := c.store.GetUserByUsername(ctx, username)
	if err != nil {
		return err
	}
	return c.store.SetTOTP(ctx, u.ID, "", nil)
}
