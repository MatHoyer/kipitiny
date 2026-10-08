package core

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/MatHoyer/kipitiny/internal/gitprovider"
	"github.com/MatHoyer/kipitiny/internal/ids"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// Git providers give the manager read access to private repositories:
// a GitHub App it creates and the user installs, or a GitLab or Gitea OAuth
// application the user authorizes. Projects pick one to follow a
// repository; no token is pasted anywhere.

// gitFlowTTL bounds a connection started in the browser.
const gitFlowTTL = time.Hour

// Steps of a connection in the browser, carried by its state.
const (
	gitStepManifest = "manifest" // GitHub redirects with the new app's code
	gitStepInstall  = "install"  // GitHub redirects after installing the app
	gitStepOAuth    = "oauth"    // GitLab/Gitea redirect with the code
)

type gitFlow struct {
	step       string
	providerID string
	// Manifest step: the provider to create.
	name, baseURL string
	webhooks      bool
}

// gitTokens caches GitHub installation tokens and serializes OAuth
// refreshes (a refresh token is single use).
type gitTokens struct {
	mu     sync.Mutex
	github map[string]oauth2.Token // provider ID -> installation token
}

// GitProviderView is a provider with its credentials masked.
type GitProviderView struct {
	store.GitProvider
	Connected bool `json:"connected"`
	// Webhooks reports that pushes reach the manager through the app, for
	// every repository it reads.
	Webhooks bool `json:"webhooks"`
}

func gitProviderView(p store.GitProvider) GitProviderView {
	v := GitProviderView{GitProvider: p, Connected: p.Connected(), Webhooks: p.WebhookSecret != ""}
	if v.ClientSecret != "" {
		v.ClientSecret = SecretMask
	}
	return v
}

func (c *Core) ListGitProviders(ctx context.Context) ([]GitProviderView, error) {
	ps, err := c.store.ListGitProviders(ctx)
	out := make([]GitProviderView, len(ps))
	for i, p := range ps {
		out[i] = gitProviderView(p)
	}
	return out, err
}

// GitProviderInput creates or edits a GitLab or Gitea provider (a GitHub
// one is created with StartGitHubApp; only its name can change).
type GitProviderInput struct {
	// Kind is set on creation only.
	Kind store.GitProviderKind `json:"kind"`
	Name string                `json:"name"`
	// BaseURL defaults to the hosted forge (gitlab.com).
	BaseURL  string `json:"baseUrl"`
	ClientID string `json:"clientId"`
	// ClientSecret equal to SecretMask keeps the stored value on update.
	ClientSecret string `json:"clientSecret"`
}

// CreateGitProvider saves an OAuth application; AuthorizeGitProvider then
// connects it.
func (c *Core) CreateGitProvider(ctx context.Context, in GitProviderInput) (GitProviderView, error) {
	if in.Kind != store.GitLab && in.Kind != store.Gitea {
		return GitProviderView{}, fmt.Errorf("%w: kind must be gitlab or gitea (GitHub providers are created as an app)", ErrInvalid)
	}
	p := store.GitProvider{Kind: in.Kind}
	if err := applyGitProviderInput(&p, in); err != nil {
		return GitProviderView{}, err
	}
	p, err := c.store.CreateGitProvider(ctx, p)
	return gitProviderView(p), gitProviderNameTaken(err)
}

func (c *Core) UpdateGitProvider(ctx context.Context, id string, in GitProviderInput) (GitProviderView, error) {
	p, err := c.store.GetGitProvider(ctx, id)
	if err != nil {
		return GitProviderView{}, err
	}
	if p.Kind == store.GitHub {
		p.Name = strings.TrimSpace(in.Name)
		if p.Name == "" {
			return GitProviderView{}, fmt.Errorf("%w: a name is required", ErrInvalid)
		}
	} else {
		baseURL, clientID := p.BaseURL, p.ClientID
		if err := applyGitProviderInput(&p, in); err != nil {
			return GitProviderView{}, err
		}
		if p.BaseURL != baseURL || p.ClientID != clientID {
			// Another forge or application: the authorization doesn't carry over.
			p.Account, p.AccessToken, p.RefreshToken, p.TokenExpiresAt = "", "", "", nil
		}
	}
	p, err = c.store.UpdateGitProvider(ctx, p)
	return gitProviderView(p), gitProviderNameTaken(err)
}

func applyGitProviderInput(p *store.GitProvider, in GitProviderInput) error {
	p.Name = strings.TrimSpace(in.Name)
	p.ClientID = strings.TrimSpace(in.ClientID)
	if in.ClientSecret != SecretMask {
		p.ClientSecret = strings.TrimSpace(in.ClientSecret)
	}
	base, err := normalizeForgeURL(p.Kind, in.BaseURL)
	if err != nil {
		return err
	}
	p.BaseURL = base
	if p.Name == "" || p.ClientID == "" || p.ClientSecret == "" {
		return fmt.Errorf("%w: name, application ID and secret are required", ErrInvalid)
	}
	return nil
}

// normalizeForgeURL is a forge's https root URL; empty is the hosted forge.
func normalizeForgeURL(kind store.GitProviderKind, raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		raw = gitprovider.DefaultBaseURL(kind)
	}
	if raw == "" {
		return "", fmt.Errorf("%w: the server's URL is required", ErrInvalid)
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" {
		return "", fmt.Errorf("%w: the server must be an https URL, e.g. https://git.example.com", ErrInvalid)
	}
	return u.Scheme + "://" + strings.ToLower(u.Host) + strings.TrimRight(u.Path, "/"), nil
}

func gitProviderNameTaken(err error) error {
	if errors.Is(err, store.ErrConflict) {
		return fmt.Errorf("%w: a git provider already has this name", ErrInvalid)
	}
	return err
}

func (c *Core) DeleteGitProvider(ctx context.Context, id string) error {
	err := c.store.DeleteGitProvider(ctx, id)
	if errors.Is(err, store.ErrConflict) {
		return fmt.Errorf("%w: %v; unlink them first", ErrInvalid, err)
	}
	c.gitTokens.mu.Lock()
	delete(c.gitTokens.github, id)
	c.gitTokens.mu.Unlock()
	return err
}

// TestGitProvider checks the provider can still list repositories.
func (c *Core) TestGitProvider(ctx context.Context, id string) error {
	_, err := c.GitProviderRepos(ctx, id)
	return err
}

// GitHubAppInput starts creating a GitHub provider.
type GitHubAppInput struct {
	Name string `json:"name"`
	// BaseURL is a GitHub Enterprise Server; empty for github.com.
	BaseURL string `json:"baseUrl"`
	// Org creates the app under an organization instead of the user.
	Org string `json:"org"`
}

// GitHubAppStart is the form the browser posts to GitHub.
type GitHubAppStart struct {
	URL      string `json:"url"`
	Manifest string `json:"manifest"`
}

// StartGitHubApp prepares a GitHub App manifest. managerURL is the
// manager's address as the browser reaches it: GitHub sends the browser
// back there, and push webhooks too when it is public.
func (c *Core) StartGitHubApp(ctx context.Context, managerURL string, in GitHubAppInput) (GitHubAppStart, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "GitHub"
	}
	base, err := normalizeForgeURL(store.GitHub, in.BaseURL)
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
			return GitHubAppStart{}, gitProviderNameTaken(store.ErrConflict)
		}
	}
	id := ids.New()
	flow := gitFlow{step: gitStepManifest, providerID: id, name: name, baseURL: base}
	m := gitprovider.ManifestInput{
		// App names are unique on GitHub.
		Name:        "kipitiny-" + strings.ToLower(randomToken(4)),
		ManagerURL:  managerURL,
		RedirectURL: managerURL + "/api/git-providers/github/created",
		SetupURL:    managerURL + "/api/git-providers/github/installed",
	}
	if publicURL(managerURL) {
		m.WebhookURL = managerURL + "/api/hooks/git-providers/" + id
		flow.webhooks = true
	}
	state := c.putGitFlow(flow)
	return GitHubAppStart{URL: gitprovider.ManifestURL(base, org, state), Manifest: gitprovider.Manifest(m)}, nil
}

// FinishGitHubApp saves the app GitHub created and returns where to
// install it.
func (c *Core) FinishGitHubApp(ctx context.Context, code, state string) (string, error) {
	flow, err := c.takeGitFlow(state, gitStepManifest)
	if err != nil {
		return "", err
	}
	app, err := gitprovider.ConvertManifest(ctx, flow.baseURL, code)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	p := store.GitProvider{
		ID: flow.providerID, Kind: store.GitHub, Name: flow.name, BaseURL: flow.baseURL,
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
func (c *Core) GitHubAppInstalled(ctx context.Context, installationID int64, state string) (string, error) {
	if state == "" {
		ps, err := c.store.ListGitProviders(ctx)
		if err != nil {
			return "", err
		}
		for _, p := range ps {
			if p.Kind == store.GitHub && p.InstallationID == installationID {
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
	account, err := gitprovider.InstallationAccount(ctx, p, installationID)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalid, err)
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
func (c *Core) AuthorizeGitProvider(ctx context.Context, id, managerURL string) (string, error) {
	p, err := c.store.GetGitProvider(ctx, id)
	if err != nil {
		return "", err
	}
	if p.Kind == store.GitHub {
		return gitprovider.InstallURL(p.BaseURL, p.AppSlug, c.putGitFlow(gitFlow{step: gitStepInstall, providerID: p.ID})), nil
	}
	p.RedirectURL = managerURL + "/api/git-providers/oauth/callback"
	if _, err := c.store.UpdateGitProvider(ctx, p); err != nil {
		return "", err
	}
	state := c.putGitFlow(gitFlow{step: gitStepOAuth, providerID: p.ID})
	return gitprovider.OAuthConfig(p).AuthCodeURL(state), nil
}

// FinishGitProviderOAuth exchanges the code GitLab or Gitea redirected
// with for tokens and returns the provider.
func (c *Core) FinishGitProviderOAuth(ctx context.Context, code, state string) (string, error) {
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
	tok, err := gitprovider.OAuthConfig(p).Exchange(ctx, code)
	if err != nil {
		return "", fmt.Errorf("%w: %s refused the authorization: %v", ErrInvalid, p.BaseURL, err)
	}
	account, err := gitprovider.Account(ctx, p.Kind, p.BaseURL, tok.AccessToken)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	p.Account = account
	setOAuthToken(&p, tok)
	_, err = c.store.UpdateGitProvider(ctx, p)
	return p.ID, err
}

func setOAuthToken(p *store.GitProvider, tok *oauth2.Token) {
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

func (c *Core) putGitFlow(f gitFlow) string {
	state := randomToken(24)
	c.gitFlows.put(state, f, time.Now().Add(gitFlowTTL))
	return state
}

func (c *Core) takeGitFlow(state, step string) (gitFlow, error) {
	f, _, ok := c.gitFlows.take(state)
	if !ok || f.step != step {
		return gitFlow{}, fmt.Errorf("%w: this connection expired or was already used; start again", ErrInvalid)
	}
	return f, nil
}

// publicURL reports whether GitHub could deliver webhooks there.
func publicURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		return !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified()
	}
	return strings.Contains(host, ".") && !strings.HasSuffix(host, ".local") && !strings.HasSuffix(host, ".localhost") &&
		!strings.HasSuffix(host, ".internal") && !strings.HasSuffix(host, ".lan")
}

// GitProviderRepos lists the repositories a provider can read.
func (c *Core) GitProviderRepos(ctx context.Context, id string) ([]gitprovider.Repo, error) {
	p, token, err := c.gitProviderToken(ctx, id)
	if err != nil {
		return nil, err
	}
	repos, err := gitprovider.Repos(ctx, p.Kind, p.BaseURL, token)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrInvalid, p.Name, err)
	}
	return repos, nil
}

// GitProviderBranches lists a repository's branches.
func (c *Core) GitProviderBranches(ctx context.Context, id, repo string) ([]string, error) {
	if repo == "" || strings.Contains(repo, "..") {
		return nil, fmt.Errorf("%w: invalid repository", ErrInvalid)
	}
	p, token, err := c.gitProviderToken(ctx, id)
	if err != nil {
		return nil, err
	}
	bs, err := gitprovider.Branches(ctx, p.Kind, p.BaseURL, token, repo)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrInvalid, repo, err)
	}
	return bs, nil
}

// gitProviderToken returns a token reading the provider's repositories:
// a GitHub installation token (minted, cached until shortly before it
// expires) or the OAuth access token (refreshed when about to expire).
func (c *Core) gitProviderToken(ctx context.Context, id string) (store.GitProvider, string, error) {
	c.gitTokens.mu.Lock()
	defer c.gitTokens.mu.Unlock()
	p, err := c.store.GetGitProvider(ctx, id)
	if err != nil {
		return store.GitProvider{}, "", err
	}
	if !p.Connected() {
		return p, "", fmt.Errorf("%w: git provider %s isn't connected", ErrInvalid, p.Name)
	}
	soon := time.Now().Add(5 * time.Minute)
	if p.Kind == store.GitHub {
		if t, ok := c.gitTokens.github[id]; ok && t.Expiry.After(soon) {
			return p, t.AccessToken, nil
		}
		token, exp, err := gitprovider.InstallationToken(ctx, p)
		if err != nil {
			return p, "", fmt.Errorf("%w: %v (is the app still installed?)", ErrInvalid, err)
		}
		if c.gitTokens.github == nil {
			c.gitTokens.github = map[string]oauth2.Token{}
		}
		c.gitTokens.github[id] = oauth2.Token{AccessToken: token, Expiry: exp}
		return p, token, nil
	}
	if p.TokenExpiresAt == nil || p.TokenExpiresAt.After(soon) {
		return p, p.AccessToken, nil
	}
	tok, err := gitprovider.OAuthConfig(p).TokenSource(ctx, &oauth2.Token{RefreshToken: p.RefreshToken}).Token()
	if err != nil {
		return p, "", fmt.Errorf("%w: %s: the authorization expired or was revoked; connect it again (%v)", ErrInvalid, p.Name, err)
	}
	setOAuthToken(&p, tok)
	if _, err := c.store.UpdateGitProvider(ctx, p); err != nil {
		return p, "", err
	}
	return p, p.AccessToken, nil
}

// checkProviderRepo refuses a repository on another host than the
// provider's: its token must never be sent elsewhere.
func checkProviderRepo(p store.GitProvider, repoURL string) error {
	u, err := url.Parse(repoURL)
	b, berr := url.Parse(p.BaseURL)
	if err != nil || berr != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, b.Host) {
		return fmt.Errorf("%w: %s only reads repositories on %s", ErrInvalid, p.Name, p.BaseURL)
	}
	return nil
}

// HandleGitProviderWebhook authenticates a GitHub App's push webhook and
// syncs the projects following that repository and branch.
func (c *Core) HandleGitProviderWebhook(ctx context.Context, providerID string, header func(string) string, body []byte) error {
	p, err := c.store.GetGitProvider(ctx, providerID)
	if err != nil || p.WebhookSecret == "" {
		return ErrUnauthorized // don't reveal which IDs exist
	}
	sig, ok := strings.CutPrefix(header("X-Hub-Signature-256"), "sha256=")
	mac := hmac.New(sha256.New, []byte(p.WebhookSecret))
	mac.Write(body)
	if !ok || !hmac.Equal([]byte(sig), []byte(hex.EncodeToString(mac.Sum(nil)))) {
		return ErrUnauthorized
	}
	if header("X-GitHub-Event") != "push" {
		return ErrIgnored
	}
	var payload struct {
		Ref        string `json:"ref"`
		Repository struct {
			CloneURL string `json:"clone_url"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("%w: invalid JSON payload", ErrInvalid)
	}
	links, err := c.store.ListProjectGit(ctx)
	if err != nil {
		return err
	}
	kicked := false
	for _, g := range links {
		if g.ProviderID == providerID && sameRepo(g.RepoURL, payload.Repository.CloneURL) && payload.Ref == "refs/heads/"+g.Branch {
			c.kickGit(g.ProjectID)
			kicked = true
		}
	}
	if !kicked {
		return ErrIgnored
	}
	return nil
}

func sameRepo(a, b string) bool {
	norm := func(s string) string {
		return strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), "/"), ".git")
	}
	return a != "" && norm(a) == norm(b)
}
