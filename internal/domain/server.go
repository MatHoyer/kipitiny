package domain

import (
	"context"
	"time"
)

// ServerRepository stores the Docker hosts the manager deploys to.
type ServerRepository interface {
	ListServers(ctx context.Context) ([]Server, error)
	GetServer(ctx context.Context, id string) (Server, error)
	CreateServer(ctx context.Context, s Server) (Server, error)
	UpdateServer(ctx context.Context, s Server) (Server, error)
	// DeleteServer returns ErrConflict while projects use it.
	DeleteServer(ctx context.Context, id string) error
}

const LocalServerID = "local"

type ServerKind string

const (
	ServerLocal ServerKind = "local"
	ServerSSH   ServerKind = "ssh"
)

// Server is a Docker host: the manager's own, or a remote one reached over SSH.
type Server struct {
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
