package projects

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/kipit-labs/kipitiny/internal/usecase/deploy"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/notify"
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
)

// ErrGitManaged refuses a change to a service a git repository owns.
var ErrGitManaged = domain.ErrGitManaged

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
	domain.ProjectGit
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

// CheckGitOwned refuses changes to the services of a git project, except
// from its sync. An orphaned database can still be deleted.
func (c *Projects) CheckGitOwned(ctx context.Context, projectID string) error {
	if ctx.Value(gitSyncKey{}) != nil {
		return nil
	}
	if _, err := c.store.GetProjectGit(ctx, projectID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		}
		return err
	}
	return ErrGitManaged
}

func (c *Projects) GetProjectGit(ctx context.Context, projectID string) (GitStatus, error) {
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
		if want, ok := g.Applied[s.Name]; ok && want != "" && s.Kind == domain.ServiceKindApp && s.Image != want {
			st.Drift = append(st.Drift, s.Name)
		}
	}
	return st, nil
}

// LinkProjectGit makes the project follow a compose file in a repository
// (or changes the link), then syncs it.
func (c *Projects) LinkProjectGit(ctx context.Context, projectID string, in GitInput) (GitStatus, error) {
	g, err := c.gitFromInput(ctx, projectID, in)
	if err != nil {
		return GitStatus{}, err
	}
	// Fail now on a wrong URL, branch or provider.
	tctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	if _, err := c.gitHead(tctx, g); err != nil {
		return GitStatus{}, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	if _, err := c.store.SaveProjectGit(ctx, g); err != nil {
		return GitStatus{}, err
	}
	c.kickGit(projectID)
	return c.GetProjectGit(ctx, projectID)
}

// PreviewProjectGit returns what linking the project to in would change,
// linking nothing: services the file doesn't list would be deleted.
func (c *Projects) PreviewProjectGit(ctx context.Context, projectID string, in GitInput) (deploy.ComposePlan, error) {
	g, err := c.gitFromInput(ctx, projectID, in)
	if err != nil {
		return deploy.ComposePlan{}, err
	}
	if cur, err := c.store.GetProjectGit(ctx, projectID); err == nil {
		g.LastCommit = cur.LastCommit
	}
	return c.syncGit(ctx, g, true, true)
}

// gitFromInput is the link in describes, checked.
func (c *Projects) gitFromInput(ctx context.Context, projectID string, in GitInput) (domain.ProjectGit, error) {
	p, err := c.store.GetProject(ctx, projectID)
	if err != nil {
		return domain.ProjectGit{}, err
	}
	if p.ParentID != "" {
		// Its compose file is the parent's, read at the preview's branch.
		return domain.ProjectGit{}, fmt.Errorf("%w: a preview follows its parent's repository; link the parent instead", ErrInvalid)
	}
	old, err := c.store.GetProjectGit(ctx, projectID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return domain.ProjectGit{}, err
	}
	g := domain.ProjectGit{
		ProjectID:     projectID,
		RepoURL:       strings.TrimSpace(in.RepoURL),
		Branch:        strings.TrimSpace(in.Branch),
		Path:          strings.TrimPrefix(path.Clean("/"+strings.TrimSpace(in.Path)), "/"),
		ProviderID:    strings.TrimSpace(in.ProviderID),
		AutoSync:      in.AutoSync,
		PollSeconds:   in.PollSeconds,
		WebhookSecret: old.WebhookSecret,
	}
	if a, ok := identity.ActorFrom(ctx); ok {
		g.LinkedBy, g.LinkedToken = a.UserID, a.TokenID
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
		g.WebhookSecret = identity.RandomToken(24)
	}
	if err := validateGit(g); err != nil {
		return domain.ProjectGit{}, err
	}
	if err := checkRepoHost(ctx, g); err != nil {
		return domain.ProjectGit{}, err
	}
	if g.ProviderID != "" {
		p, err := c.store.GetGitProvider(ctx, g.ProviderID)
		if errors.Is(err, domain.ErrNotFound) {
			return domain.ProjectGit{}, fmt.Errorf("%w: unknown git provider", ErrInvalid)
		} else if err != nil {
			return domain.ProjectGit{}, err
		}
		if err := checkProviderRepo(p, g.RepoURL); err != nil {
			return domain.ProjectGit{}, err
		}
	}
	return g, nil
}

func validateGit(g domain.ProjectGit) error {
	u, err := url.Parse(g.RepoURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("%w: repository must be an http(s) URL", ErrInvalid)
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

// checkRepoHost refuses a member a repository on a loopback, private or
// link-local address: the manager would fetch it, reaching services that
// aren't meant to be reachable from outside (other containers, the cloud
// metadata endpoint). A git provider's repositories are on the host an
// admin set up, and admins may link anything.
func checkRepoHost(ctx context.Context, g domain.ProjectGit) error {
	a, ok := identity.ActorFrom(ctx)
	if !ok || a.Admin() || g.ProviderID != "" {
		return nil
	}
	u, err := url.Parse(g.RepoURL)
	if err != nil {
		return fmt.Errorf("%w: invalid repository URL", ErrInvalid)
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", u.Hostname())
	if err != nil {
		return fmt.Errorf("%w: cannot resolve %s", ErrInvalid, u.Hostname())
	}
	for _, ip := range ips {
		if privateAddr(ip) {
			return fmt.Errorf("%w: %s is a private address; only admins link repositories there", identity.ErrForbidden, u.Hostname())
		}
	}
	return nil
}

// UnlinkProjectGit stops following the repository; the services stay, and
// can be edited again.
func (c *Projects) UnlinkProjectGit(ctx context.Context, projectID string) error {
	return c.store.DeleteProjectGit(ctx, projectID)
}

// SyncProjectGit fetches the compose file and applies it now (dryRun: only
// returns what would change).
func (c *Projects) SyncProjectGit(ctx context.Context, projectID string, dryRun bool) (deploy.ComposePlan, error) {
	g, err := c.store.GetProjectGit(ctx, projectID)
	if err != nil {
		return deploy.ComposePlan{}, err
	}
	return c.syncGit(ctx, g, true, dryRun)
}

// syncGit applies the branch's compose file. Unless forced, it does
// nothing while the branch is at the commit last applied.
func (c *Projects) syncGit(ctx context.Context, g domain.ProjectGit, force, dryRun bool) (deploy.ComposePlan, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	// The address is checked again: the name may resolve elsewhere now.
	ctx, err := c.asLinker(ctx, g)
	if err == nil {
		err = checkRepoHost(ctx, g)
	}
	if err != nil {
		if dryRun {
			return deploy.ComposePlan{}, err
		}
		return deploy.ComposePlan{}, c.gitSynced(g, "", deploy.ComposePlan{}, nil, err)
	}
	if !force {
		head, err := c.gitHead(ctx, g)
		if err != nil {
			return deploy.ComposePlan{}, c.gitSynced(g, "", deploy.ComposePlan{}, nil, err)
		}
		if head == g.LastCommit && g.LastError == "" {
			return deploy.ComposePlan{}, nil
		}
	}
	var plan deploy.ComposePlan
	var images map[string]string
	var commit string
	read := false
	err = c.WithGitFile(ctx, g, func(data []byte, cm string, files repoFiles) error {
		read, commit = true, cm
		var err error
		plan, images, err = c.mgr.ApplyGitCompose(fromGitSync(ctx), g.ProjectID, data,
			deploy.ApplyOptions{Prune: true, Deploy: true, DryRun: dryRun, Commit: commit}, files)
		return err
	})
	if !read {
		if dryRun {
			return deploy.ComposePlan{}, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
		}
		return deploy.ComposePlan{}, c.gitSynced(g, "", deploy.ComposePlan{}, nil, err)
	}
	if dryRun {
		if err != nil {
			return deploy.ComposePlan{}, err
		}
		return plan, nil
	}
	return plan, c.gitSynced(g, commit, plan, images, err)
}

// asLinker runs a sync without an actor (the poll loop, a webhook) as the
// user who linked the repository, within the restriction of the token that
// did if it was a restricted one.
func (c *Projects) asLinker(ctx context.Context, g domain.ProjectGit) (context.Context, error) {
	if _, ok := identity.ActorFrom(ctx); ok || g.LinkedBy == "" {
		return ctx, nil
	}
	u, err := c.store.GetUser(ctx, g.LinkedBy)
	if errors.Is(err, domain.ErrNotFound) {
		return ctx, fmt.Errorf("%w: the user who linked this repository was removed; link it again", identity.ErrForbidden)
	} else if err != nil {
		return ctx, err
	}
	a, err := c.mgr.UserActor(ctx, u)
	if err != nil {
		return ctx, err
	}
	if g.LinkedToken != "" {
		ts, err := c.store.ListAPITokens(ctx, u.ID)
		if err != nil {
			return ctx, err
		}
		i := slices.IndexFunc(ts, func(t domain.APIToken) bool { return t.ID == g.LinkedToken })
		if i < 0 {
			return ctx, fmt.Errorf("%w: the token that linked this repository was removed; link it again", identity.ErrForbidden)
		}
		if ts[i].Restricted {
			if a, err = c.mgr.Restricted(ctx, a, ts[i]); err != nil {
				return ctx, err
			}
		}
	}
	return identity.WithActor(ctx, a), nil
}

// gitSynced records a sync's outcome and notifies changes of state; it
// returns err.
func (c *Projects) gitSynced(g domain.ProjectGit, commit string, plan deploy.ComposePlan, images map[string]string, err error) error {
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
	if serr := c.store.SetProjectGitSync(ctx, g); serr != nil && !errors.Is(serr, domain.ErrNotFound) {
		c.log.Error("cannot record git sync", "project", g.ProjectID, "err", serr)
	}
	project, perr := c.store.GetProject(ctx, g.ProjectID)
	if perr != nil {
		return err
	}
	fields := []notify.Field{{Name: "Project", Value: project.Name}, {Name: "Repository", Value: g.RepoURL + " (" + g.Branch + ")"}}
	switch {
	case err != nil && err.Error() != prevErr:
		c.mgr.Notify(notify.Event{
			Type: domain.EventGitSyncFailed, Level: notify.Error, Title: project.Name + ": git sync failed",
			Message: err.Error(), Fields: fields,
		}, "/projects/"+g.ProjectID, "")
	case err == nil && (len(plan.Create)+len(plan.Update)+len(plan.Delete) > 0 || prevErr != ""):
		c.mgr.Notify(notify.Event{
			Type: domain.EventGitSyncSucceeded, Level: notify.Success, Title: project.Name + " synced from git",
			Message: syncSummary(plan), Fields: append(fields, notify.Field{Name: "Commit", Value: shortSHA(commit)}),
		}, "/projects/"+g.ProjectID, "")
	}
	return err
}

func syncSummary(p deploy.ComposePlan) string {
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
