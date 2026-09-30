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
	DeleteService(ctx context.Context, id string) error

	CreateDeployment(ctx context.Context, d Deployment) (Deployment, error)
	GetDeployment(ctx context.Context, id string) (Deployment, error)
	ListDeployments(ctx context.Context, serviceID string, limit int) ([]Deployment, error)
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

	ID        string    `bun:"id,pk" json:"id"`
	Name      string    `bun:"name" json:"name"`
	CreatedAt time.Time `bun:"created_at" json:"createdAt"`
	UpdatedAt time.Time `bun:"updated_at" json:"updatedAt"`
}

type ServiceKind string

const (
	ServiceKindApp      ServiceKind = "app"
	ServiceKindPostgres ServiceKind = "postgres"
)

type Service struct {
	bun.BaseModel `bun:"table:services,alias:service" json:"-"`

	ID        string      `bun:"id,pk" json:"id"`
	ProjectID string      `bun:"project_id" json:"projectId"`
	Name      string      `bun:"name" json:"name"`
	Kind      ServiceKind `bun:"kind" json:"kind"`
	Image     string      `bun:"image" json:"image"`
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
	DatabaseID string    `bun:"database_id" json:"databaseId"`
	CreatedAt  time.Time `bun:"created_at" json:"createdAt"`
	UpdatedAt  time.Time `bun:"updated_at" json:"updatedAt"`
}

type DeploymentStatus string

const (
	DeploymentRunning   DeploymentStatus = "running"
	DeploymentSucceeded DeploymentStatus = "succeeded"
	DeploymentFailed    DeploymentStatus = "failed"
)

type Deployment struct {
	bun.BaseModel `bun:"table:deployments,alias:deployment" json:"-"`

	ID         string           `bun:"id,pk" json:"id"`
	ServiceID  string           `bun:"service_id" json:"serviceId"`
	Status     DeploymentStatus `bun:"status" json:"status"`
	Image      string           `bun:"image" json:"image"`
	Error      string           `bun:"error" json:"error,omitempty"`
	CreatedAt  time.Time        `bun:"created_at" json:"createdAt"`
	FinishedAt *time.Time       `bun:"finished_at" json:"finishedAt,omitempty"`
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
	CreatedAt time.Time        `bun:"created_at" json:"createdAt"`
}

// OpStatus is the lifecycle of a background operation (backup, restore).
type OpStatus string

const (
	OpRunning   OpStatus = "running"
	OpSucceeded OpStatus = "succeeded"
	OpFailed    OpStatus = "failed"
)

type Backup struct {
	bun.BaseModel `bun:"table:backups,alias:backup" json:"-"`

	ID          string     `bun:"id,pk" json:"id"`
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
	PGVersion   string     `bun:"pg_version" json:"pgVersion"`
	DurationMS  int64      `bun:"duration_ms" json:"durationMs"`
	Error       string     `bun:"error" json:"error,omitempty"`
	CreatedAt   time.Time  `bun:"created_at" json:"createdAt"`
	FinishedAt  *time.Time `bun:"finished_at" json:"finishedAt,omitempty"`
}

type BackupFilter struct {
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
	KeepDaily   int       `bun:"keep_daily" json:"keepDaily"`
	KeepWeekly  int       `bun:"keep_weekly" json:"keepWeekly"`
	KeepMonthly int       `bun:"keep_monthly" json:"keepMonthly"`
	Enabled     bool      `bun:"enabled" json:"enabled"`
	CreatedAt   time.Time `bun:"created_at" json:"createdAt"`
}
