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

	Close() error
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
	Domain    string            `bun:"domain" json:"domain"`
	Env       map[string]string `bun:"env" json:"env"`
	CreatedAt time.Time         `bun:"created_at" json:"createdAt"`
	UpdatedAt time.Time         `bun:"updated_at" json:"updatedAt"`
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
