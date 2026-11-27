package identity

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

	"github.com/kipit-labs/kipitiny/internal/domain"
)

var ErrUnauthorized = errors.New("unauthorized")

const (
	SessionTTL        = 30 * 24 * time.Hour
	MinPasswordLength = 10
	// bcrypt ignores everything past 72 bytes; refuse rather than truncate.
	MaxPasswordBytes = 72
)

// dummyHash keeps login timing the same whether or not the user exists.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("kipitiny-dummy-password"), bcrypt.DefaultCost)

// InitAuth creates the one-time token required to create the first
// admin, so whoever reaches a fresh instance first can't claim it without
// access to the server logs.
func (c *Identity) InitAuth(ctx context.Context) error {
	n, err := c.store.CountUsers(ctx)
	if err != nil || n > 0 {
		return err
	}
	token := c.cfg.SetupToken
	if token == "" {
		token = RandomToken(16)
	}
	c.setupMu.Lock()
	c.setupToken = token
	c.setupMu.Unlock()
	c.log.Warn("no admin account yet: open the UI and use this setup token", "setup_token", token)
	return nil
}

// Setup creates the first admin account and returns a session token.
func (c *Identity) Setup(ctx context.Context, setupToken, username, password string) (domain.User, string, error) {
	c.setupMu.Lock()
	defer c.setupMu.Unlock()

	if n, err := c.store.CountUsers(ctx); err != nil {
		return domain.User{}, "", err
	} else if n > 0 {
		return domain.User{}, "", fmt.Errorf("%w: setup already completed", domain.ErrInvalid)
	}
	if c.setupToken == "" || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(setupToken)), []byte(c.setupToken)) != 1 {
		return domain.User{}, "", fmt.Errorf("%w: wrong setup token (see the manager logs)", ErrUnauthorized)
	}
	if !domain.ValidName(username) {
		return domain.User{}, "", fmt.Errorf("%w: username must be lowercase letters, digits and dashes", domain.ErrInvalid)
	}
	if err := validatePassword(password); err != nil {
		return domain.User{}, "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return domain.User{}, "", err
	}
	u, err := c.store.CreateUser(ctx, domain.User{Username: username, PasswordHash: string(hash), Role: domain.RoleAdmin})
	if err != nil {
		return domain.User{}, "", err
	}
	c.setupToken = ""
	token, err := c.newSession(ctx, u.ID)
	return u, token, err
}

// ResetPassword sets a new password and signs the user out everywhere.
func (c *Identity) ResetPassword(ctx context.Context, username, password string) error {
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
	if utf8.RuneCountInString(p) < MinPasswordLength {
		return fmt.Errorf("%w: password must be at least %d characters", domain.ErrInvalid, MinPasswordLength)
	}
	if len(p) > MaxPasswordBytes {
		return fmt.Errorf("%w: password must be at most %d bytes", domain.ErrInvalid, MaxPasswordBytes)
	}
	return nil
}

// LoginResult is a new session, or a ticket for the second step when the
// user has two-factor authentication on.
type LoginResult struct {
	User    domain.User
	Session string
	// MFATicket redeems a TOTP or recovery code (LoginMFA) for a session.
	MFATicket string
}

func (c *Identity) Login(ctx context.Context, username, password string) (LoginResult, error) {
	if c.SSORequired(ctx) {
		return LoginResult{}, ErrSSORequired
	}
	u, err := c.store.GetUserByUsername(ctx, username)
	if errors.Is(err, domain.ErrNotFound) {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return LoginResult{}, ErrUnauthorized
	}
	if err != nil {
		return LoginResult{}, err
	}
	if u.PasswordHash == "" { // single sign-on only
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return LoginResult{}, ErrUnauthorized
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return LoginResult{}, ErrUnauthorized
	}
	if u.TOTPSecret != "" {
		ticket := RandomToken(32)
		c.mfaTickets.Put(ticket, mfaTicket{userID: u.ID}, time.Now().Add(mfaTicketTTL))
		return LoginResult{MFATicket: ticket}, nil
	}
	_ = c.store.DeleteExpiredSessions(ctx)
	token, err := c.newSession(ctx, u.ID)
	return LoginResult{User: u, Session: token}, err
}

func (c *Identity) Authenticate(ctx context.Context, sessionToken string) (domain.User, error) {
	if sessionToken == "" {
		return domain.User{}, ErrUnauthorized
	}
	sess, err := c.store.GetSession(ctx, hashToken(sessionToken))
	if errors.Is(err, domain.ErrNotFound) {
		return domain.User{}, ErrUnauthorized
	}
	if err != nil {
		return domain.User{}, err
	}
	u, err := c.store.GetUser(ctx, sess.UserID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.User{}, ErrUnauthorized
	}
	return u, err
}

// SessionActor is the actor of a signed-in user's request.
func (c *Identity) SessionActor(ctx context.Context, sessionToken string) (Actor, error) {
	u, err := c.Authenticate(ctx, sessionToken)
	if err != nil {
		return Actor{}, err
	}
	a, err := c.UserActor(ctx, u)
	a.Session = hashToken(sessionToken)
	return a, err
}

func (c *Identity) Logout(ctx context.Context, sessionToken string) error {
	if sessionToken == "" {
		return nil
	}
	return c.store.DeleteSession(ctx, hashToken(sessionToken))
}

func (c *Identity) newSession(ctx context.Context, userID string) (string, error) {
	token := RandomToken(32)
	err := c.store.CreateSession(ctx, domain.Session{
		TokenHash: hashToken(token),
		UserID:    userID,
		ExpiresAt: time.Now().Add(SessionTTL),
	})
	return token, err
}

func RandomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b) // never fails (crypto/rand panics on failure since Go 1.24)
	return base64.RawURLEncoding.EncodeToString(b)
}

// RandomPassword is a database password: a RandomToken that never starts
// with "-", which command line tools (the user's own too) read as an option.
func RandomPassword() string {
	for {
		if p := RandomToken(24); p[0] != '-' {
			return p
		}
	}
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
