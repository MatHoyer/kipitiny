package gitprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// A GitHub provider is a GitHub App the manager creates from a manifest
// (https://docs.github.com/en/apps/sharing-github-apps/registering-a-github-app-from-a-manifest):
// the browser posts the manifest to GitHub, GitHub redirects back with a
// code the manager exchanges for the app's credentials, then the app is
// installed on an account. Its private key mints installation tokens,
// valid an hour, that read the repositories it was given.

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

// ConvertManifest exchanges the code GitHub redirected with for the app.
func ConvertManifest(ctx context.Context, baseURL, code string) (App, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		APIURL(store.GitHub, baseURL)+"/app-manifests/"+url.PathEscape(code)+"/conversions", nil)
	if err != nil {
		return App{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	var app App
	if err := do(req, &app); err != nil {
		return App{}, fmt.Errorf("create the GitHub App: %w", err)
	}
	return app, nil
}

// InstallURL lets the user install the app on an account and pick its
// repositories.
func InstallURL(baseURL, slug, state string) string {
	return baseURL + "/apps/" + url.PathEscape(slug) + "/installations/new?state=" + url.QueryEscape(state)
}

// appJWT authenticates as the app itself (10 minutes at most).
func appJWT(appID int64, pemKey string) (string, error) {
	key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(pemKey))
	if err != nil {
		return "", fmt.Errorf("GitHub App private key: %w", err)
	}
	now := time.Now()
	return jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.RegisteredClaims{
		Issuer:    strconv.FormatInt(appID, 10),
		IssuedAt:  jwt.NewNumericDate(now.Add(-time.Minute)), // clock drift
		ExpiresAt: jwt.NewNumericDate(now.Add(9 * time.Minute)),
	}).SignedString(key)
}

func appRequest(ctx context.Context, method, u string, appID int64, pemKey string, out any) error {
	token, err := appJWT(appID, pemKey)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(nil))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	return do(req, out)
}

// InstallationToken mints a token reading the installation's repositories.
func InstallationToken(ctx context.Context, p store.GitProvider) (string, time.Time, error) {
	var body struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	u := fmt.Sprintf("%s/app/installations/%d/access_tokens", APIURL(store.GitHub, p.BaseURL), p.InstallationID)
	if err := appRequest(ctx, http.MethodPost, u, p.AppID, p.PrivateKey, &body); err != nil {
		return "", time.Time{}, fmt.Errorf("GitHub App installation token: %w", err)
	}
	return body.Token, body.ExpiresAt, nil
}

// InstallationAccount is the user or organization the app is installed on.
func InstallationAccount(ctx context.Context, p store.GitProvider, installationID int64) (string, error) {
	var body struct {
		Account struct {
			Login string `json:"login"`
		} `json:"account"`
	}
	u := fmt.Sprintf("%s/app/installations/%d", APIURL(store.GitHub, p.BaseURL), installationID)
	if err := appRequest(ctx, http.MethodGet, u, p.AppID, p.PrivateKey, &body); err != nil {
		return "", fmt.Errorf("GitHub App installation: %w", err)
	}
	return body.Account.Login, nil
}
