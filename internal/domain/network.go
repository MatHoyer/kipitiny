package domain

import (
	"context"
	"time"
)

// NetworkRepository stores domains, user networks and maintenance pages.
type NetworkRepository interface {
	CreateDomain(ctx context.Context, d Domain) (Domain, error)
	ListDomains(ctx context.Context) ([]Domain, error)
	SetDomainProxied(ctx context.Context, id string, proxied bool) (Domain, error)
	DeleteDomain(ctx context.Context, id string) error
	ListNetworks(ctx context.Context) ([]Network, error)
	GetNetwork(ctx context.Context, id string) (Network, error)
	CreateNetwork(ctx context.Context, n Network) (Network, error)
	DeleteNetwork(ctx context.Context, id string) error
	// SetProjectMaintenance replaces a project's maintenance page and mode.
	SetProjectMaintenance(ctx context.Context, projectID string, m Maintenance) error
}

// Domain is a base domain (example.com) offered in the UI when giving a
// service a public domain. Services store their full domain, not a reference.
type Domain struct {
	ID   string `bun:"id,pk" json:"id"`
	Name string `bun:"name" json:"name"`
	// Proxied puts managed DNS records behind Cloudflare's proxy.
	Proxied   bool      `bun:"proxied" json:"proxied"`
	CreatedAt time.Time `bun:"created_at" json:"createdAt"`
}

// Network is a bridge network created by hand on a server, which services
// of any project on that server can join to reach each other.
type Network struct {
	ID        string    `bun:"id,pk" json:"id"`
	ServerID  string    `bun:"server_id" json:"serverId"`
	Name      string    `bun:"name" json:"name"`
	CreatedAt time.Time `bun:"created_at" json:"createdAt"`
}
