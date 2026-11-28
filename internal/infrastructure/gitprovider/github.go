package gitprovider

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/kipit-labs/kipitiny/internal/domain"
	gp "github.com/kipit-labs/kipitiny/internal/domain/gitprovider"
)

// A GitHub provider is a GitHub App the manager creates from a manifest
// (https://docs.github.com/en/apps/sharing-github-apps/registering-a-github-app-from-a-manifest):
// the browser posts the manifest to GitHub, GitHub redirects back with a
// code the manager exchanges for the app's credentials, then the app is
// installed on an account. Its private key mints installation tokens,
// valid an hour, that read the repositories it was given.

// ConvertManifest exchanges the code GitHub redirected with for the app.
func ConvertManifest(ctx context.Context, baseURL, code string) (gp.App, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		APIURL(domain.GitHub, baseURL)+"/app-manifests/"+url.PathEscape(code)+"/conversions", nil)
	if err != nil {
		return gp.App{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	var app gp.App
	if err := do(req, &app); err != nil {
		return gp.App{}, fmt.Errorf("create the GitHub App: %w", err)
	}
	return app, nil
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
func InstallationToken(ctx context.Context, p domain.GitProvider) (string, time.Time, error) {
	var body struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	u := fmt.Sprintf("%s/app/installations/%d/access_tokens", APIURL(domain.GitHub, p.BaseURL), p.InstallationID)
	if err := appRequest(ctx, http.MethodPost, u, p.AppID, p.PrivateKey, &body); err != nil {
		return "", time.Time{}, fmt.Errorf("GitHub App installation token: %w", err)
	}
	return body.Token, body.ExpiresAt, nil
}

// InstallationAccount is the user or organization the app is installed on.
func InstallationAccount(ctx context.Context, p domain.GitProvider, installationID int64) (string, error) {
	var body struct {
		Account struct {
			Login string `json:"login"`
		} `json:"account"`
	}
	u := fmt.Sprintf("%s/app/installations/%d", APIURL(domain.GitHub, p.BaseURL), installationID)
	if err := appRequest(ctx, http.MethodGet, u, p.AppID, p.PrivateKey, &body); err != nil {
		return "", fmt.Errorf("GitHub App installation: %w", err)
	}
	return body.Account.Login, nil
}
