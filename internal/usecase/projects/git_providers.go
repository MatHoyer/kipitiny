package projects

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

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"golang.org/x/oauth2"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/gitprovider"
	"github.com/kipit-labs/kipitiny/internal/progress"
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
	// Where the browser posts the manifest, and the manifest.
	manifestURL, manifest string
}

// gitTokens caches GitHub installation tokens and serializes OAuth
// refreshes (a refresh token is single use).
type gitTokens struct {
	mu     sync.Mutex
	github map[string]oauth2.Token // provider ID -> installation token
}

// GitProviderView is a provider with its credentials masked.
type GitProviderView struct {
	domain.GitProvider
	Connected bool `json:"connected"`
	// Webhooks reports that pushes reach the manager through the app, for
	// every repository it reads.
	Webhooks bool `json:"webhooks"`
}

func gitProviderView(p domain.GitProvider) GitProviderView {
	v := GitProviderView{GitProvider: p, Connected: p.Connected(), Webhooks: p.WebhookSecret != ""}
	if v.ClientSecret != "" {
		v.ClientSecret = identity.SecretMask
	}
	return v
}

func (c *Projects) ListGitProviders(ctx context.Context) ([]GitProviderView, error) {
	ps, err := c.store.ListGitProviders(ctx)
	out := make([]GitProviderView, len(ps))
	for i, p := range ps {
		out[i] = gitProviderView(p)
		if identity.IsMember(ctx) { // enough to pick one
			out[i].GitProvider = domain.GitProvider{ID: p.ID, Kind: p.Kind, Name: p.Name, BaseURL: p.BaseURL, Account: p.Account}
		}
	}
	return out, err
}

// GitProviderInput creates or edits a GitLab or Gitea provider (a GitHub
// one is created with StartGitHubApp; only its name can change).
type GitProviderInput struct {
	// Kind is set on creation only.
	Kind domain.GitProviderKind `json:"kind"`
	Name string                 `json:"name"`
	// BaseURL defaults to the hosted forge (gitlab.com).
	BaseURL  string `json:"baseUrl"`
	ClientID string `json:"clientId"`
	// ClientSecret equal to SecretMask keeps the stored value on update.
	ClientSecret string `json:"clientSecret"`
}

// CreateGitProvider saves an OAuth application; AuthorizeGitProvider then
// connects it.
func (c *Projects) CreateGitProvider(ctx context.Context, in GitProviderInput) (GitProviderView, error) {
	if in.Kind != domain.GitLab && in.Kind != domain.Gitea {
		return GitProviderView{}, fmt.Errorf("%w: kind must be gitlab or gitea (GitHub providers are created as an app)", ErrInvalid)
	}
	p := domain.GitProvider{Kind: in.Kind}
	if err := applyGitProviderInput(&p, in); err != nil {
		return GitProviderView{}, err
	}
	p, err := c.store.CreateGitProvider(ctx, p)
	return gitProviderView(p), gitProviderNameTaken(err)
}

func (c *Projects) UpdateGitProvider(ctx context.Context, id string, in GitProviderInput) (GitProviderView, error) {
	p, err := c.store.GetGitProvider(ctx, id)
	if err != nil {
		return GitProviderView{}, err
	}
	if p.Kind == domain.GitHub {
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

func applyGitProviderInput(p *domain.GitProvider, in GitProviderInput) error {
	p.Name = strings.TrimSpace(in.Name)
	p.ClientID = strings.TrimSpace(in.ClientID)
	if in.ClientSecret != identity.SecretMask {
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
func normalizeForgeURL(kind domain.GitProviderKind, raw string) (string, error) {
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
	if errors.Is(err, domain.ErrConflict) {
		return fmt.Errorf("%w: a git provider already has this name", ErrInvalid)
	}
	return err
}

func (c *Projects) DeleteGitProvider(ctx context.Context, id string) error {
	err := c.store.DeleteGitProvider(ctx, id)
	if errors.Is(err, domain.ErrConflict) {
		return fmt.Errorf("%w: %s; unlink them first", ErrInvalid, err.Error())
	}
	c.gitTokens.mu.Lock()
	delete(c.gitTokens.github, id)
	c.gitTokens.mu.Unlock()
	return err
}

// TestGitProvider checks the provider can still list repositories,
// reporting its steps: getting a token ("token"), then listing ("repos").
func (c *Projects) TestGitProvider(ctx context.Context, id string) error {
	progress.Report(ctx, "token")
	p, token, err := c.gitProviderToken(ctx, id)
	if err != nil {
		return err
	}
	progress.Report(ctx, "repos")
	if _, err := gitprovider.Repos(ctx, p.Kind, p.BaseURL, token); err != nil {
		return fmt.Errorf("%w: %s: %s", ErrInvalid, p.Name, err.Error())
	}
	return nil
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
func (c *Projects) GitProviderRepos(ctx context.Context, id string) ([]gitprovider.Repo, error) {
	p, token, err := c.gitProviderToken(ctx, id)
	if err != nil {
		return nil, err
	}
	repos, err := gitprovider.Repos(ctx, p.Kind, p.BaseURL, token)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %s", ErrInvalid, p.Name, err.Error())
	}
	return repos, nil
}

// GitProviderBranches lists a repository's branches.
func (c *Projects) GitProviderBranches(ctx context.Context, id, repo string) ([]string, error) {
	if repo == "" || strings.Contains(repo, "..") {
		return nil, fmt.Errorf("%w: invalid repository", ErrInvalid)
	}
	p, token, err := c.gitProviderToken(ctx, id)
	if err != nil {
		return nil, err
	}
	bs, err := gitprovider.Branches(ctx, p.Kind, p.BaseURL, token, repo)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %s", ErrInvalid, repo, err.Error())
	}
	return bs, nil
}

// gitProviderToken returns a token reading the provider's repositories:
// a GitHub installation token (minted, cached until shortly before it
// expires) or the OAuth access token (refreshed when about to expire).
func (c *Projects) gitProviderToken(ctx context.Context, id string) (domain.GitProvider, string, error) {
	c.gitTokens.mu.Lock()
	defer c.gitTokens.mu.Unlock()
	p, err := c.store.GetGitProvider(ctx, id)
	if err != nil {
		return domain.GitProvider{}, "", err
	}
	if !p.Connected() {
		return p, "", fmt.Errorf("%w: git provider %s isn't connected", ErrInvalid, p.Name)
	}
	soon := time.Now().Add(5 * time.Minute)
	if p.Kind == domain.GitHub {
		if t, ok := c.gitTokens.github[id]; ok && t.Expiry.After(soon) {
			return p, t.AccessToken, nil
		}
		token, exp, err := gitprovider.InstallationToken(ctx, p)
		if err != nil {
			return p, "", fmt.Errorf("%w: %s (is the app still installed?)", ErrInvalid, err.Error())
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
		return p, "", fmt.Errorf("%w: %s: the authorization expired or was revoked; connect it again (%s)", ErrInvalid, p.Name, err.Error())
	}
	setOAuthToken(&p, tok)
	if _, err := c.store.UpdateGitProvider(ctx, p); err != nil {
		return p, "", err
	}
	return p, p.AccessToken, nil
}

// checkProviderRepo refuses a repository on another host than the
// provider's: its token must never be sent elsewhere.
func checkProviderRepo(p domain.GitProvider, repoURL string) error {
	u, err := url.Parse(repoURL)
	b, berr := url.Parse(p.BaseURL)
	if err != nil || berr != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, b.Host) {
		return fmt.Errorf("%w: %s only reads repositories on %s", ErrInvalid, p.Name, p.BaseURL)
	}
	return nil
}

// HandleGitProviderWebhook authenticates a GitHub App's push webhook and
// syncs the projects following that repository and branch. The body is only
// read once the provider is known and the delivery is signed, so unknown
// callers can't make the manager buffer payloads.
func (c *Projects) HandleGitProviderWebhook(ctx context.Context, providerID string, header func(string) string, readBody func() ([]byte, error)) error {
	p, err := c.store.GetGitProvider(ctx, providerID)
	if err != nil || p.WebhookSecret == "" {
		return identity.ErrUnauthorized // don't reveal which IDs exist
	}
	sig, ok := strings.CutPrefix(header("X-Hub-Signature-256"), "sha256=")
	if !ok {
		return identity.ErrUnauthorized
	}
	body, err := readBody()
	if err != nil {
		return err
	}
	mac := hmac.New(sha256.New, []byte(p.WebhookSecret))
	mac.Write(body)
	if !hmac.Equal([]byte(sig), []byte(hex.EncodeToString(mac.Sum(nil)))) {
		return identity.ErrUnauthorized
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
