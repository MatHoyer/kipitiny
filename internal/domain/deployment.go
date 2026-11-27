package domain

import (
	"context"
	"time"
)

// DeploymentRepository stores the deployments of services.
type DeploymentRepository interface {
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
}

type DeploymentStatus string

const (
	DeploymentRunning   DeploymentStatus = "running"
	DeploymentSucceeded DeploymentStatus = "succeeded"
	DeploymentFailed    DeploymentStatus = "failed"
	// DeploymentSkipped is a compose deploy not tried because a service it
	// depends on (healthy or completed) failed; the old version keeps running.
	DeploymentSkipped DeploymentStatus = "skipped"
)

type Deployment struct {
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
