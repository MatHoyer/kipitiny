package core

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"github.com/MatHoyer/kipitiny/internal/store"
)

var ErrUnauthorized = errors.New("unauthorized")

const (
	SessionTTL        = 30 * 24 * time.Hour
	minPasswordLength = 10
	// bcrypt ignores everything past 72 bytes; refuse rather than truncate.
	maxPasswordBytes = 72
)

// dummyHash keeps login timing the same whether or not the user exists.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("kipitiny-dummy-password"), bcrypt.DefaultCost)

type AuthState struct {
	SetupRequired bool        `json:"setupRequired"`
	User          *store.User `json:"user,omitempty"`
}

// InitAuth creates the one-time token required to create the first
// admin, so whoever reaches a fresh instance first can't claim it without
// access to the server logs.
func (c *Core) InitAuth(ctx context.Context) error {
	n, err := c.store.CountUsers(ctx)
	if err != nil || n > 0 {
		return err
	}
	token := c.cfg.SetupToken
	if token == "" {
		token = randomToken(16)
	}
	c.setupMu.Lock()
	c.setupToken = token
	c.setupMu.Unlock()
	c.log.Warn("no admin account yet: open the UI and use this setup token", "setup_token", token)
	return nil
}

func (c *Core) AuthState(ctx context.Context, sessionToken string) (AuthState, error) {
	n, err := c.store.CountUsers(ctx)
	if err != nil {
		return AuthState{}, err
	}
	if n == 0 {
		return AuthState{SetupRequired: true}, nil
	}
	u, err := c.Authenticate(ctx, sessionToken)
	if errors.Is(err, ErrUnauthorized) {
		return AuthState{}, nil
	}
	if err != nil {
		return AuthState{}, err
	}
	return AuthState{User: &u}, nil
}

// Setup creates the first admin account and returns a session token.
func (c *Core) Setup(ctx context.Context, setupToken, username, password string) (store.User, string, error) {
	c.setupMu.Lock()
	defer c.setupMu.Unlock()

	if n, err := c.store.CountUsers(ctx); err != nil {
		return store.User{}, "", err
	} else if n > 0 {
		return store.User{}, "", fmt.Errorf("%w: setup already completed", ErrInvalid)
	}
	if c.setupToken == "" || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(setupToken)), []byte(c.setupToken)) != 1 {
		return store.User{}, "", fmt.Errorf("%w: wrong setup token (see the manager logs)", ErrUnauthorized)
	}
	if !nameRe.MatchString(username) {
		return store.User{}, "", fmt.Errorf("%w: username must be lowercase letters, digits and dashes", ErrInvalid)
	}
	if err := validatePassword(password); err != nil {
		return store.User{}, "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return store.User{}, "", err
	}
	u, err := c.store.CreateUser(ctx, store.User{Username: username, PasswordHash: string(hash)})
	if err != nil {
		return store.User{}, "", err
	}
	c.setupToken = ""
	token, err := c.newSession(ctx, u.ID)
	return u, token, err
}

// ResetPassword sets a new password and signs the user out everywhere.
func (c *Core) ResetPassword(ctx context.Context, username, password string) error {
	u, err := c.store.GetUserByUsername(ctx, username)
	if err != nil {
		return err
	}
	if err := validatePassword(password); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return c.store.SetPassword(ctx, u.ID, string(hash))
}

func validatePassword(p string) error {
	if utf8.RuneCountInString(p) < minPasswordLength {
		return fmt.Errorf("%w: password must be at least %d characters", ErrInvalid, minPasswordLength)
	}
	if len(p) > maxPasswordBytes {
		return fmt.Errorf("%w: password must be at most %d bytes", ErrInvalid, maxPasswordBytes)
	}
	return nil
}

// LoginResult is a new session, or a ticket for the second step when the
// user has two-factor authentication on.
type LoginResult struct {
	User    store.User
	Session string
	// MFATicket redeems a TOTP or recovery code (LoginMFA) for a session.
	MFATicket string
}

func (c *Core) Login(ctx context.Context, username, password string) (LoginResult, error) {
	u, err := c.store.GetUserByUsername(ctx, username)
	if errors.Is(err, store.ErrNotFound) {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return LoginResult{}, ErrUnauthorized
	}
	if err != nil {
		return LoginResult{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return LoginResult{}, ErrUnauthorized
	}
	if u.TOTPSecret != "" {
		ticket := randomToken(32)
		c.mfaTickets.put(ticket, mfaTicket{userID: u.ID}, time.Now().Add(mfaTicketTTL))
		return LoginResult{MFATicket: ticket}, nil
	}
	_ = c.store.DeleteExpiredSessions(ctx)
	token, err := c.newSession(ctx, u.ID)
	return LoginResult{User: u, Session: token}, err
}

func (c *Core) Authenticate(ctx context.Context, sessionToken string) (store.User, error) {
	if sessionToken == "" {
		return store.User{}, ErrUnauthorized
	}
	sess, err := c.store.GetSession(ctx, hashToken(sessionToken))
	if errors.Is(err, store.ErrNotFound) {
		return store.User{}, ErrUnauthorized
	}
	if err != nil {
		return store.User{}, err
	}
	u, err := c.store.GetUser(ctx, sess.UserID)
	if errors.Is(err, store.ErrNotFound) {
		return store.User{}, ErrUnauthorized
	}
	return u, err
}

func (c *Core) Logout(ctx context.Context, sessionToken string) error {
	if sessionToken == "" {
		return nil
	}
	return c.store.DeleteSession(ctx, hashToken(sessionToken))
}

func (c *Core) newSession(ctx context.Context, userID string) (string, error) {
	token := randomToken(32)
	err := c.store.CreateSession(ctx, store.Session{
		TokenHash: hashToken(token),
		UserID:    userID,
		ExpiresAt: time.Now().Add(SessionTTL),
	})
	return token, err
}

func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b) // never fails (crypto/rand panics on failure since Go 1.24)
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
