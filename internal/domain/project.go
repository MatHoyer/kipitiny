package domain

import (
	"context"
	"time"
)

// ProjectRepository stores projects, their preview settings, tag colors and canvas layout.
type ProjectRepository interface {
	CreateProject(ctx context.Context, p Project) (Project, error)
	GetProject(ctx context.Context, id string) (Project, error)
	ListProjects(ctx context.Context) ([]Project, error)
	DeleteProject(ctx context.Context, id string) error
	SetProjectEnv(ctx context.Context, id string, env map[string]string, secrets []string) (Project, error)
	RenameProject(ctx context.Context, id, name string) error
	SetProjectTags(ctx context.Context, id string, tags []string) error
	SetProjectPreview(ctx context.Context, id string, p Preview) error
	SetProjectSleep(ctx context.Context, id string, sleepAfterSeconds int) error
	// SetProjectSleeping flags the project's deployed, running services as
	// asleep, or clears the flag on all of them.
	SetProjectSleeping(ctx context.Context, id string, sleeping bool) error
	// GetPreviewSettings returns ErrNotFound for a project without any.
	GetPreviewSettings(ctx context.Context, projectID string) (PreviewSettings, error)
	SavePreviewSettings(ctx context.Context, s PreviewSettings) (PreviewSettings, error)
	ListTagColors(ctx context.Context) ([]TagColor, error)
	// SetTagColor sets the color of a tag; an empty color deletes it.
	SetTagColor(ctx context.Context, name, color string) error
	ListCanvasPositions(ctx context.Context) ([]CanvasPosition, error)
	// SaveCanvasPositions upserts ps and deletes the nodes in remove.
	SaveCanvasPositions(ctx context.Context, ps []CanvasPosition, remove []string) error
}

type Project struct {
	ID   string `bun:"id,pk" json:"id"`
	Name string `bun:"name" json:"name"`
	// ServerID is where the project's containers run; fixed at creation.
	ServerID string `bun:"server_id" json:"serverId"`
	// Env holds variables shared by the services, which reference them as
	// {{ project.NAME }}.
	Env map[string]string `bun:"env" json:"env"`
	// Secrets names the Env entries that are write-only.
	Secrets []string `bun:"secrets" json:"secrets"`
	// ParentID is set on a preview environment: the project it copies.
	ParentID string `bun:"parent_id" json:"parentId,omitempty"`
	// Preview describes a preview environment; zero for other projects.
	Preview Preview `bun:"preview,type:text" json:"preview"`
	// SleepAfterSeconds stops every service after that long without a
	// request to its apps, and starts them again on the next one; 0 never
	// does.
	SleepAfterSeconds int `bun:"sleep_after_seconds" json:"sleepAfterSeconds"`
	// Maintenance is the page visitors of its apps get while one is down or
	// the project is in maintenance mode. Set on its own, never by a
	// compose file.
	Maintenance Maintenance `bun:"maintenance,type:text" json:"maintenance"`
	// Tags group projects in the UI. Set on their own, never by a compose
	// file.
	Tags      []string  `bun:"tags" json:"tags"`
	CreatedAt time.Time `bun:"created_at" json:"createdAt"`
	UpdatedAt time.Time `bun:"updated_at" json:"updatedAt"`
}

// Preview is what a preview environment was last deployed from.
type Preview struct {
	// Name is the preview's name under its parent (pr-12, staging).
	Name string `json:"name,omitempty"`
	// Branch and Commit are the source, when CI gave them; a git-linked
	// parent's compose file is read at Branch.
	Branch string `json:"branch,omitempty"`
	Commit string `json:"commit,omitempty"`
	// TTLSeconds closes the preview after that long without a request or
	// a deploy; 0 never does.
	TTLSeconds int `json:"ttlSeconds,omitempty"`
	// DeployedAt is the last time CI or a user deployed it.
	DeployedAt time.Time `json:"deployedAt,omitzero"`
}

// PreviewSettings are how a project's previews differ from it.
type PreviewSettings struct {
	ProjectID string `bun:"project_id,pk" json:"projectId"`
	// BaseDomain hosts the previews' apps: <preview>-<service>.<base>.
	BaseDomain string `bun:"base_domain" json:"baseDomain"`
	// Env are project variables of every preview, replacing the parent's;
	// Secrets names the write-only ones.
	Env     map[string]string `bun:"env" json:"env"`
	Secrets []string          `bun:"secrets" json:"secrets"`
	// Inherit names the parent's secrets previews may still use; the
	// others are left out.
	Inherit           []string `bun:"inherit" json:"inherit"`
	TTLSeconds        int      `bun:"ttl_seconds" json:"ttlSeconds"`
	SleepAfterSeconds int      `bun:"sleep_after_seconds" json:"sleepAfterSeconds"`
	// SeedMode is what --seed fills a new preview's databases with.
	SeedMode SeedMode `bun:"seed_mode" json:"seed"`
	// Salt keys the anonymized values' hashes (anonymize.Value).
	Salt      string    `bun:"salt" json:"-"`
	UpdatedAt time.Time `bun:"updated_at" json:"updatedAt"`
}

// SeedMode is what previews may be seeded with: nothing, their parent's
// databases anonymized with the rules of each (Service.Anonymize), or
// copied as they are.
type SeedMode string

const (
	SeedOff        SeedMode = ""
	SeedAnonymized SeedMode = "anonymized"
	SeedRaw        SeedMode = "raw"
)

// CanvasPosition is where a node of the map canvas was dragged to.
// TagColor is the color a user picked for a tag, one of core's palette.
type TagColor struct {
	Name      string    `bun:"name,pk" json:"name"`
	Color     string    `bun:"color" json:"color"`
	UpdatedAt time.Time `bun:"updated_at" json:"-"`
}

type CanvasPosition struct {

	// Node is "<kind>:<id>", e.g. svc:<service id>.
	Node      string    `bun:"node,pk" json:"node"`
	X         float64   `bun:"x" json:"x"`
	Y         float64   `bun:"y" json:"y"`
	UpdatedAt time.Time `bun:"updated_at" json:"-"`
}
