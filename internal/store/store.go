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

	CreateService(ctx context.Context, s Service) (Service, error)
	GetService(ctx context.Context, id string) (Service, error)
	ListServices(ctx context.Context, projectID string) ([]Service, error)
	UpdateService(ctx context.Context, s Service) (Service, error)
	SetCurrentDeployment(ctx context.Context, serviceID, deploymentID string) error
	// SetDeploymentBuild records the image built for a deployment.
	SetDeploymentBuild(ctx context.Context, id, image, commit string, config Service) error
	SetServiceStopped(ctx context.Context, serviceID string, stopped bool) error
	// ListAllServices returns every service of every project.
	ListAllServices(ctx context.Context) ([]Service, error)
	DeleteService(ctx context.Context, id string) error

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
	GetUser(ctx context.Context, id string) (User, error)
	GetUserByUsername(ctx context.Context, username string) (User, error)
	// SetPassword updates the hash and revokes all of the user's sessions.
	SetPassword(ctx context.Context, userID, passwordHash string) error

	CreateSession(ctx context.Context, s Session) error
	// GetSession returns ErrNotFound for unknown or expired sessions.
	GetSession(ctx context.Context, tokenHash string) (Session, error)
	DeleteSession(ctx context.Context, tokenHash string) error
	DeleteExpiredSessions(ctx context.Context) error

	ListBackupTargets(ctx context.Context) ([]BackupTarget, error)
	GetBackupTarget(ctx context.Context, id string) (BackupTarget, error)
	CreateBackupTarget(ctx context.Context, t BackupTarget) (BackupTarget, error)
	UpdateBackupTarget(ctx context.Context, t BackupTarget) (BackupTarget, error)
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
	ListAPITokens(ctx context.Context) ([]APIToken, error)
	// GetAPITokenByHash also records the use.
	GetAPITokenByHash(ctx context.Context, hash string) (APIToken, error)
	DeleteAPIToken(ctx context.Context, id string) error

	CreateDomain(ctx context.Context, d Domain) (Domain, error)
	ListDomains(ctx context.Context) ([]Domain, error)
	SetDomainProxied(ctx context.Context, id string, proxied bool) (Domain, error)
	DeleteDomain(ctx context.Context, id string) error

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
	ServerID  string    `bun:"server_id" json:"serverId"`
	CreatedAt time.Time `bun:"created_at" json:"createdAt"`
	UpdatedAt time.Time `bun:"updated_at" json:"updatedAt"`
}

type ServiceSource string

const (
	SourceImage ServiceSource = "image"
	SourceGit   ServiceSource = "git"
)

type ServiceKind string

const (
	ServiceKindApp      ServiceKind = "app"
	ServiceKindPostgres ServiceKind = "postgres"
)

type Service struct {
	bun.BaseModel `bun:"table:services,alias:service" json:"-"`

	ID        string `bun:"id,pk" json:"id"`
	ProjectID string `bun:"project_id" json:"projectId"`
	// ServerID mirrors the project's server.
	ServerID string      `bun:"server_id" json:"serverId"`
	Name     string      `bun:"name" json:"name"`
	Kind     ServiceKind `bun:"kind" json:"kind"`
	Image    string      `bun:"image" json:"image"`
	// Replicas is always 1 for postgres services.
	Replicas int `bun:"replicas" json:"replicas"`
	// Port is the container port Traefik routes to; 0 when not public.
	Port int `bun:"port" json:"port"`
	// Domain is the public hostname; empty when not public.
	Domain string            `bun:"domain" json:"domain"`
	Env    map[string]string `bun:"env" json:"env"`
	// MemoryMB is the container memory limit; 0 means unlimited.
	MemoryMB int `bun:"memory_mb" json:"memoryMb"`
	// DatabaseID links an app to a postgres service of the same project.
	DatabaseID string `bun:"database_id" json:"databaseId"`
	// HealthPath is an HTTP path that must answer 2xx/3xx before a new
	// replica takes over; empty uses the image healthcheck or a stability wait.
	HealthPath string `bun:"health_path" json:"healthPath"`
	// PreDeploy runs once (sh -c) in a one-off container before a rollout,
	// e.g. migrations. A failure aborts the deploy.
	PreDeploy           string `bun:"pre_deploy" json:"preDeploy"`
	CurrentDeploymentID string `bun:"current_deployment_id" json:"currentDeploymentId"`
	// Stopped is the desired run state after the user stopped the service.
	Stopped bool `bun:"stopped" json:"stopped"`

	// Source is image (pull Image) or git (build GitURL's Dockerfile).
	Source    ServiceSource `bun:"source" json:"source"`
	GitURL    string        `bun:"git_url" json:"gitUrl"`
	GitBranch string        `bun:"git_branch" json:"gitBranch"`
	// GitToken authenticates HTTPS clones of private repositories.
	GitToken     string `bun:"git_token" json:"gitToken"`
	Dockerfile   string `bun:"dockerfile" json:"dockerfile"`
	BuildContext string `bun:"build_context" json:"buildContext"`
	// WebhookSecret authenticates push webhooks that trigger a deploy.
	WebhookSecret string    `bun:"webhook_secret" json:"-"`
	CreatedAt     time.Time `bun:"created_at" json:"createdAt"`
	UpdatedAt     time.Time `bun:"updated_at" json:"updatedAt"`
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
	// Kind is postgres (a database) or manager (the manager's own state).
	Kind        BackupKind `bun:"kind" json:"kind"`
	ServiceID   string     `bun:"service_id" json:"serviceId"`
	ProjectID   string     `bun:"project_id" json:"projectId"`
	ServiceName string     `bun:"service_name" json:"serviceName"`
	ProjectName string     `bun:"project_name" json:"projectName"`
	TargetID    string     `bun:"target_id" json:"targetId"`
	ScheduleID  string     `bun:"schedule_id" json:"scheduleId,omitempty"`
	ObjectKey   string     `bun:"object_key" json:"objectKey"`
	Status      OpStatus   `bun:"status" json:"status"`
	SizeBytes   int64      `bun:"size_bytes" json:"sizeBytes"`
	SHA256      string     `bun:"sha256" json:"sha256"`
	Encrypted   bool       `bun:"encrypted" json:"encrypted"`
	PGVersion   string     `bun:"pg_version" json:"pgVersion"`
	DurationMS  int64      `bun:"duration_ms" json:"durationMs"`
	Error       string     `bun:"error" json:"error,omitempty"`
	CreatedAt   time.Time  `bun:"created_at" json:"createdAt"`
	FinishedAt  *time.Time `bun:"finished_at" json:"finishedAt,omitempty"`
	Verification
}

// Verification is the result of restoring a backup into a throwaway server.
type Verification struct {
	VerifyStatus  OpStatus            `bun:"verify_status" json:"verifyStatus,omitempty"`
	VerifyError   string              `bun:"verify_error" json:"verifyError,omitempty"`
	VerifyDetails VerificationDetails `bun:"verify_details" json:"verifyDetails"`
	VerifiedAt    *time.Time          `bun:"verified_at" json:"verifiedAt,omitempty"`
}

type VerificationDetails struct {
	Tables     int   `json:"tables"`
	Rows       int64 `json:"rows"`
	DBBytes    int64 `json:"dbBytes"`
	DurationMS int64 `json:"durationMs"`
}

type BackupKind string

const (
	BackupKindPostgres BackupKind = "postgres"
	BackupKindManager  BackupKind = "manager"
)

type BackupFilter struct {
	Kind       BackupKind
	ServiceID  string
	ScheduleID string
	ProjectID  string
	Status     OpStatus
	Limit      int
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
}

// BackupSchedule backs up a database on a cron schedule and prunes older
// backups it made. A backup is kept if any Keep* rule selects it.
type BackupSchedule struct {
	bun.BaseModel `bun:"table:backup_schedules,alias:schedule" json:"-"`

	ID        string `bun:"id,pk" json:"id"`
	ServiceID string `bun:"service_id" json:"serviceId"`
	TargetID  string `bun:"target_id" json:"targetId"`
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

type Scope string

const (
	ScopeRead   Scope = "read"   // look only
	ScopeDeploy Scope = "deploy" // plus deploy, rollback, start/stop, backups
	ScopeAdmin  Scope = "admin"  // everything
)

// Allows reports whether a holder of s may do what needs want.
func (s Scope) Allows(want Scope) bool {
	rank := map[Scope]int{ScopeRead: 1, ScopeDeploy: 2, ScopeAdmin: 3}
	return rank[s] >= rank[want] && rank[want] > 0
}

type APIToken struct {
	bun.BaseModel `bun:"table:api_tokens,alias:token" json:"-"`

	ID         string     `bun:"id,pk" json:"id"`
	Name       string     `bun:"name" json:"name"`
	TokenHash  string     `bun:"token_hash" json:"-"`
	Scope      Scope      `bun:"scope" json:"scope"`
	CreatedAt  time.Time  `bun:"created_at" json:"createdAt"`
	LastUsedAt *time.Time `bun:"last_used_at" json:"lastUsedAt,omitempty"`
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
