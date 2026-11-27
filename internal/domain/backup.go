package domain

import (
	"context"
	"time"
)

// BackupRepository stores backup targets, backups, their schedules and restores.
type BackupRepository interface {
	ListBackupTargets(ctx context.Context) ([]BackupTarget, error)
	GetBackupTarget(ctx context.Context, id string) (BackupTarget, error)
	CreateBackupTarget(ctx context.Context, t BackupTarget) (BackupTarget, error)
	UpdateBackupTarget(ctx context.Context, t BackupTarget) (BackupTarget, error)
	// SetBackupTargetKey gives an unencrypted target an age key; ErrConflict
	// if it already has one (replacing it would orphan its backups).
	SetBackupTargetKey(ctx context.Context, id, identity, recipient string) error
	// DeleteBackupTarget returns ErrConflict while backups reference it.
	DeleteBackupTarget(ctx context.Context, id string) error
	// CreateBackup keeps b.CreatedAt when set (an adopted backup).
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
}

type BackupTargetKind string

const (
	BackupTargetLocal BackupTargetKind = "local"
	BackupTargetS3    BackupTargetKind = "s3"

	// LocalTargetID is the built-in local disk target.
	LocalTargetID = "local"
)

type BackupTarget struct {
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
	// SourceID is the backup an anonymized copy was made from, with the
	// rules (and salt) hashing to RulesHash; empty for other backups.
	SourceID  string `bun:"source_id" json:"sourceId,omitempty"`
	RulesHash string `bun:"rules_hash" json:"rulesHash,omitempty"`
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
	// Anonymized lists anonymized copies (of SourceID, when set) instead
	// of backups.
	Anonymized bool
	SourceID   string
	Limit      int
}

type Restore struct {
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
