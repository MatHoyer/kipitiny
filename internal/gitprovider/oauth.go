package gitprovider

import (
	"golang.org/x/oauth2"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// GitLab and Gitea providers are OAuth applications the user registers on
// the forge, with the manager's callback as redirect URI. The authorization
// gives an access token (2 hours on GitLab) and a refresh token, which the
// core keeps refreshed.

// OAuthConfig is p's OAuth application.
func OAuthConfig(p store.GitProvider) *oauth2.Config {
	c := &oauth2.Config{ClientID: p.ClientID, ClientSecret: p.ClientSecret, RedirectURL: p.RedirectURL}
	switch p.Kind {
	case store.GitLab:
		c.Endpoint = oauth2.Endpoint{AuthURL: p.BaseURL + "/oauth/authorize", TokenURL: p.BaseURL + "/oauth/token"}
		c.Scopes = []string{"read_api", "read_repository"}
	default:
		c.Endpoint = oauth2.Endpoint{AuthURL: p.BaseURL + "/login/oauth/authorize", TokenURL: p.BaseURL + "/login/oauth/access_token"}
		c.Scopes = []string{"read:repository", "read:user"}
	}
	return c
}

// OAuthScopes is what the application must be allowed, as the forge's
// settings name it.
func OAuthScopes(kind store.GitProviderKind) []string {
	return OAuthConfig(store.GitProvider{Kind: kind}).Scopes
}
