package domain

import (
	"context"
	"time"
)

// JobRepository stores scheduled jobs and their runs.
type JobRepository interface {
	// ListJobs returns a service's jobs, or every job when it's empty.
	ListJobs(ctx context.Context, serviceID string) ([]Job, error)
	GetJob(ctx context.Context, id string) (Job, error)
	CreateJob(ctx context.Context, j Job) (Job, error)
	UpdateJob(ctx context.Context, j Job) (Job, error)
	DeleteJob(ctx context.Context, id string) error
	CreateJobRun(ctx context.Context, r JobRun) (JobRun, error)
	GetJobRun(ctx context.Context, id string) (JobRun, error)
	// ListJobRuns returns a job's runs, newest first; limit 0 returns all.
	ListJobRuns(ctx context.Context, jobID string, limit int) ([]JobRun, error)
	FinishJobRun(ctx context.Context, r JobRun) error
	DeleteJobRun(ctx context.Context, id string) error
}

// Job runs a command on a cron schedule in a one-off container of an app's
// deployed image, with its env, networks and volumes.
type Job struct {
	ID        string `bun:"id,pk" json:"id"`
	ServiceID string `bun:"service_id" json:"serviceId"`
	// Name is unique within the service.
	Name    string `bun:"name" json:"name"`
	Command string `bun:"command" json:"command"`
	// Cron is a standard 5-field expression or a descriptor like @daily.
	Cron       string    `bun:"cron" json:"cron"`
	TimeoutSec int       `bun:"timeout_sec" json:"timeoutSec"`
	Enabled    bool      `bun:"enabled" json:"enabled"`
	CreatedAt  time.Time `bun:"created_at" json:"createdAt"`
}

// JobRun is one run of a job. Its log is a file in the data directory.
type JobRun struct {
	ID     string   `bun:"id,pk" json:"id"`
	JobID  string   `bun:"job_id" json:"jobId"`
	Status OpStatus `bun:"status" json:"status"`
	// ExitCode is nil when the command never ran or was killed.
	ExitCode *int `bun:"exit_code" json:"exitCode,omitempty"`
	// TriggeredBy is who ran it now (user:<name> or token:<name>); empty for
	// the schedule.
	TriggeredBy string     `bun:"triggered_by" json:"triggeredBy,omitempty"`
	Error       string     `bun:"error" json:"error,omitempty"`
	CreatedAt   time.Time  `bun:"created_at" json:"createdAt"`
	FinishedAt  *time.Time `bun:"finished_at" json:"finishedAt,omitempty"`
}
