package domain

import (
	"context"
	"time"
)

// RegistryRepository stores the credentials of image registries.
type RegistryRepository interface {
	ListRegistries(ctx context.Context) ([]Registry, error)
	GetRegistry(ctx context.Context, id string) (Registry, error)
	CreateRegistry(ctx context.Context, r Registry) (Registry, error)
	UpdateRegistry(ctx context.Context, r Registry) (Registry, error)
	DeleteRegistry(ctx context.Context, id string) error
}

// Registry holds the credentials pulls from one image registry host use.
type Registry struct {
	ID string `bun:"id,pk" json:"id"`
	// Host as image references name it: docker.io for Docker Hub.
	Host     string `bun:"host" json:"host"`
	Username string `bun:"username" json:"username"`
	// Password is a password or access token; it reads back masked.
	Password  string    `bun:"password" json:"password"`
	CreatedAt time.Time `bun:"created_at" json:"createdAt"`
}
