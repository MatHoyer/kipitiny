package gitprovider

import (
	"context"
	"time"

	"golang.org/x/oauth2"

	"github.com/kipit-labs/kipitiny/internal/domain"
	gp "github.com/kipit-labs/kipitiny/internal/domain/gitprovider"
)

// Client is the forges' APIs, as projects.GitProviders.
type Client struct{}

func (Client) Repos(ctx context.Context, kind domain.GitProviderKind, baseURL, token string) ([]gp.Repo, error) {
	return Repos(ctx, kind, baseURL, token)
}

func (Client) Branches(ctx context.Context, kind domain.GitProviderKind, baseURL, token, fullName string) ([]string, error) {
	return Branches(ctx, kind, baseURL, token, fullName)
}

func (Client) Account(ctx context.Context, kind domain.GitProviderKind, baseURL, token string) (string, error) {
	return Account(ctx, kind, baseURL, token)
}

func (Client) ConvertManifest(ctx context.Context, baseURL, code string) (gp.App, error) {
	return ConvertManifest(ctx, baseURL, code)
}

func (Client) InstallationToken(ctx context.Context, p domain.GitProvider) (string, time.Time, error) {
	return InstallationToken(ctx, p)
}

func (Client) InstallationAccount(ctx context.Context, p domain.GitProvider, installationID int64) (string, error) {
	return InstallationAccount(ctx, p, installationID)
}

// AuthCodeURL is where the browser authorizes p's OAuth application.
func (Client) AuthCodeURL(p domain.GitProvider, state string) string {
	return OAuthConfig(p).AuthCodeURL(state)
}

// Exchange trades the code the forge redirected with for tokens.
func (Client) Exchange(ctx context.Context, p domain.GitProvider, code string) (gp.Token, error) {
	tok, err := OAuthConfig(p).Exchange(ctx, code)
	if err != nil {
		return gp.Token{}, err
	}
	return token(tok), nil
}

// Refresh gets a new access token with p's refresh token.
func (Client) Refresh(ctx context.Context, p domain.GitProvider) (gp.Token, error) {
	tok, err := OAuthConfig(p).TokenSource(ctx, &oauth2.Token{RefreshToken: p.RefreshToken}).Token()
	if err != nil {
		return gp.Token{}, err
	}
	return token(tok), nil
}

func token(t *oauth2.Token) gp.Token {
	return gp.Token{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken, Expiry: t.Expiry}
}
