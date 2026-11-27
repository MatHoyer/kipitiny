package core

import (
	"context"
	"errors"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"
	"github.com/kipit-labs/kipitiny/internal/usecase/projects"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

type AuthState struct {
	SetupRequired bool         `json:"setupRequired"`
	User          *domain.User `json:"user,omitempty"`
	// SSO lists the providers to sign in with; with SSORequired, the only
	// way to sign in.
	SSO         []identity.SSOButton `json:"sso"`
	SSORequired bool                 `json:"ssoRequired"`
	Branding    projects.Branding    `json:"branding"`
}

func (c *Core) AuthState(ctx context.Context, sessionToken string) (AuthState, error) {
	n, err := c.store.CountUsers(ctx)
	if err != nil {
		return AuthState{}, err
	}
	if n == 0 {
		return AuthState{SetupRequired: true, SSO: []identity.SSOButton{}, Branding: c.Branding(ctx)}, nil
	}
	u, err := c.Authenticate(ctx, sessionToken)
	if errors.Is(err, identity.ErrUnauthorized) {
		return AuthState{SSO: c.SSOButtons(ctx), SSORequired: c.SSORequired(ctx), Branding: c.Branding(ctx)}, nil
	}
	if err != nil {
		return AuthState{}, err
	}
	return AuthState{User: &u, SSO: []identity.SSOButton{}, Branding: c.Branding(ctx)}, nil
}
