package projects

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/domain/gitprovider"
	"github.com/kipit-labs/kipitiny/internal/ids"
)

// GitHubAppInput starts creating a GitHub provider.
type GitHubAppInput struct {
	Name string `json:"name"`
	// BaseURL is a GitHub Enterprise Server; empty for github.com.
	BaseURL string `json:"baseUrl"`
	// Org creates the app under an organization instead of the user.
	Org string `json:"org"`
}

// GitHubAppStart is the form the browser posts to GitHub.
// GitHubAppStart is where to send the browser: a manager page that posts
// the manifest to GitHub (the UI's CSP only allows its own form targets).
type GitHubAppStart struct {
	URL string `json:"url"`
}

// GitHubAppLaunch is the manifest form of a started GitHub App creation.
type GitHubAppLaunch struct {
	URL      string
	Manifest string
	// Origin is the forge's, the only place the form may post to.
	Origin string
}

// StartGitHubApp prepares a GitHub App manifest. managerURL is the
// manager's address as the browser reaches it: GitHub sends the browser
// back there, and push webhooks too when it is public.
func (c *Projects) StartGitHubApp(ctx context.Context, managerURL string, in GitHubAppInput) (GitHubAppStart, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "GitHub"
	}
	base, err := normalizeForgeURL(domain.GitHub, in.BaseURL)
	if err != nil {
		return GitHubAppStart{}, err
	}
	org := strings.TrimSpace(in.Org)
	if strings.ContainsAny(org, "/ ?#") {
		return GitHubAppStart{}, fmt.Errorf("%w: invalid organization", ErrInvalid)
	}
	ps, err := c.store.ListGitProviders(ctx)
	if err != nil {
		return GitHubAppStart{}, err
	}
	for _, p := range ps {
		if p.Name == name {
			return GitHubAppStart{}, gitProviderNameTaken(domain.ErrConflict)
		}
	}
	id := ids.New()
	flow := gitFlow{step: gitStepManifest, providerID: id, name: name, baseURL: base}
	m := gitprovider.ManifestInput{
		// App names are unique on GitHub.
		Name:        "kipitiny-" + strings.ToLower(identity.RandomToken(4)),
		ManagerURL:  managerURL,
		RedirectURL: managerURL + "/api/git-providers/github/created",
		SetupURL:    managerURL + "/api/git-providers/github/installed",
	}
	if publicURL(managerURL) {
		m.WebhookURL = managerURL + "/api/hooks/git-providers/" + id
		flow.webhooks = true
	}
	// The manifest URL carries the state GitHub hands back.
	state := identity.RandomToken(24)
	flow.manifest, flow.manifestURL = gitprovider.Manifest(m), gitprovider.ManifestURL(base, org, state)
	c.gitFlows.Put(state, flow, time.Now().Add(gitFlowTTL))
	return GitHubAppStart{URL: managerURL + "/api/git-providers/github/launch?state=" + url.QueryEscape(state)}, nil
}

// LaunchGitHubApp returns the manifest form of a started creation, without
// consuming it: GitHub sends the same state back once the app exists.
func (c *Projects) LaunchGitHubApp(state string) (GitHubAppLaunch, error) {
	f, ok := c.gitFlows.Peek(state)
	if !ok || f.step != gitStepManifest || f.manifestURL == "" {
		return GitHubAppLaunch{}, fmt.Errorf("%w: this connection expired or was already used; start again", ErrInvalid)
	}
	u, err := url.Parse(f.baseURL)
	if err != nil {
		return GitHubAppLaunch{}, err
	}
	return GitHubAppLaunch{URL: f.manifestURL, Manifest: f.manifest, Origin: u.Scheme + "://" + u.Host}, nil
}

// FinishGitHubApp saves the app GitHub created and returns where to
// install it.
func (c *Projects) FinishGitHubApp(ctx context.Context, code, state string) (string, error) {
	flow, err := c.takeGitFlow(state, gitStepManifest)
	if err != nil {
		return "", err
	}
	app, err := c.forges.ConvertManifest(ctx, flow.baseURL, code)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	p := domain.GitProvider{
		ID: flow.providerID, Kind: domain.GitHub, Name: flow.name, BaseURL: flow.baseURL,
		AppID: app.ID, AppSlug: app.Slug, PrivateKey: app.PEM,
		ClientID: app.ClientID, ClientSecret: app.ClientSecret,
	}
	if flow.webhooks {
		p.WebhookSecret = app.WebhookSecret
	}
	if _, err := c.store.CreateGitProvider(ctx, p); err != nil {
		return "", gitProviderNameTaken(err)
	}
	return gitprovider.InstallURL(p.BaseURL, p.AppSlug, c.putGitFlow(gitFlow{step: gitStepInstall, providerID: p.ID})), nil
}

// GitHubAppInstalled records the installation GitHub redirected with and
// returns its provider. Without a state (repositories changed on GitHub),
// it only confirms a known installation.
func (c *Projects) GitHubAppInstalled(ctx context.Context, installationID int64, state string) (string, error) {
	if state == "" {
		ps, err := c.store.ListGitProviders(ctx)
		if err != nil {
			return "", err
		}
		for _, p := range ps {
			if p.Kind == domain.GitHub && p.InstallationID == installationID {
				return p.ID, nil
			}
		}
		return "", fmt.Errorf("%w: unknown installation; connect it from Settings › Git providers", ErrInvalid)
	}
	flow, err := c.takeGitFlow(state, gitStepInstall)
	if err != nil {
		return "", err
	}
	p, err := c.store.GetGitProvider(ctx, flow.providerID)
	if err != nil {
		return "", err
	}
	// Asked as the app: an installation of another app is refused.
	account, err := c.forges.InstallationAccount(ctx, p, installationID)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	p.InstallationID, p.Account = installationID, account
	if _, err := c.store.UpdateGitProvider(ctx, p); err != nil {
		return "", err
	}
	c.gitTokens.mu.Lock()
	delete(c.gitTokens.github, p.ID)
	c.gitTokens.mu.Unlock()
	return p.ID, nil
}

// AuthorizeGitProvider returns where the browser connects the provider:
// the OAuth consent of GitLab or Gitea, or the GitHub App's installation
// (to install it again or change its repositories).
func (c *Projects) AuthorizeGitProvider(ctx context.Context, id, managerURL string) (string, error) {
	p, err := c.store.GetGitProvider(ctx, id)
	if err != nil {
		return "", err
	}
	if p.Kind == domain.GitHub {
		return gitprovider.InstallURL(p.BaseURL, p.AppSlug, c.putGitFlow(gitFlow{step: gitStepInstall, providerID: p.ID})), nil
	}
	p.RedirectURL = managerURL + "/api/git-providers/oauth/callback"
	if _, err := c.store.UpdateGitProvider(ctx, p); err != nil {
		return "", err
	}
	state := c.putGitFlow(gitFlow{step: gitStepOAuth, providerID: p.ID})
	return c.forges.AuthCodeURL(p, state), nil
}

// FinishGitProviderOAuth exchanges the code GitLab or Gitea redirected
// with for tokens and returns the provider.
func (c *Projects) FinishGitProviderOAuth(ctx context.Context, code, state string) (string, error) {
	flow, err := c.takeGitFlow(state, gitStepOAuth)
	if err != nil {
		return "", err
	}
	c.gitTokens.mu.Lock()
	defer c.gitTokens.mu.Unlock()
	p, err := c.store.GetGitProvider(ctx, flow.providerID)
	if err != nil {
		return "", err
	}
	tok, err := c.forges.Exchange(ctx, p, code)
	if err != nil {
		return "", fmt.Errorf("%w: %s refused the authorization: %s", ErrInvalid, p.BaseURL, err.Error())
	}
	account, err := c.forges.Account(ctx, p.Kind, p.BaseURL, tok.AccessToken)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	p.Account = account
	setOAuthToken(&p, tok)
	_, err = c.store.UpdateGitProvider(ctx, p)
	return p.ID, err
}

func setOAuthToken(p *domain.GitProvider, tok gitprovider.Token) {
	p.AccessToken = tok.AccessToken
	if tok.RefreshToken != "" {
		p.RefreshToken = tok.RefreshToken
	}
	p.TokenExpiresAt = nil
	if !tok.Expiry.IsZero() {
		exp := tok.Expiry
		p.TokenExpiresAt = &exp
	}
}

func (c *Projects) putGitFlow(f gitFlow) string {
	state := identity.RandomToken(24)
	c.gitFlows.Put(state, f, time.Now().Add(gitFlowTTL))
	return state
}

func (c *Projects) takeGitFlow(state, step string) (gitFlow, error) {
	f, _, ok := c.gitFlows.Take(state)
	if !ok || f.step != step {
		return gitFlow{}, fmt.Errorf("%w: this connection expired or was already used; start again", ErrInvalid)
	}
	return f, nil
}
