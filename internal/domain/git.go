package domain

import (
	"context"
	"time"
)

// GitRepository stores the git links of projects and the git providers.
type GitRepository interface {
	GetProjectGit(ctx context.Context, projectID string) (ProjectGit, error)
	ListProjectGit(ctx context.Context) ([]ProjectGit, error)
	// SaveProjectGit creates or updates a project's link (not its sync state).
	SaveProjectGit(ctx context.Context, g ProjectGit) (ProjectGit, error)
	// SetProjectGitSync records a sync's outcome.
	SetProjectGitSync(ctx context.Context, g ProjectGit) error
	DeleteProjectGit(ctx context.Context, projectID string) error
	ListGitProviders(ctx context.Context) ([]GitProvider, error)
	GetGitProvider(ctx context.Context, id string) (GitProvider, error)
	// CreateGitProvider keeps p.ID when set.
	CreateGitProvider(ctx context.Context, p GitProvider) (GitProvider, error)
	UpdateGitProvider(ctx context.Context, p GitProvider) (GitProvider, error)
	// DeleteGitProvider returns ErrConflict while projects use it.
	DeleteGitProvider(ctx context.Context, id string) error
}

type GitProviderKind string

const (
	GitHub GitProviderKind = "github"
	GitLab GitProviderKind = "gitlab"
	Gitea  GitProviderKind = "gitea"
)

// GitProvider gives access to the private repositories of a forge account:
// a GitHub App installation, or a GitLab or Gitea OAuth authorization.
// Credentials read back masked.
type GitProvider struct {
	ID   string          `bun:"id,pk" json:"id"`
	Kind GitProviderKind `bun:"kind" json:"kind"`
	Name string          `bun:"name" json:"name"`
	// BaseURL is the forge's web address (https://github.com, a self-hosted
	// GitLab or Gitea).
	BaseURL string `bun:"base_url" json:"baseUrl"`
	// Account is the user or organization it was installed on or
	// authorized by; empty until connected.
	Account string `bun:"account" json:"account"`

	// GitHub App.
	AppID          int64  `bun:"app_id" json:"appId,omitempty"`
	AppSlug        string `bun:"app_slug" json:"appSlug,omitempty"`
	PrivateKey     string `bun:"private_key" json:"-"`
	InstallationID int64  `bun:"installation_id" json:"installationId,omitempty"`
	// WebhookSecret signs the app's push webhooks; empty when the app has
	// none (a manager GitHub can't reach).
	WebhookSecret string `bun:"webhook_secret" json:"-"`

	// OAuth application (GitLab, Gitea; GitHub's are unused).
	ClientID     string `bun:"client_id" json:"clientId,omitempty"`
	ClientSecret string `bun:"client_secret" json:"clientSecret,omitempty"`
	// RedirectURL is the callback the application was authorized with.
	RedirectURL    string     `bun:"redirect_url" json:"-"`
	AccessToken    string     `bun:"access_token" json:"-"`
	RefreshToken   string     `bun:"refresh_token" json:"-"`
	TokenExpiresAt *time.Time `bun:"token_expires_at" json:"-"`

	CreatedAt time.Time `bun:"created_at" json:"createdAt"`
}

// Connected reports whether it can read repositories.
func (p GitProvider) Connected() bool {
	if p.Kind == GitHub {
		return p.InstallationID != 0
	}
	return p.AccessToken != ""
}

// ProjectGit links a project to a compose file in a git repository, which
// then owns its services.
type ProjectGit struct {
	ProjectID string `bun:"project_id,pk" json:"projectId"`
	RepoURL   string `bun:"repo_url" json:"repoUrl"`
	Branch    string `bun:"branch" json:"branch"`
	// Path is the compose file's path in the repository.
	Path string `bun:"path" json:"path"`
	// ProviderID is the git provider reading the repository; empty for a
	// public one.
	ProviderID string `bun:"provider_id,nullzero" json:"providerId"`
	// AutoSync polls the branch every PollSeconds (webhooks sync anyway).
	AutoSync      bool   `bun:"auto_sync" json:"autoSync"`
	PollSeconds   int    `bun:"poll_seconds" json:"pollSeconds"`
	WebhookSecret string `bun:"webhook_secret" json:"webhookSecret"`
	// LastCommit is the commit last applied.
	LastCommit   string     `bun:"last_commit" json:"lastCommit"`
	LastSyncedAt *time.Time `bun:"last_synced_at" json:"lastSyncedAt,omitempty"`
	LastError    string     `bun:"last_error" json:"lastError"`
	Warnings     []string   `bun:"warnings" json:"warnings"`
	// Applied maps each service name to the image the last sync set.
	Applied map[string]string `bun:"applied" json:"applied"`
	// LinkedBy is the user whose permissions syncs run with; empty for links
	// made before there were members.
	LinkedBy string `bun:"linked_by" json:"linkedBy,omitempty"`
	// LinkedToken is the restricted token that linked it, whose restriction
	// syncs keep; empty when a user or an unrestricted token did.
	LinkedToken string    `bun:"linked_token" json:"-"`
	CreatedAt   time.Time `bun:"created_at" json:"createdAt"`
}
