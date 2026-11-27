// Package domain holds the manager's entities, the errors they share and the
// ports adapters implement: one repository per area, and Store for them all.
// It imports only the standard library (and the anonymization rules).
package domain

import "context"

// Store is every repository, plus what spans them.
type Store interface {
	ProjectRepository
	ServiceRepository
	DeploymentRepository
	GitRepository
	UserRepository
	SessionRepository
	SSORepository
	SecretRepository
	BackupRepository
	JobRepository
	NetworkRepository
	NotificationRepository
	RegistryRepository
	MonitoringRepository
	ServerRepository

	// FailRunningOperations marks backups, restores and job runs left
	// running by a previous process as failed.
	FailRunningOperations(ctx context.Context, errMsg string) error
	GetSetting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
	// Snapshot writes a consistent copy of the whole store to path.
	Snapshot(ctx context.Context, path string) error
	Close() error
}
