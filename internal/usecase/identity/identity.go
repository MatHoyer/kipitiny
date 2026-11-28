// Package identity is who may do what: sign-in (passwords, second factors,
// passkeys, SSO), users and invites, API tokens, the permissions every use
// case checks, and the password managers secrets are read from.
package identity

import (
	"context"
	"log/slog"
	"net/http"
	netmail "net/mail"
	"sync"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/domain/secrets"
	"github.com/kipit-labs/kipitiny/internal/usecase/pending"
)

// Mailer sends the invite emails through the email providers set up.
type Mailer interface {
	// CheckMailer fails when no email provider is set up.
	CheckMailer(ctx context.Context) error
	MailInvite(ctx context.Context, to *netmail.Address, i domain.Invite, link string) error
}

type Identity struct {
	cfg   config.Config
	store domain.Store
	log   *slog.Logger
	mail  Mailer

	setupMu    sync.Mutex
	setupToken string // set while no admin exists

	secretKinds    []SecretKind
	secretsMu      sync.Mutex
	secrets        map[string]secrets.Provider // by kind + "\x00" + connection name
	secretListings secretListings
	mfaTickets     pending.Map[mfaTicket]
	totpSetups     pending.Map[string] // user ID -> secret awaiting its first code
	ceremonies     pending.Map[ceremony]
	sso            ssoState
	confirmations  pending.Map[struct{}] // session hash -> confirmed by a fresh sign-in
	ssoFlows       pending.Map[ssoFlow]
	ssoHTTP        *http.Client // nil: a client with ssoTimeout; tests redirect it
}

// New builds the identity use cases; kinds are the password managers
// secrets can be read from (SecretKinds).
func New(cfg config.Config, store domain.Store, kinds []SecretKind, mail Mailer, log *slog.Logger) *Identity {
	return &Identity{cfg: cfg, store: store, log: log, mail: mail, secretKinds: kinds}
}
