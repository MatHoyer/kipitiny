package core

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/MatHoyer/kipitiny/internal/gitprovider"
	"github.com/MatHoyer/kipitiny/internal/notify"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// A project linked to a git repository follows the compose file there: the
// manager polls the branch (and syncs on push webhooks), applies the file
// like ApplyCompose with pruning, and refuses service edits made any other
// way. Deploying another tag stays allowed (CI): the service keeps it until
// the file changes that service.

const (
	defaultGitPoll  = 300
	minGitPoll      = 60
	maxGitPoll      = 24 * 3600
	gitTimeout      = 2 * time.Minute
	gitLoopEvery    = 30 * time.Second
	maxComposeBytes = 1 << 20

	EventGitSyncFailed    = "git.sync.failed"
	EventGitSyncSucceeded = "git.sync.succeeded"
)

// ErrGitManaged refuses a change to a service a git repository owns.
var ErrGitManaged = fmt.Errorf("%w: the project follows a git repository; change its compose file instead", ErrInvalid)

// ErrIgnored is a valid webhook that triggers nothing (another branch, ping).
var ErrIgnored = errors.New("ignored")

// GitInput links a project to a repository.
type GitInput struct {
	RepoURL string `json:"repoUrl"`
	// Branch defaults to main.
	Branch string `json:"branch"`
	// Path defaults to compose.yaml.
	Path string `json:"path"`
	// ProviderID is the git provider reading a private repository; empty
	// for a public one.
	ProviderID string `json:"providerId"`
	AutoSync   bool   `json:"autoSync"`
	// PollSeconds defaults to 300.
	PollSeconds int `json:"pollSeconds"`
}

// GitStatus is a project's link.
type GitStatus struct {
	store.ProjectGit
	// WebhookPath receives push webhooks (POST, the secret as GitHub HMAC,
	// GitLab token or bearer token).
	WebhookPath string `json:"webhookPath"`
	// Drift lists the services deployed with another image than the file's.
	Drift []string `json:"drift"`
}

type gitSyncKey struct{}

// fromGitSync marks ctx as a git sync's, which may change git-owned services.
func fromGitSync(ctx context.Context) context.Context {
	return context.WithValue(ctx, gitSyncKey{}, true)
}

// checkGitOwned refuses changes to the services of a git project, except
// from its sync. An orphaned database can still be deleted.
func (c *Core) checkGitOwned(ctx context.Context, projectID string) error {
	if ctx.Value(gitSyncKey{}) != nil {
		return nil
	}
	if _, err := c.store.GetProjectGit(ctx, projectID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return err
	}
	return ErrGitManaged
}

func (c *Core) GetProjectGit(ctx context.Context, projectID string) (GitStatus, error) {
	g, err := c.store.GetProjectGit(ctx, projectID)
	if err != nil {
		return GitStatus{}, err
	}
	svcs, err := c.store.ListServices(ctx, projectID)
	if err != nil {
		return GitStatus{}, err
	}
	st := GitStatus{ProjectGit: g, WebhookPath: "/api/hooks/projects/" + projectID, Drift: []string{}}
	for _, s := range svcs {
		if want, ok := g.Applied[s.Name]; ok && want != "" && s.Kind == store.ServiceKindApp && s.Image != want {
			st.Drift = append(st.Drift, s.Name)
		}
	}
	return st, nil
}

// LinkProjectGit makes the project follow a compose file in a repository
// (or changes the link), then syncs it.
func (c *Core) LinkProjectGit(ctx context.Context, projectID string, in GitInput) (GitStatus, error) {
	g, err := c.gitFromInput(ctx, projectID, in)
	if err != nil {
		return GitStatus{}, err
	}
	// Fail now on a wrong URL, branch or provider.
	tctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	if _, err := c.gitHead(tctx, g); err != nil {
		return GitStatus{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if _, err := c.store.SaveProjectGit(ctx, g); err != nil {
		return GitStatus{}, err
	}
	c.kickGit(projectID)
	return c.GetProjectGit(ctx, projectID)
}

// PreviewProjectGit returns what linking the project to in would change,
// linking nothing: services the file doesn't list would be deleted.
func (c *Core) PreviewProjectGit(ctx context.Context, projectID string, in GitInput) (ComposePlan, error) {
	g, err := c.gitFromInput(ctx, projectID, in)
	if err != nil {
		return ComposePlan{}, err
	}
	if cur, err := c.store.GetProjectGit(ctx, projectID); err == nil {
		g.LastCommit = cur.LastCommit
	}
	return c.syncGit(ctx, g, true, true)
}

// gitFromInput is the link in describes, checked.
func (c *Core) gitFromInput(ctx context.Context, projectID string, in GitInput) (store.ProjectGit, error) {
	if _, err := c.store.GetProject(ctx, projectID); err != nil {
		return store.ProjectGit{}, err
	}
	old, err := c.store.GetProjectGit(ctx, projectID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return store.ProjectGit{}, err
	}
	g := store.ProjectGit{
		ProjectID:     projectID,
		RepoURL:       strings.TrimSpace(in.RepoURL),
		Branch:        strings.TrimSpace(in.Branch),
		Path:          strings.TrimPrefix(path.Clean("/"+strings.TrimSpace(in.Path)), "/"),
		ProviderID:    strings.TrimSpace(in.ProviderID),
		AutoSync:      in.AutoSync,
		PollSeconds:   in.PollSeconds,
		WebhookSecret: old.WebhookSecret,
	}
	if g.Branch == "" {
		g.Branch = "main"
	}
	if g.Path == "" {
		g.Path = "compose.yaml"
	}
	if g.PollSeconds == 0 {
		g.PollSeconds = defaultGitPoll
	}
	if g.WebhookSecret == "" {
		g.WebhookSecret = randomToken(24)
	}
	if err := validateGit(g); err != nil {
		return store.ProjectGit{}, err
	}
	if g.ProviderID != "" {
		p, err := c.store.GetGitProvider(ctx, g.ProviderID)
		if errors.Is(err, store.ErrNotFound) {
			return store.ProjectGit{}, fmt.Errorf("%w: unknown git provider", ErrInvalid)
		} else if err != nil {
			return store.ProjectGit{}, err
		}
		if err := checkProviderRepo(p, g.RepoURL); err != nil {
			return store.ProjectGit{}, err
		}
	}
	return g, nil
}

func validateGit(g store.ProjectGit) error {
	u, err := url.Parse(g.RepoURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("%w: repository must be an https URL", ErrInvalid)
	}
	if u.User != nil {
		return fmt.Errorf("%w: no credentials in the URL; a private repository is read through a git provider", ErrInvalid)
	}
	if strings.ContainsAny(g.Branch, " ~^:?*[\\") || strings.Contains(g.Branch, "..") {
		return fmt.Errorf("%w: invalid branch", ErrInvalid)
	}
	if strings.HasPrefix(g.Path, "../") || g.Path == ".." {
		return fmt.Errorf("%w: the compose file must be a path inside the repository", ErrInvalid)
	}
	if g.PollSeconds < minGitPoll || g.PollSeconds > maxGitPoll {
		return fmt.Errorf("%w: poll interval must be between %d and %d seconds", ErrInvalid, minGitPoll, maxGitPoll)
	}
	return nil
}

// UnlinkProjectGit stops following the repository; the services stay, and
// can be edited again.
func (c *Core) UnlinkProjectGit(ctx context.Context, projectID string) error {
	return c.store.DeleteProjectGit(ctx, projectID)
}

// SyncProjectGit fetches the compose file and applies it now (dryRun: only
// returns what would change).
func (c *Core) SyncProjectGit(ctx context.Context, projectID string, dryRun bool) (ComposePlan, error) {
	g, err := c.store.GetProjectGit(ctx, projectID)
	if err != nil {
		return ComposePlan{}, err
	}
	return c.syncGit(ctx, g, true, dryRun)
}

// syncGit applies the branch's compose file. Unless forced, it does
// nothing while the branch is at the commit last applied.
func (c *Core) syncGit(ctx context.Context, g store.ProjectGit, force, dryRun bool) (ComposePlan, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	if !force {
		head, err := c.gitHead(ctx, g)
		if err != nil {
			return ComposePlan{}, c.gitSynced(g, "", ComposePlan{}, nil, err)
		}
		if head == g.LastCommit && g.LastError == "" {
			return ComposePlan{}, nil
		}
	}
	data, commit, err := c.gitReadFile(ctx, g)
	if err != nil {
		if dryRun {
			return ComposePlan{}, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		return ComposePlan{}, c.gitSynced(g, "", ComposePlan{}, nil, err)
	}
	a, err := c.applyCompose(fromGitSync(ctx), g.ProjectID, data, ApplyOptions{Prune: true, Deploy: true, DryRun: dryRun, Commit: commit}, true)
	if dryRun {
		if err != nil {
			return ComposePlan{}, err
		}
		return a.plan, nil
	}
	var plan ComposePlan
	var images map[string]string
	if a != nil {
		plan, images = a.plan, a.images
	}
	return plan, c.gitSynced(g, commit, plan, images, err)
}

// gitSynced records a sync's outcome and notifies changes of state; it
// returns err.
func (c *Core) gitSynced(g store.ProjectGit, commit string, plan ComposePlan, images map[string]string, err error) error {
	ctx := context.WithoutCancel(c.bg)
	prevErr := g.LastError
	now := time.Now()
	g.LastSyncedAt = &now
	g.Warnings = plan.Warnings
	if err != nil {
		g.LastError = err.Error()
	} else {
		g.LastError, g.LastCommit = "", commit
		if images != nil {
			g.Applied = images
		}
	}
	if serr := c.store.SetProjectGitSync(ctx, g); serr != nil && !errors.Is(serr, store.ErrNotFound) {
		c.log.Error("cannot record git sync", "project", g.ProjectID, "err", serr)
	}
	project, perr := c.store.GetProject(ctx, g.ProjectID)
	if perr != nil {
		return err
	}
	fields := []notify.Field{{Name: "Project", Value: project.Name}, {Name: "Repository", Value: g.RepoURL + " (" + g.Branch + ")"}}
	switch {
	case err != nil && err.Error() != prevErr:
		c.notify(notify.Event{
			Type: EventGitSyncFailed, Level: notify.Error, Title: project.Name + ": git sync failed",
			Message: err.Error(), Fields: fields,
		}, "/projects/"+g.ProjectID, "")
	case err == nil && (len(plan.Create)+len(plan.Update)+len(plan.Delete) > 0 || prevErr != ""):
		c.notify(notify.Event{
			Type: EventGitSyncSucceeded, Level: notify.Success, Title: project.Name + " synced from git",
			Message: syncSummary(plan), Fields: append(fields, notify.Field{Name: "Commit", Value: shortSHA(commit)}),
		}, "/projects/"+g.ProjectID, "")
	}
	return err
}

func syncSummary(p ComposePlan) string {
	var parts []string
	for _, x := range []struct {
		label string
		names []string
	}{{"created", p.Create}, {"deleted", p.Delete}, {"orphaned", p.Orphaned}} {
		if len(x.names) > 0 {
			parts = append(parts, x.label+" "+strings.Join(x.names, ", "))
		}
	}
	if len(p.Update) > 0 {
		var names []string
		for _, u := range p.Update {
			names = append(names, u.Name)
		}
		parts = append(parts, "updated "+strings.Join(names, ", "))
	}
	if len(parts) == 0 {
		return "No changes."
	}
	return strings.Join(parts, "; ") + "."
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// gitAuth is the credential reading the repository: its provider's token
// (none for a public repository).
func (c *Core) gitAuth(ctx context.Context, g store.ProjectGit) (transport.AuthMethod, error) {
	if g.ProviderID == "" {
		return nil, nil
	}
	p, token, err := c.gitProviderToken(ctx, g.ProviderID)
	if err != nil {
		return nil, err
	}
	if err := checkProviderRepo(p, g.RepoURL); err != nil {
		return nil, err
	}
	return &githttp.BasicAuth{Username: gitprovider.CloneUsername(p.Kind), Password: token}, nil
}

// gitAccessError explains a refused repository.
func gitAccessError(g store.ProjectGit, op string, err error) error {
	if errors.Is(err, transport.ErrAuthenticationRequired) || errors.Is(err, transport.ErrRepositoryNotFound) ||
		errors.Is(err, transport.ErrAuthorizationFailed) {
		if g.ProviderID == "" {
			return fmt.Errorf("%s %s: %w (a private repository? read it through a git provider)", op, g.RepoURL, err)
		}
		return fmt.Errorf("%s %s: %w (does the git provider have access to it?)", op, g.RepoURL, err)
	}
	return fmt.Errorf("%s %s: %w", op, g.RepoURL, err)
}

// gitHead returns the commit the branch points at, without fetching.
func (c *Core) gitHead(ctx context.Context, g store.ProjectGit) (string, error) {
	auth, err := c.gitAuth(ctx, g)
	if err != nil {
		return "", err
	}
	rem := git.NewRemote(memory.NewStorage(), &gitconfig.RemoteConfig{Name: "origin", URLs: []string{g.RepoURL}})
	refs, err := rem.ListContext(ctx, &git.ListOptions{Auth: auth})
	if err != nil {
		return "", gitAccessError(g, "list", err)
	}
	want := plumbing.NewBranchReferenceName(g.Branch)
	for _, r := range refs {
		if r.Name() == want {
			return r.Hash().String(), nil
		}
	}
	return "", fmt.Errorf("branch %s not found in %s", g.Branch, g.RepoURL)
}

// gitReadFile shallow-clones the branch into a temporary directory (on
// disk: memory stays flat whatever the repository's size) and reads the
// compose file. Returns its content and the commit.
func (c *Core) gitReadFile(ctx context.Context, g store.ProjectGit) ([]byte, string, error) {
	auth, err := c.gitAuth(ctx, g)
	if err != nil {
		return nil, "", err
	}
	base := filepath.Join(c.cfg.DataDir, "git")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return nil, "", err
	}
	dir, err := os.MkdirTemp(base, "clone-")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(dir)
	repo, err := git.PlainCloneContext(ctx, dir, true, &git.CloneOptions{
		URL:           g.RepoURL,
		Auth:          auth,
		ReferenceName: plumbing.NewBranchReferenceName(g.Branch),
		SingleBranch:  true,
		Depth:         1,
		NoCheckout:    true,
		Tags:          git.NoTags,
	})
	if err != nil {
		return nil, "", gitAccessError(g, "clone", err)
	}
	head, err := repo.Head()
	if err != nil {
		return nil, "", fmt.Errorf("clone %s: %w", g.RepoURL, err)
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return nil, "", err
	}
	f, err := commit.File(g.Path)
	if err != nil {
		return nil, "", fmt.Errorf("%s not found on %s: %w", g.Path, g.Branch, err)
	}
	if f.Size > maxComposeBytes {
		return nil, "", fmt.Errorf("%s is larger than %d bytes", g.Path, maxComposeBytes)
	}
	r, err := f.Reader()
	if err != nil {
		return nil, "", err
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	return data, head.Hash().String(), err
}

// StartGitSync polls the branches of the projects with auto sync, each on
// its interval, and syncs projects kicked by a webhook or a new link.
func (c *Core) StartGitSync() error {
	return c.goLoop(func() {
		checked := map[string]time.Time{}
		ticker := time.NewTicker(gitLoopEvery)
		defer ticker.Stop()
		for {
			select {
			case <-c.bg.Done():
				return
			case id := <-c.gitKick:
				checked[id] = time.Now()
				c.runGitSync(id, true)
				continue
			case <-ticker.C:
			}
			links, err := c.store.ListProjectGit(c.bg)
			if err != nil {
				c.log.Warn("git sync", "err", err)
				continue
			}
			for _, g := range links {
				if !g.AutoSync || time.Since(checked[g.ProjectID]) < time.Duration(g.PollSeconds)*time.Second {
					continue
				}
				checked[g.ProjectID] = time.Now()
				c.syncLink(g, false)
			}
		}
	})
}

func (c *Core) runGitSync(projectID string, force bool) {
	g, err := c.store.GetProjectGit(c.bg, projectID)
	if err != nil {
		return
	}
	c.syncLink(g, force)
}

func (c *Core) syncLink(g store.ProjectGit, force bool) {
	if _, err := c.syncGit(c.bg, g, force, false); err != nil && c.bg.Err() == nil {
		c.log.Warn("git sync failed", "project", g.ProjectID, "err", err)
	}
}

// kickGit syncs a project soon.
func (c *Core) kickGit(projectID string) {
	select {
	case c.gitKick <- projectID:
	default:
	}
}

// HandleGitWebhook authenticates a push webhook and syncs the project when
// it concerns its branch. It accepts GitHub/Gitea (X-Hub-Signature-256
// HMAC), GitLab (X-Gitlab-Token) and generic (Bearer token) deliveries.
func (c *Core) HandleGitWebhook(ctx context.Context, projectID string, header func(string) string, body []byte) error {
	g, err := c.store.GetProjectGit(ctx, projectID)
	if err != nil || g.WebhookSecret == "" {
		return ErrUnauthorized // don't reveal which IDs exist
	}
	if !webhookAuthorized(g.WebhookSecret, header, body) {
		return ErrUnauthorized
	}
	if header("X-GitHub-Event") == "ping" {
		return ErrIgnored
	}
	var payload struct {
		Ref string `json:"ref"`
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &payload); err != nil {
			return fmt.Errorf("%w: invalid JSON payload", ErrInvalid)
		}
	}
	if payload.Ref != "" && payload.Ref != "refs/heads/"+g.Branch {
		return ErrIgnored
	}
	c.kickGit(projectID)
	return nil
}

func webhookAuthorized(secret string, header func(string) string, body []byte) bool {
	if sig, ok := strings.CutPrefix(header("X-Hub-Signature-256"), "sha256="); ok {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		want := hex.EncodeToString(mac.Sum(nil))
		return hmac.Equal([]byte(sig), []byte(want))
	}
	for _, got := range []string{header("X-Gitlab-Token"), strings.TrimPrefix(header("Authorization"), "Bearer ")} {
		if got != "" && hmac.Equal([]byte(got), []byte(secret)) {
			return true
		}
	}
	return false
}

// isGitProject reports whether a git repository owns the project.
func (c *Core) isGitProject(ctx context.Context, projectID string) bool {
	_, err := c.store.GetProjectGit(ctx, projectID)
	return err == nil
}
