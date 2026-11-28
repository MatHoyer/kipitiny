// Package gitprovider holds what the manager knows of git forges (GitHub,
// GitLab, Gitea): repositories, GitHub App manifests, the URLs the browser
// goes through, and access tokens. The forges' APIs are in
// infrastructure/gitprovider (projects.GitProviders).
package gitprovider

import (
	"encoding/json"
	"net/url"
	"time"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

// Repo is a repository a provider can read.
type Repo struct {
	// FullName is owner/name (group/subgroup/name on GitLab).
	FullName      string `json:"fullName"`
	CloneURL      string `json:"cloneUrl"`
	DefaultBranch string `json:"defaultBranch"`
	Private       bool   `json:"private"`
}

// DefaultBaseURL is the hosted forge of a kind.
func DefaultBaseURL(kind domain.GitProviderKind) string {
	switch kind {
	case domain.GitHub:
		return "https://github.com"
	case domain.GitLab:
		return "https://gitlab.com"
	}
	return ""
}

// CloneUsername goes with the token in HTTPS clone credentials.
func CloneUsername(kind domain.GitProviderKind) string {
	if kind == domain.GitHub {
		return "x-access-token"
	}
	return "oauth2"
}

// ManifestInput describes the app to create.
type ManifestInput struct {
	Name string
	// ManagerURL is the manager's address as the browser reaches it.
	ManagerURL string
	// RedirectURL receives the manifest code, SetupURL the installation.
	RedirectURL, SetupURL string
	// WebhookURL receives push events; empty when GitHub can't reach the
	// manager (polling only).
	WebhookURL string
}

// Manifest is the JSON the browser posts to GitHub.
func Manifest(in ManifestInput) string {
	m := map[string]any{
		"name":                in.Name,
		"url":                 in.ManagerURL,
		"redirect_url":        in.RedirectURL,
		"setup_url":           in.SetupURL,
		"setup_on_update":     true,
		"public":              false,
		"default_permissions": map[string]string{"contents": "read", "metadata": "read"},
	}
	if in.WebhookURL != "" {
		m["hook_attributes"] = map[string]any{"url": in.WebhookURL, "active": true}
		m["default_events"] = []string{"push"}
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// ManifestURL is where the manifest is posted: a personal app, or an
// organization's when org is set.
func ManifestURL(baseURL, org, state string) string {
	u := baseURL + "/settings/apps/new"
	if org != "" {
		u = baseURL + "/organizations/" + url.PathEscape(org) + "/settings/apps/new"
	}
	return u + "?state=" + url.QueryEscape(state)
}

// App is a created GitHub App's credentials.
type App struct {
	ID            int64  `json:"id"`
	Slug          string `json:"slug"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	WebhookSecret string `json:"webhook_secret"`
	PEM           string `json:"pem"`
	Owner         struct {
		Login string `json:"login"`
	} `json:"owner"`
}

// InstallURL lets the user install the app on an account and pick its
// repositories.
func InstallURL(baseURL, slug, state string) string {
	return baseURL + "/apps/" + url.PathEscape(slug) + "/installations/new?state=" + url.QueryEscape(state)
}

// Token is an OAuth or installation access token.
type Token struct {
	AccessToken string
	// RefreshToken is "" when the forge didn't send a new one.
	RefreshToken string
	// Expiry is zero when the token doesn't expire.
	Expiry time.Time
}
