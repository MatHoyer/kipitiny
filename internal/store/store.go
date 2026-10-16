// Package store defines the manager's persistent state. Handlers and the core
// layer only talk to the Store interface; dialect-specific implementations
// live in subpackages (sqlite now, postgres later).
package store

import (
	"context"
	"errors"
	"time"

	"github.com/uptrace/bun"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

type Store interface {
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

	CreateService(ctx context.Context, s Service) (Service, error)
	GetService(ctx context.Context, id string) (Service, error)
	ListServices(ctx context.Context, projectID string) ([]Service, error)
	UpdateService(ctx context.Context, s Service) (Service, error)
	RenameService(ctx context.Context, id, name string) error
	SetServiceTags(ctx context.Context, id string, tags []string) error
	SetCurrentDeployment(ctx context.Context, serviceID, deploymentID string) error
	// SetDeploymentImage records the image a deployment runs, pinned to the
	// digest it was pulled at.
	SetDeploymentImage(ctx context.Context, id, image string, config Service) error
	// SetServiceStopped also clears Sleeping: the user's choice wins.
	SetServiceStopped(ctx context.Context, serviceID string, stopped bool) error
	// SetServiceImage records the image a service now deploys.
	SetServiceImage(ctx context.Context, serviceID, image string) error
	// ListAllServices returns every service of every project.
	ListAllServices(ctx context.Context) ([]Service, error)
	DeleteService(ctx context.Context, id string) error
	// SetServiceGitState records what a git sync applied to a service.
	SetServiceGitState(ctx context.Context, serviceID, specHash string, orphaned bool) error

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

	ListEmailProviders(ctx context.Context) ([]EmailProvider, error)
	GetEmailProvider(ctx context.Context, id string) (EmailProvider, error)
	CreateEmailProvider(ctx context.Context, p EmailProvider) (EmailProvider, error)
	UpdateEmailProvider(ctx context.Context, p EmailProvider) (EmailProvider, error)
	DeleteEmailProvider(ctx context.Context, id string) error

	ListSSOProviders(ctx context.Context) ([]SSOProvider, error)
	GetSSOProvider(ctx context.Context, id string) (SSOProvider, error)
	CreateSSOProvider(ctx context.Context, p SSOProvider) (SSOProvider, error)
	UpdateSSOProvider(ctx context.Context, p SSOProvider) (SSOProvider, error)
	DeleteSSOProvider(ctx context.Context, id string) error

	ListSSOIdentities(ctx context.Context, userID string) ([]SSOIdentity, error)
	// GetSSOIdentity finds the identity a provider's subject is linked to.
	GetSSOIdentity(ctx context.Context, providerID, subject string) (SSOIdentity, error)
	// CreateSSOIdentity returns ErrConflict if the subject or the user is
	// already linked through that provider.
	CreateSSOIdentity(ctx context.Context, i SSOIdentity) (SSOIdentity, error)
	SSOIdentityUsed(ctx context.Context, id, email string) error
	DeleteSSOIdentity(ctx context.Context, userID, id string) error

	CreateDeployment(ctx context.Context, d Deployment) (Deployment, error)
	GetDeployment(ctx context.Context, id string) (Deployment, error)
	// ListDeployments returns the newest first; limit <= 0 returns all.
	ListDeployments(ctx context.Context, serviceID string, limit int) ([]Deployment, error)
	DeleteDeployment(ctx context.Context, id string) error
	// FinishDeployment sets the final status, error and finish time.
	FinishDeployment(ctx context.Context, id string, status DeploymentStatus, errMsg string) error
	// FailRunningDeployments marks deployments left running by a previous
	// process as failed. Returns how many were updated.
	FailRunningDeployments(ctx context.Context, errMsg string) (int, error)

	CountUsers(ctx context.Context) (int, error)
	CreateUser(ctx context.Context, u User) (User, error)
	ListUsers(ctx context.Context) ([]User, error)
	GetUser(ctx context.Context, id string) (User, error)
	GetUserByUsername(ctx context.Context, username string) (User, error)
	// UpdateUserAccess sets the role and features.
	UpdateUserAccess(ctx context.Context, id string, role Role, features []string) (User, error)
	// DeleteUser removes the user with their sessions, grants and tokens.
	DeleteUser(ctx context.Context, id string) error
	// DeleteUserSessions signs the user out everywhere.
	DeleteUserSessions(ctx context.Context, userID string) error

	ListGrants(ctx context.Context, userID string) ([]ProjectGrant, error)
	// SetGrant creates or replaces a grant; no permissions deletes it.
	SetGrant(ctx context.Context, g ProjectGrant) error

	CreateInvite(ctx context.Context, i Invite) (Invite, error)
	// ListInvites returns the unused, unexpired invites, newest first.
	ListInvites(ctx context.Context) ([]Invite, error)
	// GetInviteByHash returns ErrNotFound for unknown, used or expired invites.
	GetInviteByHash(ctx context.Context, hash string) (Invite, error)
	// AcceptInvite creates u with the invite's role, features and grants,
	// links sso to it when set, and marks the invite used, at once.
	// ErrNotFound if it was used or expired.
	AcceptInvite(ctx context.Context, inviteID string, u User, sso *SSOIdentity) (User, error)
	DeleteInvite(ctx context.Context, id string) error
	// SetPassword updates the hash and revokes all of the user's sessions.
	SetPassword(ctx context.Context, userID, passwordHash string) error

	// SetTOTP turns TOTP on with secret (empty: off) and replaces the
	// recovery codes with codeHashes.
	SetTOTP(ctx context.Context, userID, secret string, codeHashes []string) error
	// UseTOTPStep records step as used; ErrConflict if it (or a later one)
	// already was.
	UseTOTPStep(ctx context.Context, userID string, step int64) error
	SetRecoveryCodes(ctx context.Context, userID string, codeHashes []string) error
	CountRecoveryCodes(ctx context.Context, userID string) (int, error)
	// UseRecoveryCode deletes the code; ErrNotFound if the user has no such code.
	UseRecoveryCode(ctx context.Context, userID, codeHash string) error

	CreatePasskey(ctx context.Context, p Passkey) (Passkey, error)
	ListPasskeys(ctx context.Context, userID string) ([]Passkey, error)
	GetPasskeyByCredentialID(ctx context.Context, credentialID string) (Passkey, error)
	// PasskeyUsed stores the updated credential (sign count) and the use.
	PasskeyUsed(ctx context.Context, id, credential string) error
	DeletePasskey(ctx context.Context, userID, id string) error

	CreateSession(ctx context.Context, s Session) error
	// GetSession returns ErrNotFound for unknown or expired sessions.
	GetSession(ctx context.Context, tokenHash string) (Session, error)
	DeleteSession(ctx context.Context, tokenHash string) error
	DeleteExpiredSessions(ctx context.Context) error

	ListBackupTargets(ctx context.Context) ([]BackupTarget, error)
	GetBackupTarget(ctx context.Context, id string) (BackupTarget, error)
	CreateBackupTarget(ctx context.Context, t BackupTarget) (BackupTarget, error)
	UpdateBackupTarget(ctx context.Context, t BackupTarget) (BackupTarget, error)
	// SetBackupTargetKey gives an unencrypted target an age key; ErrConflict
	// if it already has one (replacing it would orphan its backups).
	SetBackupTargetKey(ctx context.Context, id, identity, recipient string) error
	// DeleteBackupTarget returns ErrConflict while backups reference it.
	DeleteBackupTarget(ctx context.Context, id string) error

	CreateBackup(ctx context.Context, b Backup) (Backup, error)
	GetBackup(ctx context.Context, id string) (Backup, error)
	ListBackups(ctx context.Context, f BackupFilter) ([]Backup, error)
	FinishBackup(ctx context.Context, b Backup) error
	SetBackupVerification(ctx context.Context, id string, v Verification) error
	DeleteBackup(ctx context.Context, id string) error

	ListBackupSchedules(ctx context.Context, serviceID string) ([]BackupSchedule, error)
	GetBackupSchedule(ctx context.Context, id string) (BackupSchedule, error)
	CreateBackupSchedule(ctx context.Context, s BackupSchedule) (BackupSchedule, error)
	UpdateBackupSchedule(ctx context.Context, s BackupSchedule) (BackupSchedule, error)
	DeleteBackupSchedule(ctx context.Context, id string) error

	CreateRestore(ctx context.Context, r Restore) (Restore, error)
	ListRestores(ctx context.Context, serviceID string, limit int) ([]Restore, error)
	FinishRestore(ctx context.Context, id string, status OpStatus, errMsg string) error

	// FailRunningOperations marks backups and restores left running by a
	// previous process as failed.
	FailRunningOperations(ctx context.Context, errMsg string) error

	CreateAPIToken(ctx context.Context, t APIToken) (APIToken, error)
	// ListAPITokens returns userID's tokens, or every token when it's empty.
	ListAPITokens(ctx context.Context, userID string) ([]APIToken, error)
	// GetAPITokenByHash also records the use.
	GetAPITokenByHash(ctx context.Context, hash string) (APIToken, error)
	// UpdateAPIToken saves a token's name, access and hash.
	UpdateAPIToken(ctx context.Context, t APIToken) (APIToken, error)
	DeleteAPIToken(ctx context.Context, id string) error

	CreateDomain(ctx context.Context, d Domain) (Domain, error)
	ListDomains(ctx context.Context) ([]Domain, error)
	SetDomainProxied(ctx context.Context, id string, proxied bool) (Domain, error)
	DeleteDomain(ctx context.Context, id string) error

	ListNotificationChannels(ctx context.Context) ([]NotificationChannel, error)
	GetNotificationChannel(ctx context.Context, id string) (NotificationChannel, error)
	CreateNotificationChannel(ctx context.Context, ch NotificationChannel) (NotificationChannel, error)
	UpdateNotificationChannel(ctx context.Context, ch NotificationChannel) (NotificationChannel, error)
	DeleteNotificationChannel(ctx context.Context, id string) error

	ListRegistries(ctx context.Context) ([]Registry, error)
	GetRegistry(ctx context.Context, id string) (Registry, error)
	CreateRegistry(ctx context.Context, r Registry) (Registry, error)
	UpdateRegistry(ctx context.Context, r Registry) (Registry, error)
	DeleteRegistry(ctx context.Context, id string) error

	ListSecretConnections(ctx context.Context) ([]SecretConnection, error)
	GetSecretConnection(ctx context.Context, id string) (SecretConnection, error)
	CreateSecretConnection(ctx context.Context, c SecretConnection) (SecretConnection, error)
	SetSecretConnectionToken(ctx context.Context, id, token string) error
	DeleteSecretConnection(ctx context.Context, id string) error

	ListNetworks(ctx context.Context) ([]Network, error)
	GetNetwork(ctx context.Context, id string) (Network, error)
	CreateNetwork(ctx context.Context, n Network) (Network, error)
	DeleteNetwork(ctx context.Context, id string) error
	// SetServiceNetworks replaces the networks a service joins.
	SetServiceNetworks(ctx context.Context, serviceID string, networkIDs []string) error
	// SetProjectMaintenance replaces a project's maintenance page and mode.
	SetProjectMaintenance(ctx context.Context, projectID string, m Maintenance) error

	ListTagColors(ctx context.Context) ([]TagColor, error)
	// SetTagColor sets the color of a tag; an empty color deletes it.
	SetTagColor(ctx context.Context, name, color string) error

	ListCanvasPositions(ctx context.Context) ([]CanvasPosition, error)
	// SaveCanvasPositions upserts ps and deletes the nodes in remove.
	SaveCanvasPositions(ctx context.Context, ps []CanvasPosition, remove []string) error

	ListUptimeChecks(ctx context.Context) ([]UptimeCheck, error)
	GetUptimeCheck(ctx context.Context, serviceID string) (UptimeCheck, error)
	// SaveUptimeCheck creates or replaces a service's check settings,
	// keeping its state.
	SaveUptimeCheck(ctx context.Context, c UptimeCheck) (UptimeCheck, error)
	SetUptimeState(ctx context.Context, serviceID string, down bool, since time.Time) error
	// DeleteUptimeCheck also deletes its results.
	DeleteUptimeCheck(ctx context.Context, serviceID string) error
	// SetUptimeRecent replaces the last results of a service's check.
	SetUptimeRecent(ctx context.Context, serviceID string, recent []UptimeResult) error
	// AddUptimeResult counts one check in the hour of at.
	AddUptimeResult(ctx context.Context, serviceID string, at time.Time, ok bool, latency time.Duration) error
	// ListUptimeHours returns the hours from since on, oldest first.
	ListUptimeHours(ctx context.Context, serviceID string, since time.Time) ([]UptimeHour, error)
	// PruneUptime deletes the hours before before.
	PruneUptime(ctx context.Context, before time.Time) error

	AddAudit(ctx context.Context, e AuditEntry) error
	ListAudit(ctx context.Context, limit int) ([]AuditEntry, error)
	// PruneAudit deletes entries older than before.
	PruneAudit(ctx context.Context, before time.Time) error

	ListServers(ctx context.Context) ([]Server, error)
	GetServer(ctx context.Context, id string) (Server, error)
	CreateServer(ctx context.Context, s Server) (Server, error)
	UpdateServer(ctx context.Context, s Server) (Server, error)
	// DeleteServer returns ErrConflict while projects use it.
	DeleteServer(ctx context.Context, id string) error

	GetSetting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error

	// Snapshot writes a consistent copy of the whole store to path.
	Snapshot(ctx context.Context, path string) error

	Close() error
}

type User struct {
	bun.BaseModel `bun:"table:users,alias:u" json:"-"`

	ID           string    `bun:"id,pk" json:"id"`
	Username     string    `bun:"username" json:"username"`
	PasswordHash string    `bun:"password_hash" json:"-"`
	CreatedAt    time.Time `bun:"created_at" json:"createdAt"`
	Role         Role      `bun:"role" json:"role"`
	// Features are the server-level resources a member may see, read-only.
	Features []string `bun:"features" json:"features"`
	// TOTPSecret is the base32 authenticator secret; empty while 2FA is off.
	TOTPSecret   string `bun:"totp_secret" json:"-"`
	TOTPLastStep int64  `bun:"totp_last_step" json:"-"`
}

type Role string

const (
	RoleAdmin  Role = "admin"  // everything
	RoleMember Role = "member" // only what their grants and features allow
)

// ProjectGrant is what a member may do in one project.
type ProjectGrant struct {
	bun.BaseModel `bun:"table:project_grants,alias:grant" json:"-"`

	UserID    string    `bun:"user_id,pk" json:"userId"`
	ProjectID string    `bun:"project_id,pk" json:"projectId"`
	Perms     []string  `bun:"perms" json:"perms"`
	CreatedAt time.Time `bun:"created_at" json:"createdAt"`
}

// Invite is a single-use link that creates an account.
type Invite struct {
	bun.BaseModel `bun:"table:invites,alias:invite" json:"-"`

	ID        string   `bun:"id,pk" json:"id"`
	TokenHash string   `bun:"token_hash" json:"-"`
	Role      Role     `bun:"role" json:"role"`
	Features  []string `bun:"features" json:"features"`
	// Grants maps project IDs to permissions.
	Grants    map[string][]string `bun:"grants" json:"grants"`
	CreatedBy string              `bun:"created_by" json:"createdBy"`
	// Email is where the link was sent; empty when copied by hand.
	Email     string     `bun:"email" json:"email,omitempty"`
	CreatedAt time.Time  `bun:"created_at" json:"createdAt"`
	ExpiresAt time.Time  `bun:"expires_at" json:"expiresAt"`
	UsedAt    *time.Time `bun:"used_at" json:"usedAt,omitempty"`
}

type Passkey struct {
	bun.BaseModel `bun:"table:passkeys,alias:passkey" json:"-"`

	ID     string `bun:"id,pk" json:"id"`
	UserID string `bun:"user_id" json:"-"`
	Name   string `bun:"name" json:"name"`
	// CredentialID is the base64url WebAuthn credential ID.
	CredentialID string `bun:"credential_id" json:"-"`
	// Credential is the JSON-encoded webauthn.Credential.
	Credential string     `bun:"credential" json:"-"`
	CreatedAt  time.Time  `bun:"created_at" json:"createdAt"`
	LastUsedAt *time.Time `bun:"last_used_at" json:"lastUsedAt,omitempty"`
}

type Session struct {
	bun.BaseModel `bun:"table:sessions,alias:session" json:"-"`

	TokenHash string    `bun:"token_hash,pk"`
	UserID    string    `bun:"user_id"`
	CreatedAt time.Time `bun:"created_at"`
	ExpiresAt time.Time `bun:"expires_at"`
}

type Project struct {
	bun.BaseModel `bun:"table:projects,alias:project" json:"-"`

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
	bun.BaseModel `bun:"table:preview_settings,alias:ps" json:"-"`

	ProjectID string `bun:"project_id,pk" json:"projectId"`
	// BaseDomain hosts the previews' apps: <preview>-<service>.<base>.
	BaseDomain string `bun:"base_domain" json:"baseDomain"`
	// Env are project variables of every preview, replacing the parent's;
	// Secrets names the write-only ones.
	Env     map[string]string `bun:"env" json:"env"`
	Secrets []string          `bun:"secrets" json:"secrets"`
	// Inherit names the parent's secrets previews may still use; the
	// others are left out.
	Inherit           []string  `bun:"inherit" json:"inherit"`
	TTLSeconds        int       `bun:"ttl_seconds" json:"ttlSeconds"`
	SleepAfterSeconds int       `bun:"sleep_after_seconds" json:"sleepAfterSeconds"`
	AllowSeed         bool      `bun:"allow_seed" json:"allowSeed"`
	UpdatedAt         time.Time `bun:"updated_at" json:"updatedAt"`
}

type ServiceKind string

const (
	ServiceKindApp      ServiceKind = "app"
	ServiceKindPostgres ServiceKind = "postgres"
	ServiceKindRedis    ServiceKind = "redis"
	ServiceKindMySQL    ServiceKind = "mysql"
	ServiceKindMariaDB  ServiceKind = "mariadb"
	ServiceKindMongoDB  ServiceKind = "mongodb"
)

// IsDatabase reports whether the kind is a stateful single-replica service
// on a data volume, never public.
func (k ServiceKind) IsDatabase() bool {
	switch k {
	case ServiceKindPostgres, ServiceKindRedis, ServiceKindMySQL, ServiceKindMariaDB, ServiceKindMongoDB:
		return true
	}
	return false
}

// IsMySQL reports whether the kind speaks the MySQL protocol (MySQL or
// MariaDB).
func (k ServiceKind) IsMySQL() bool {
	return k == ServiceKindMySQL || k == ServiceKindMariaDB
}

type Service struct {
	bun.BaseModel `bun:"table:services,alias:service" json:"-"`

	ID        string `bun:"id,pk" json:"id"`
	ProjectID string `bun:"project_id" json:"projectId"`
	// ServerID mirrors the project's server.
	ServerID string      `bun:"server_id" json:"serverId"`
	Name     string      `bun:"name" json:"name"`
	Kind     ServiceKind `bun:"kind" json:"kind"`
	Image    string      `bun:"image" json:"image"`
	// Icon names the logo the UI shows (e.g. ghost), set by a template or
	// the user; empty picks one from the kind or image.
	Icon string `bun:"icon" json:"icon"`
	// Tags group services in the UI, like Icon set by a compose file or
	// the user.
	Tags []string `bun:"tags" json:"tags"`
	// Replicas is always 1 for databases.
	Replicas int `bun:"replicas" json:"replicas"`
	// Port is the container port Traefik routes to; 0 when not public.
	Port int `bun:"port" json:"port"`
	// Domain is the public hostname; empty when not public.
	Domain string            `bun:"domain" json:"domain"`
	Env    map[string]string `bun:"env" json:"env"`
	// Secrets names the Env entries that are write-only; the others are
	// plain variables.
	Secrets []string `bun:"secrets" json:"secrets"`
	// MemoryMB is the container memory limit; 0 means unlimited.
	MemoryMB int `bun:"memory_mb" json:"memoryMb"`
	// CPUs is the container CPU limit in cores (0.5 = half a core); 0 means
	// unlimited.
	CPUs float64 `bun:"cpus" json:"cpus"`
	// HealthPath is an HTTP path that must answer 2xx/3xx before a new
	// replica takes over; empty uses the image healthcheck or a stability wait.
	HealthPath string `bun:"health_path" json:"healthPath"`
	// PreDeploy runs once (sh -c) in a one-off container before a rollout,
	// e.g. migrations. A failure aborts the deploy.
	PreDeploy string `bun:"pre_deploy" json:"preDeploy"`
	// Healthcheck replaces the image's own and the injected probe (apps
	// only); the zero value has none.
	Healthcheck Healthcheck `bun:"healthcheck,type:text" json:"healthcheck"`
	// Volumes are named volumes an app mounts, shared by its replicas and
	// kept across deploys. Databases keep their data in their own volume.
	Volumes []Volume `bun:"volumes" json:"volumes"`
	// Middlewares apply to a public app's requests through Traefik.
	Middlewares Middlewares `bun:"middlewares,type:text" json:"middlewares"`
	// PublishedPorts are host ports bound straight to an app's container,
	// for traffic Traefik doesn't carry (game servers, MQTT…). An app with
	// published ports runs one replica.
	PublishedPorts []PublishedPort `bun:"published_ports" json:"publishedPorts"`
	// StopGraceSeconds is how long an app gets to exit after SIGTERM before
	// it is killed (e.g. a game server saving its world); 0 means 10 s.
	StopGraceSeconds int `bun:"stop_grace_seconds" json:"stopGraceSeconds"`
	// HostNetwork runs an app in the host's network namespace (agents that
	// report on the host): no project network, no domain, one replica.
	HostNetwork bool `bun:"host_network" json:"hostNetwork"`
	// DockerSocket mounts the host's Docker socket in an app: "" none, "ro"
	// or "rw". Either gives the app control of the host.
	DockerSocket string `bun:"docker_socket" json:"dockerSocket"`
	// PreBackup runs (sh -c) in a running replica before each volume
	// backup, e.g. to flush to disk. A failure aborts the backup.
	PreBackup string `bun:"pre_backup" json:"preBackup"`
	// Runtime holds the rarer container settings (command, user, tmpfs…);
	// the zero value runs the image as built. Apps only.
	Runtime Runtime `bun:"runtime,type:text" json:"runtime"`
	// Networks are the IDs of the networks created by hand that the service
	// joins besides its project network. Set on their own, never by a
	// compose file, so a git sync keeps them.
	Networks            []string `bun:"networks" json:"networks"`
	CurrentDeploymentID string   `bun:"current_deployment_id" json:"currentDeploymentId"`
	// Stopped is the desired run state after the user stopped the service.
	Stopped bool `bun:"stopped" json:"stopped"`
	// Sleeping is set while the manager keeps the service stopped for its
	// project being idle. Waking the project, a stop or a deploy clears it.
	Sleeping bool `bun:"sleeping" json:"sleeping"`
	// GitSpecHash is the hash of the compose block a git sync last applied.
	GitSpecHash string `bun:"git_spec_hash" json:"-"`
	// Orphaned marks a database of a git project that the compose file no
	// longer lists: kept until deleted by hand.
	Orphaned bool `bun:"orphaned" json:"orphaned"`

	CreatedAt time.Time `bun:"created_at" json:"createdAt"`
	UpdatedAt time.Time `bun:"updated_at" json:"updatedAt"`
}

// ProjectGit links a project to a compose file in a git repository, which
// then owns its services.
type ProjectGit struct {
	bun.BaseModel `bun:"table:project_git,alias:project_git" json:"-"`

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
	LinkedBy  string    `bun:"linked_by" json:"linkedBy,omitempty"`
	CreatedAt time.Time `bun:"created_at" json:"createdAt"`
}

// Volume is a named volume mounted at Path in every replica of an app.
type Volume struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// PublishedPort binds HostPort on the server to ContainerPort of an app.
type PublishedPort struct {
	HostPort      int `json:"hostPort"`
	ContainerPort int `json:"containerPort"`
	// Protocol is "tcp" or "udp".
	Protocol string `json:"protocol"`
}

// Healthcheck is a Docker healthcheck, as compose's healthcheck key writes
// it. Test ["NONE"] disables the image's; an empty Test means none set.
type Healthcheck struct {
	// Test is ["CMD", args...], ["CMD-SHELL", command] or ["NONE"].
	Test               []string `json:"test,omitempty"`
	IntervalSeconds    int      `json:"intervalSeconds,omitempty"`
	TimeoutSeconds     int      `json:"timeoutSeconds,omitempty"`
	StartPeriodSeconds int      `json:"startPeriodSeconds,omitempty"`
	Retries            int      `json:"retries,omitempty"`
}

// Set reports whether the healthcheck replaces the image's.
func (h Healthcheck) Set() bool { return len(h.Test) > 0 }

// Runtime is how an app's container runs, beyond its image, env and
// resources: compose's command, entrypoint, user, tmpfs… Empty fields keep
// the image's or Docker's defaults.
type Runtime struct {
	// Command and Entrypoint replace the image's CMD and ENTRYPOINT;
	// Entrypoint [""] clears the image's.
	Command    []string `json:"command,omitempty"`
	Entrypoint []string `json:"entrypoint,omitempty"`
	// User is a name or UID, optionally with :group.
	User       string `json:"user,omitempty"`
	WorkingDir string `json:"workingDir,omitempty"`
	// Labels are added to the container's own (never kipitiny.* or traefik.*).
	Labels map[string]string `json:"labels,omitempty"`
	// Tmpfs maps an in-memory mount's path to its options (size=64m,mode=1777).
	Tmpfs map[string]string `json:"tmpfs,omitempty"`
	// ShmSizeMB sizes /dev/shm; 0 keeps Docker's 64 MB.
	ShmSizeMB int               `json:"shmSizeMb,omitempty"`
	Ulimits   map[string]Ulimit `json:"ulimits,omitempty"`
	// Sysctls are namespaced kernel parameters (net.core.somaxconn…).
	Sysctls map[string]string `json:"sysctls,omitempty"`
	// ReadOnly mounts the root filesystem read-only.
	ReadOnly bool `json:"readOnly,omitempty"`
	// Init runs an init process as PID 1 that reaps zombies.
	Init       bool   `json:"init,omitempty"`
	StopSignal string `json:"stopSignal,omitempty"`
	TTY        bool   `json:"tty,omitempty"`
	StdinOpen  bool   `json:"stdinOpen,omitempty"`
	// ExtraHosts are host:ip entries added to /etc/hosts (ip may be
	// host-gateway).
	ExtraHosts []string `json:"extraHosts,omitempty"`
}

// Ulimit is a soft and hard limit; compose's single value sets both.
type Ulimit struct {
	Soft int64 `json:"soft"`
	Hard int64 `json:"hard"`
}

// IsZero reports whether r changes nothing.
func (r Runtime) IsZero() bool {
	return len(r.Command) == 0 && len(r.Entrypoint) == 0 && r.User == "" && r.WorkingDir == "" && len(r.Labels) == 0 &&
		len(r.Tmpfs) == 0 && r.ShmSizeMB == 0 && len(r.Ulimits) == 0 && len(r.Sysctls) == 0 && !r.ReadOnly && !r.Init &&
		r.StopSignal == "" && !r.TTY && !r.StdinOpen && len(r.ExtraHosts) == 0
}

// Middlewares are the Traefik middlewares on an app's router; the zero
// value adds none.
type Middlewares struct {
	// BasicAuth asks for one of these users' credentials.
	BasicAuth []BasicAuthUser `json:"basicAuth,omitempty"`
	// IPAllowList limits clients to these IPs or CIDR ranges.
	IPAllowList []string `json:"ipAllowList,omitempty"`
	// RateLimit caps requests per client IP; nil means unlimited.
	RateLimit *RateLimit `json:"rateLimit,omitempty"`
	// Headers are set on every response; an empty value removes the header.
	Headers map[string]string `json:"headers,omitempty"`
}

// BasicAuthUser is one basic auth login. Its password is either stored as
// Hash or read from a password manager at deploy (Ref).
type BasicAuthUser struct {
	Name string `json:"name"`
	// Hash is the bcrypt hash of the password. For a Ref user it is only
	// set in deployment snapshots, hashed from the value fetched at deploy.
	Hash string `json:"hash,omitempty"`
	// Ref is a password manager reference, {{ scheme://… }}.
	Ref string `json:"ref,omitempty"`
}

// RateLimit allows Average requests per second per client IP, with bursts
// of up to Burst requests.
type RateLimit struct {
	Average int `json:"average"`
	Burst   int `json:"burst"`
}

// Maintenance is a project's maintenance page, served by the manager through
// Traefik when one of its apps has no replica to answer, and maintenance
// mode, which serves it to every visitor of its apps while the replicas
// keep running.
type Maintenance struct {
	// Enabled is maintenance mode.
	Enabled bool `json:"enabled"`
	// AllowIPs still reach the apps in maintenance mode (IPs or CIDR ranges).
	AllowIPs []string `json:"allowIps,omitempty"`
	// Title and Message replace the default page's text; empty keeps it.
	Title   string `json:"title,omitempty"`
	Message string `json:"message,omitempty"`
	// HTML, when set, is served instead of the default page.
	HTML string `json:"html,omitempty"`
	// Starting is the page visitors get while the project wakes from sleep.
	Starting StartingPage `json:"starting,omitzero"`
}

// StartingPage replaces the default "Starting…" page's text, or the whole
// page with HTML; zero keeps the default.
type StartingPage struct {
	Title   string `json:"title,omitempty"`
	Message string `json:"message,omitempty"`
	HTML    string `json:"html,omitempty"`
}

// IsZero reports whether no middleware is configured.
func (m Middlewares) IsZero() bool {
	return len(m.BasicAuth) == 0 && len(m.IPAllowList) == 0 && m.RateLimit == nil && len(m.Headers) == 0
}

type DeploymentStatus string

const (
	DeploymentRunning   DeploymentStatus = "running"
	DeploymentSucceeded DeploymentStatus = "succeeded"
	DeploymentFailed    DeploymentStatus = "failed"
)

type Deployment struct {
	bun.BaseModel `bun:"table:deployments,alias:deployment" json:"-"`

	ID        string           `bun:"id,pk" json:"id"`
	ServiceID string           `bun:"service_id" json:"serviceId"`
	Status    DeploymentStatus `bun:"status" json:"status"`
	Image     string           `bun:"image" json:"image"`
	Error     string           `bun:"error" json:"error,omitempty"`
	GitCommit string           `bun:"git_commit" json:"gitCommit,omitempty"`
	// TriggeredBy is who started it: user:<name> or token:<name>; empty
	// for older deployments.
	TriggeredBy string `bun:"triggered_by" json:"triggeredBy,omitempty"`
	// Config is the service as deployed (image included).
	Config     Service    `bun:"config,type:text" json:"-"`
	CreatedAt  time.Time  `bun:"created_at" json:"createdAt"`
	FinishedAt *time.Time `bun:"finished_at" json:"finishedAt,omitempty"`
}

type BackupTargetKind string

const (
	BackupTargetLocal BackupTargetKind = "local"
	BackupTargetS3    BackupTargetKind = "s3"

	// LocalTargetID is the built-in local disk target.
	LocalTargetID = "local"
)

type BackupTarget struct {
	bun.BaseModel `bun:"table:backup_targets,alias:target" json:"-"`

	ID        string           `bun:"id,pk" json:"id"`
	Name      string           `bun:"name" json:"name"`
	Kind      BackupTargetKind `bun:"kind" json:"kind"`
	Endpoint  string           `bun:"endpoint" json:"endpoint"`
	Region    string           `bun:"region" json:"region"`
	Bucket    string           `bun:"bucket" json:"bucket"`
	Prefix    string           `bun:"prefix" json:"prefix"`
	AccessKey string           `bun:"access_key" json:"accessKey"`
	SecretKey string           `bun:"secret_key" json:"secretKey"`
	UseSSL    bool             `bun:"use_ssl" json:"useSsl"`
	// AgeRecipient/AgeIdentity are set when backups on this target are
	// encrypted with age (X25519).
	AgeRecipient string    `bun:"age_recipient" json:"ageRecipient"`
	AgeIdentity  string    `bun:"age_identity" json:"-"`
	CreatedAt    time.Time `bun:"created_at" json:"createdAt"`
}

func (t BackupTarget) Encrypted() bool { return t.AgeRecipient != "" }

// OpStatus is the lifecycle of a background operation (backup, restore).
type OpStatus string

const (
	OpRunning   OpStatus = "running"
	OpSucceeded OpStatus = "succeeded"
	OpFailed    OpStatus = "failed"
)

type Backup struct {
	bun.BaseModel `bun:"table:backups,alias:backup" json:"-"`

	ID string `bun:"id,pk" json:"id"`
	// Kind is postgres (a pg_dump), dump (a MySQL, MariaDB or MongoDB dump,
	// ServiceKind tells which), volume (an archive of a service's volumes)
	// or manager (the manager's own state).
	Kind        BackupKind `bun:"kind" json:"kind"`
	ServiceID   string     `bun:"service_id" json:"serviceId"`
	ProjectID   string     `bun:"project_id" json:"projectId"`
	ServiceName string     `bun:"service_name" json:"serviceName"`
	ProjectName string     `bun:"project_name" json:"projectName"`
	// ServiceKind is the backed-up service's kind; empty for the manager.
	ServiceKind ServiceKind `bun:"service_kind" json:"serviceKind,omitempty"`
	// ServiceIcon is the service's icon hint (see core.IconHint).
	ServiceIcon string   `bun:"service_icon" json:"serviceIcon,omitempty"`
	TargetID    string   `bun:"target_id" json:"targetId"`
	ScheduleID  string   `bun:"schedule_id" json:"scheduleId,omitempty"`
	ObjectKey   string   `bun:"object_key" json:"objectKey"`
	Status      OpStatus `bun:"status" json:"status"`
	SizeBytes   int64    `bun:"size_bytes" json:"sizeBytes"`
	SHA256      string   `bun:"sha256" json:"sha256"`
	Encrypted   bool     `bun:"encrypted" json:"encrypted"`
	// PGVersion is the database server's version (any dumped kind, despite
	// the name).
	PGVersion string `bun:"pg_version" json:"pgVersion"`
	// Database is the PostgreSQL database dumped; empty (older backups) is
	// the service's own.
	Database string `bun:"db_name" json:"database,omitempty"`
	// Volumes names the volumes in a volume backup's archive.
	Volumes []string `bun:"volumes" json:"volumes,omitempty"`
	// Image is the backed-up service's image: a restore test falls back to
	// it when the service is gone or runs another major version.
	Image string `bun:"image" json:"image,omitempty"`
	// Extensions are the PostgreSQL extensions the dumped database uses
	// (plpgsql aside): restoring needs a server that has them (PostGIS…).
	Extensions []string   `bun:"extensions" json:"extensions,omitempty"`
	DurationMS int64      `bun:"duration_ms" json:"durationMs"`
	Error      string     `bun:"error" json:"error,omitempty"`
	CreatedAt  time.Time  `bun:"created_at" json:"createdAt"`
	FinishedAt *time.Time `bun:"finished_at" json:"finishedAt,omitempty"`
	// Step is where a running backup is at, and StepBytes how much it has
	// uploaded so far; core fills them, they aren't stored.
	Step      string `bun:"-" json:"step,omitempty"`
	StepBytes int64  `bun:"-" json:"stepBytes,omitempty"`
	Verification
}

// Verification is the result of restoring a backup into a throwaway server.
type Verification struct {
	VerifyStatus  OpStatus            `bun:"verify_status" json:"verifyStatus,omitempty"`
	VerifyError   string              `bun:"verify_error" json:"verifyError,omitempty"`
	VerifyDetails VerificationDetails `bun:"verify_details" json:"verifyDetails"`
	VerifiedAt    *time.Time          `bun:"verified_at" json:"verifiedAt,omitempty"`
	// VerifyStep is where a running restore test is at; core fills it, it
	// isn't stored.
	VerifyStep string `bun:"-" json:"verifyStep,omitempty"`
}

type VerificationDetails struct {
	Tables  int   `json:"tables"`
	Rows    int64 `json:"rows"`
	DBBytes int64 `json:"dbBytes"`
	// Files counts the entries of a volume backup's archive.
	Files      int64 `json:"files,omitempty"`
	DurationMS int64 `json:"durationMs"`
}

type BackupKind string

const (
	BackupKindPostgres BackupKind = "postgres"
	BackupKindDump     BackupKind = "dump"
	BackupKindVolume   BackupKind = "volume"
	BackupKindManager  BackupKind = "manager"
)

type BackupFilter struct {
	Kind       BackupKind
	ServiceID  string
	ScheduleID string
	ProjectID  string
	// ProjectIn, when not nil, keeps the backups of these projects only.
	ProjectIn []string
	Status    OpStatus
	Limit     int
}

type Restore struct {
	bun.BaseModel `bun:"table:restores,alias:restore" json:"-"`

	ID         string     `bun:"id,pk" json:"id"`
	BackupID   string     `bun:"backup_id" json:"backupId"`
	ServiceID  string     `bun:"service_id" json:"serviceId"`
	Status     OpStatus   `bun:"status" json:"status"`
	Error      string     `bun:"error" json:"error,omitempty"`
	CreatedAt  time.Time  `bun:"created_at" json:"createdAt"`
	FinishedAt *time.Time `bun:"finished_at" json:"finishedAt,omitempty"`
	// Step is where a running restore is at; core fills it, it isn't stored.
	Step string `bun:"-" json:"step,omitempty"`
}

// BackupSchedule backs up a database on a cron schedule and prunes older
// backups it made. A backup is kept if any Keep* rule selects it.
type BackupSchedule struct {
	bun.BaseModel `bun:"table:backup_schedules,alias:schedule" json:"-"`

	ID string `bun:"id,pk" json:"id"`
	// Kind is what it backs up: a PostgreSQL service, a MySQL, MariaDB or
	// MongoDB one (dump), a service's volumes, or the manager's own state (no
	// ServiceID).
	Kind      BackupKind `bun:"kind" json:"kind"`
	ServiceID string     `bun:"service_id,nullzero" json:"serviceId,omitempty"`
	TargetID  string     `bun:"target_id" json:"targetId"`
	// Database is the PostgreSQL database backed up; empty follows the
	// service's own.
	Database string `bun:"db_name" json:"database,omitempty"`
	// Cron is a standard 5-field expression or a descriptor like @daily.
	Cron string `bun:"cron" json:"cron"`
	// KeepLast keeps the N most recent backups.
	KeepLast int `bun:"keep_last" json:"keepLast"`
	// KeepDaily/Weekly/Monthly keep the newest backup of each of the last N
	// days/ISO weeks/months that have one.
	KeepDaily   int  `bun:"keep_daily" json:"keepDaily"`
	KeepWeekly  int  `bun:"keep_weekly" json:"keepWeekly"`
	KeepMonthly int  `bun:"keep_monthly" json:"keepMonthly"`
	Enabled     bool `bun:"enabled" json:"enabled"`
	// Verify runs a restore test of each backup the schedule makes.
	Verify    bool      `bun:"verify" json:"verify"`
	CreatedAt time.Time `bun:"created_at" json:"createdAt"`
}

type APIToken struct {
	bun.BaseModel `bun:"table:api_tokens,alias:token" json:"-"`

	ID   string `bun:"id,pk" json:"id"`
	Name string `bun:"name" json:"name"`
	// UserID owns the token, which never does more than its owner may.
	UserID    string `bun:"user_id,nullzero" json:"userId"`
	TokenHash string `bun:"token_hash" json:"-"`
	// Restricted limits the token to Features and Grants (project IDs to
	// permissions), within its owner's; otherwise it has all of them.
	Restricted bool                `bun:"restricted" json:"restricted"`
	Features   []string            `bun:"features" json:"features"`
	Grants     map[string][]string `bun:"grants" json:"grants"`
	CreatedAt  time.Time           `bun:"created_at" json:"createdAt"`
	LastUsedAt *time.Time          `bun:"last_used_at" json:"lastUsedAt,omitempty"`
}

// Domain is a base domain (example.com) offered in the UI when giving a
// service a public domain. Services store their full domain, not a reference.
type Domain struct {
	bun.BaseModel `bun:"table:domains,alias:domain" json:"-"`

	ID   string `bun:"id,pk" json:"id"`
	Name string `bun:"name" json:"name"`
	// Proxied puts managed DNS records behind Cloudflare's proxy.
	Proxied   bool      `bun:"proxied" json:"proxied"`
	CreatedAt time.Time `bun:"created_at" json:"createdAt"`
}

// NotificationChannel is one destination for notifications. Config is
// specific to the kind (see internal/notify) and may hold secrets.
type NotificationChannel struct {
	bun.BaseModel `bun:"table:notification_channels,alias:channel" json:"-"`

	ID     string            `bun:"id,pk" json:"id"`
	Name   string            `bun:"name" json:"name"`
	Kind   string            `bun:"kind" json:"kind"`
	Config map[string]string `bun:"config" json:"config"`
	// Events lists the event types sent to this channel.
	Events    []string  `bun:"events" json:"events"`
	Enabled   bool      `bun:"enabled" json:"enabled"`
	CreatedAt time.Time `bun:"created_at" json:"createdAt"`
}

type GitProviderKind string

const (
	GitHub GitProviderKind = "github"
	GitLab GitProviderKind = "gitlab"
	Gitea  GitProviderKind = "gitea"
)

// EmailProvider is an SMTP server email goes out through.
type EmailProvider struct {
	bun.BaseModel `bun:"table:email_providers,alias:ep" json:"-"`

	ID       string `bun:"id,pk" json:"id"`
	Name     string `bun:"name" json:"name"`
	Host     string `bun:"host" json:"host"`
	Port     int    `bun:"port" json:"port"`
	Security string `bun:"security" json:"security"`
	Username string `bun:"username" json:"username"`
	Password string `bun:"password" json:"password"`
	// From is the sender, an address with an optional name.
	From      string    `bun:"sender" json:"from"`
	CreatedAt time.Time `bun:"created_at" json:"createdAt"`
}

// SSOProvider is an OpenID Connect provider users sign in with.
type SSOProvider struct {
	bun.BaseModel `bun:"table:sso_providers,alias:sso" json:"-"`

	ID string `bun:"id,pk" json:"id"`
	// Name labels the sign-in button.
	Name         string    `bun:"name" json:"name"`
	Issuer       string    `bun:"issuer" json:"issuer"`
	ClientID     string    `bun:"client_id" json:"clientId"`
	ClientSecret string    `bun:"client_secret" json:"clientSecret"`
	CreatedAt    time.Time `bun:"created_at" json:"createdAt"`
}

// SSOIdentity links a user to their subject at an SSO provider.
type SSOIdentity struct {
	bun.BaseModel `bun:"table:sso_identities,alias:ssoi" json:"-"`

	ID         string `bun:"id,pk" json:"id"`
	UserID     string `bun:"user_id" json:"-"`
	ProviderID string `bun:"provider_id" json:"providerId"`
	Subject    string `bun:"subject" json:"-"`
	// Email is the provider's, as of the last sign-in, to tell identities apart.
	Email      string     `bun:"email" json:"email"`
	CreatedAt  time.Time  `bun:"created_at" json:"createdAt"`
	LastUsedAt *time.Time `bun:"last_used_at" json:"lastUsedAt,omitempty"`
}

// GitProvider gives access to the private repositories of a forge account:
// a GitHub App installation, or a GitLab or Gitea OAuth authorization.
// Credentials read back masked.
type GitProvider struct {
	bun.BaseModel `bun:"table:git_providers,alias:git_provider" json:"-"`

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

// Registry holds the credentials pulls from one image registry host use.
type Registry struct {
	bun.BaseModel `bun:"table:registries,alias:registry" json:"-"`

	ID string `bun:"id,pk" json:"id"`
	// Host as image references name it: docker.io for Docker Hub.
	Host     string `bun:"host" json:"host"`
	Username string `bun:"username" json:"username"`
	// Password is a password or access token; it reads back masked.
	Password  string    `bun:"password" json:"password"`
	CreatedAt time.Time `bun:"created_at" json:"createdAt"`
}

// SecretConnection is a password manager account the manager is logged
// into. Kind is the provider (internal/secrets); Name is "" for the default
// connection of its kind, which the plain scheme references (pass://), else
// the suffix of its scheme (pass+work://).
type SecretConnection struct {
	bun.BaseModel `bun:"table:secret_connections,alias:secret_connection" json:"-"`

	ID   string `bun:"id,pk" json:"id"`
	Kind string `bun:"kind" json:"kind"`
	Name string `bun:"name" json:"name"`
	// Token logs in again when the session is lost or expires; never sent
	// to clients.
	Token     string    `bun:"token" json:"-"`
	CreatedAt time.Time `bun:"created_at" json:"createdAt"`
}

// Network is a bridge network created by hand on a server, which services
// of any project on that server can join to reach each other.
type Network struct {
	bun.BaseModel `bun:"table:networks,alias:network" json:"-"`

	ID        string    `bun:"id,pk" json:"id"`
	ServerID  string    `bun:"server_id" json:"serverId"`
	Name      string    `bun:"name" json:"name"`
	CreatedAt time.Time `bun:"created_at" json:"createdAt"`
}

// CanvasPosition is where a node of the map canvas was dragged to.
// TagColor is the color a user picked for a tag, one of core's palette.
type TagColor struct {
	bun.BaseModel `bun:"table:tag_colors,alias:tag_color" json:"-"`

	Name      string    `bun:"name,pk" json:"name"`
	Color     string    `bun:"color" json:"color"`
	UpdatedAt time.Time `bun:"updated_at" json:"-"`
}

type CanvasPosition struct {
	bun.BaseModel `bun:"table:canvas_positions,alias:canvas_position" json:"-"`

	// Node is "<kind>:<id>", e.g. svc:<service id>.
	Node      string    `bun:"node,pk" json:"node"`
	X         float64   `bun:"x" json:"x"`
	Y         float64   `bun:"y" json:"y"`
	UpdatedAt time.Time `bun:"updated_at" json:"-"`
}

// UptimeCheck requests a service's public URL on an interval and notifies
// when it goes down and when it recovers.
type UptimeCheck struct {
	bun.BaseModel `bun:"table:uptime_checks,alias:uptime" json:"-"`

	ServiceID   string `bun:"service_id,pk" json:"serviceId"`
	Path        string `bun:"path" json:"path"`
	IntervalSec int    `bun:"interval_sec" json:"intervalSec"`
	TimeoutSec  int    `bun:"timeout_sec" json:"timeoutSec"`
	// ExpectedStatus is the status that means up; 0 accepts any below 400.
	ExpectedStatus int  `bun:"expected_status" json:"expectedStatus"`
	Enabled        bool `bun:"enabled" json:"enabled"`
	// Down is the state last notified, since ChangedAt.
	Down      bool       `bun:"down" json:"down"`
	ChangedAt *time.Time `bun:"changed_at" json:"changedAt,omitempty"`
	// Recent are the last results, oldest first.
	Recent    []UptimeResult `bun:"recent" json:"-"`
	CreatedAt time.Time      `bun:"created_at" json:"createdAt"`
}

// UptimeResult is the outcome of one check.
type UptimeResult struct {
	At time.Time `json:"at"`
	OK bool      `json:"ok"`
	// Status is 0 when no response arrived.
	Status    int    `json:"status,omitempty"`
	LatencyMS int64  `json:"latencyMs"`
	Error     string `json:"error,omitempty"`
}

// UptimeHour counts the checks of a service in one hour.
type UptimeHour struct {
	bun.BaseModel `bun:"table:uptime_hours,alias:uptime_hour" json:"-"`

	ServiceID string    `bun:"service_id,pk" json:"-"`
	Hour      time.Time `bun:"hour,pk" json:"hour"`
	Checks    int       `bun:"checks" json:"checks"`
	Failures  int       `bun:"failures" json:"failures"`
	// LatencyMS sums the response times of the successful checks.
	LatencyMS int64 `bun:"latency_ms" json:"latencyMs"`
}

type AuditEntry struct {
	bun.BaseModel `bun:"table:audit_log,alias:audit" json:"-"`

	ID string `bun:"id,pk" json:"id"`
	// Actor is "user:<name>" or "token:<name>".
	Actor     string    `bun:"actor" json:"actor"`
	Action    string    `bun:"action" json:"action"`
	Target    string    `bun:"target" json:"target"`
	Status    int       `bun:"status" json:"status"`
	Error     string    `bun:"error" json:"error,omitempty"`
	CreatedAt time.Time `bun:"created_at" json:"createdAt"`
}

const LocalServerID = "local"

type ServerKind string

const (
	ServerLocal ServerKind = "local"
	ServerSSH   ServerKind = "ssh"
)

// Server is a Docker host: the manager's own, or a remote one reached over SSH.
type Server struct {
	bun.BaseModel `bun:"table:servers,alias:server" json:"-"`

	ID      string     `bun:"id,pk" json:"id"`
	Name    string     `bun:"name" json:"name"`
	Kind    ServerKind `bun:"kind" json:"kind"`
	Host    string     `bun:"host" json:"host"`
	Port    int        `bun:"port" json:"port"`
	SSHUser string     `bun:"ssh_user" json:"sshUser"`
	Socket  string     `bun:"socket" json:"socket"`
	HostKey string     `bun:"host_key" json:"hostKey"`
	// PublicIP is where managed DNS records point for apps on this server.
	PublicIP string `bun:"public_ip" json:"publicIp"`
	// TunnelToken, when set, serves the server's apps through that
	// Cloudflare tunnel instead of public ports. Never sent to clients.
	TunnelToken string    `bun:"tunnel_token" json:"-"`
	CreatedAt   time.Time `bun:"created_at" json:"createdAt"`
}
