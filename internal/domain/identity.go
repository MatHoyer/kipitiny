package domain

import (
	"context"
	"time"
)

// UserRepository stores users, their project grants, invites, second factors and passkeys.
type UserRepository interface {
	CountUsers(ctx context.Context) (int, error)
	CreateUser(ctx context.Context, u User) (User, error)
	ListUsers(ctx context.Context) ([]User, error)
	GetUser(ctx context.Context, id string) (User, error)
	GetUserByUsername(ctx context.Context, username string) (User, error)
	// UpdateUserAccess sets the role and features.
	UpdateUserAccess(ctx context.Context, id string, role Role, features []string) (User, error)
	// SetUserPreferences replaces the user's preferences.
	SetUserPreferences(ctx context.Context, id string, p Preferences) error
	// DeleteUser removes the user with their sessions, grants and tokens.
	DeleteUser(ctx context.Context, id string) error
	// DeleteUserSessions signs the user out everywhere.
	DeleteUserSessions(ctx context.Context, userID string) error
	ListGrants(ctx context.Context, userID string) ([]ProjectGrant, error)
	// SetGrant creates or replaces a grant; no permissions deletes it.
	SetGrant(ctx context.Context, g ProjectGrant) error
	CreateInvite(ctx context.Context, i Invite) (Invite, error)
	// ListInvites returns the unused, unexpired invites, newest first.
	ListInvites(ctx context.Context) ([]Invite, error)
	// GetInviteByHash returns ErrNotFound for unknown, used or expired invites.
	GetInviteByHash(ctx context.Context, hash string) (Invite, error)
	// AcceptInvite creates u with the invite's role, features and grants,
	// links sso to it when set, and marks the invite used, at once.
	// ErrNotFound if it was used or expired.
	AcceptInvite(ctx context.Context, inviteID string, u User, sso *SSOIdentity) (User, error)
	DeleteInvite(ctx context.Context, id string) error
	// SetPassword updates the hash and revokes all of the user's sessions.
	SetPassword(ctx context.Context, userID, passwordHash string) error
	// SetTOTP turns TOTP on with secret (empty: off) and replaces the
	// recovery codes with codeHashes.
	SetTOTP(ctx context.Context, userID, secret string, codeHashes []string) error
	// UseTOTPStep records step as used; ErrConflict if it (or a later one)
	// already was.
	UseTOTPStep(ctx context.Context, userID string, step int64) error
	SetRecoveryCodes(ctx context.Context, userID string, codeHashes []string) error
	CountRecoveryCodes(ctx context.Context, userID string) (int, error)
	// UseRecoveryCode deletes the code; ErrNotFound if the user has no such code.
	UseRecoveryCode(ctx context.Context, userID, codeHash string) error
	CreatePasskey(ctx context.Context, p Passkey) (Passkey, error)
	ListPasskeys(ctx context.Context, userID string) ([]Passkey, error)
	GetPasskeyByCredentialID(ctx context.Context, credentialID string) (Passkey, error)
	// PasskeyUsed stores the updated credential (sign count) and the use.
	PasskeyUsed(ctx context.Context, id, credential string) error
	DeletePasskey(ctx context.Context, userID, id string) error
}

// SessionRepository stores sign-in sessions and API tokens.
type SessionRepository interface {
	CreateSession(ctx context.Context, s Session) error
	// GetSession returns ErrNotFound for unknown or expired sessions.
	GetSession(ctx context.Context, tokenHash string) (Session, error)
	DeleteSession(ctx context.Context, tokenHash string) error
	DeleteExpiredSessions(ctx context.Context) error
	CreateAPIToken(ctx context.Context, t APIToken) (APIToken, error)
	// ListAPITokens returns userID's tokens, or every token when it's empty.
	ListAPITokens(ctx context.Context, userID string) ([]APIToken, error)
	// GetAPITokenByHash also records the use.
	GetAPITokenByHash(ctx context.Context, hash string) (APIToken, error)
	// UpdateAPIToken saves a token's name, access and hash.
	UpdateAPIToken(ctx context.Context, t APIToken) (APIToken, error)
	DeleteAPIToken(ctx context.Context, id string) error
}

// SSORepository stores the single sign-on providers and the identities users link to them.
type SSORepository interface {
	ListSSOProviders(ctx context.Context) ([]SSOProvider, error)
	GetSSOProvider(ctx context.Context, id string) (SSOProvider, error)
	CreateSSOProvider(ctx context.Context, p SSOProvider) (SSOProvider, error)
	UpdateSSOProvider(ctx context.Context, p SSOProvider) (SSOProvider, error)
	DeleteSSOProvider(ctx context.Context, id string) error
	ListSSOIdentities(ctx context.Context, userID string) ([]SSOIdentity, error)
	// GetSSOIdentity finds the identity a provider's subject is linked to.
	GetSSOIdentity(ctx context.Context, providerID, subject string) (SSOIdentity, error)
	// CreateSSOIdentity returns ErrConflict if the subject or the user is
	// already linked through that provider.
	CreateSSOIdentity(ctx context.Context, i SSOIdentity) (SSOIdentity, error)
	SSOIdentityUsed(ctx context.Context, id, email string) error
	DeleteSSOIdentity(ctx context.Context, userID, id string) error
}

// SecretRepository stores the connections to password managers.
type SecretRepository interface {
	ListSecretConnections(ctx context.Context) ([]SecretConnection, error)
	GetSecretConnection(ctx context.Context, id string) (SecretConnection, error)
	CreateSecretConnection(ctx context.Context, c SecretConnection) (SecretConnection, error)
	SetSecretConnectionToken(ctx context.Context, id, token string) error
	DeleteSecretConnection(ctx context.Context, id string) error
}

type User struct {
	ID           string    `bun:"id,pk" json:"id"`
	Username     string    `bun:"username" json:"username"`
	PasswordHash string    `bun:"password_hash" json:"-"`
	CreatedAt    time.Time `bun:"created_at" json:"createdAt"`
	Role         Role      `bun:"role" json:"role"`
	// Features are the server-level resources a member may see, read-only.
	Features []string `bun:"features" json:"features"`
	// TOTPSecret is the base32 authenticator secret; empty while 2FA is off.
	TOTPSecret   string      `bun:"totp_secret" json:"-"`
	TOTPLastStep int64       `bun:"totp_last_step" json:"-"`
	Preferences  Preferences `bun:"preferences" json:"preferences"`
}

// Preferences are a user's own UI choices; zero values are the defaults.
type Preferences struct {
	// MapView is the map's view when it opens: "" (2D) or "3d".
	MapView string `json:"mapView,omitempty"`
}

type Role string

const (
	RoleAdmin  Role = "admin"  // everything
	RoleMember Role = "member" // only what their grants and features allow
)

// ProjectGrant is what a member may do in one project.
type ProjectGrant struct {
	UserID    string    `bun:"user_id,pk" json:"userId"`
	ProjectID string    `bun:"project_id,pk" json:"projectId"`
	Perms     []string  `bun:"perms" json:"perms"`
	CreatedAt time.Time `bun:"created_at" json:"createdAt"`
}

// Invite is a single-use link that creates an account.
type Invite struct {
	ID        string   `bun:"id,pk" json:"id"`
	TokenHash string   `bun:"token_hash" json:"-"`
	Role      Role     `bun:"role" json:"role"`
	Features  []string `bun:"features" json:"features"`
	// Grants maps project IDs to permissions.
	Grants    map[string][]string `bun:"grants" json:"grants"`
	CreatedBy string              `bun:"created_by" json:"createdBy"`
	// Email is where the link was sent; empty when copied by hand.
	Email     string     `bun:"email" json:"email,omitempty"`
	CreatedAt time.Time  `bun:"created_at" json:"createdAt"`
	ExpiresAt time.Time  `bun:"expires_at" json:"expiresAt"`
	UsedAt    *time.Time `bun:"used_at" json:"usedAt,omitempty"`
}

type Passkey struct {
	ID     string `bun:"id,pk" json:"id"`
	UserID string `bun:"user_id" json:"-"`
	Name   string `bun:"name" json:"name"`
	// CredentialID is the base64url WebAuthn credential ID.
	CredentialID string `bun:"credential_id" json:"-"`
	// Credential is the JSON-encoded webauthn.Credential.
	Credential string     `bun:"credential" json:"-"`
	CreatedAt  time.Time  `bun:"created_at" json:"createdAt"`
	LastUsedAt *time.Time `bun:"last_used_at" json:"lastUsedAt,omitempty"`
}

// PushSubscription is a browser that receives push notifications.
type PushSubscription struct {
	ID     string `bun:"id,pk" json:"id"`
	UserID string `bun:"user_id" json:"-"`
	// Name tells devices apart (Chrome on Linux).
	Name string `bun:"name" json:"name"`
	// Endpoint is the push service URL that reaches the browser.
	Endpoint  string    `bun:"endpoint" json:"endpoint"`
	P256dh    string    `bun:"p256dh" json:"-"`
	Auth      string    `bun:"auth" json:"-"`
	CreatedAt time.Time `bun:"created_at" json:"createdAt"`
}

type Session struct {
	TokenHash string    `bun:"token_hash,pk"`
	UserID    string    `bun:"user_id"`
	CreatedAt time.Time `bun:"created_at"`
	ExpiresAt time.Time `bun:"expires_at"`
}

type APIToken struct {
	ID   string `bun:"id,pk" json:"id"`
	Name string `bun:"name" json:"name"`
	// UserID owns the token, which never does more than its owner may.
	UserID    string `bun:"user_id,nullzero" json:"userId"`
	TokenHash string `bun:"token_hash" json:"-"`
	// Restricted limits the token to Features and Grants (project IDs to
	// permissions), within its owner's; otherwise it has all of them.
	Restricted bool                `bun:"restricted" json:"restricted"`
	Features   []string            `bun:"features" json:"features"`
	Grants     map[string][]string `bun:"grants" json:"grants"`
	CreatedAt  time.Time           `bun:"created_at" json:"createdAt"`
	LastUsedAt *time.Time          `bun:"last_used_at" json:"lastUsedAt,omitempty"`
}

// SSOProvider is an OpenID Connect provider users sign in with.
type SSOProvider struct {
	ID string `bun:"id,pk" json:"id"`
	// Name labels the sign-in button.
	Name         string    `bun:"name" json:"name"`
	Issuer       string    `bun:"issuer" json:"issuer"`
	ClientID     string    `bun:"client_id" json:"clientId"`
	ClientSecret string    `bun:"client_secret" json:"clientSecret"`
	CreatedAt    time.Time `bun:"created_at" json:"createdAt"`
}

// SSOIdentity links a user to their subject at an SSO provider.
type SSOIdentity struct {
	ID         string `bun:"id,pk" json:"id"`
	UserID     string `bun:"user_id" json:"-"`
	ProviderID string `bun:"provider_id" json:"providerId"`
	Subject    string `bun:"subject" json:"-"`
	// Email is the provider's, as of the last sign-in, to tell identities apart.
	Email      string     `bun:"email" json:"email"`
	CreatedAt  time.Time  `bun:"created_at" json:"createdAt"`
	LastUsedAt *time.Time `bun:"last_used_at" json:"lastUsedAt,omitempty"`
}

// SecretConnection is a password manager account the manager is logged
// into. Kind is the provider (internal/domain/secrets); Name is "" for the default
// connection of its kind, which the plain scheme references (pass://), else
// the suffix of its scheme (pass+work://).
type SecretConnection struct {
	ID   string `bun:"id,pk" json:"id"`
	Kind string `bun:"kind" json:"kind"`
	Name string `bun:"name" json:"name"`
	// Token logs in again when the session is lost or expires; never sent
	// to clients.
	Token     string    `bun:"token" json:"-"`
	CreatedAt time.Time `bun:"created_at" json:"createdAt"`
}
