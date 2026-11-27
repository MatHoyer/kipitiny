package servers

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"golang.org/x/crypto/ssh"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/progress"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
)

const sshKeySetting = "ssh_private_key"

var sshUserRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// managerKey returns the manager's SSH key, generating it on first use. Its
// public half goes into ~/.ssh/authorized_keys on every remote server.
func (c *Servers) managerKey(ctx context.Context) (ssh.Signer, error) {
	pemText, err := c.store.GetSetting(ctx, sshKeySetting)
	if errors.Is(err, domain.ErrNotFound) {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		block, err := ssh.MarshalPrivateKey(priv, "kipitiny")
		if err != nil {
			return nil, err
		}
		pemText = string(pem.EncodeToMemory(block))
		if err := c.store.SetSetting(ctx, sshKeySetting, pemText); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey([]byte(pemText))
}

// SSHPublicKey is the line to add to authorized_keys on remote servers.
func (c *Servers) SSHPublicKey(ctx context.Context) (string, error) {
	signer, err := c.managerKey(ctx)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) + " kipitiny", nil
}

// RemoteDocker is how the pool reaches a remote server's Docker daemon:
// its Unix socket through an SSH connection.
type RemoteDocker struct {
	Host   string
	Port   int
	User   string
	Socket string
	Signer ssh.Signer
	// HostKey pins the server's key (authorized_keys format). When empty the
	// first key seen is trusted and passed to OnNewHostKey to be pinned.
	HostKey      string
	OnNewHostKey func(key string) error
}

// RemoteDocker returns how to reach a remote server's daemon (for the pool).
func (c *Servers) RemoteDocker(id string) (RemoteDocker, error) {
	ctx := context.WithoutCancel(c.bg)
	sv, err := c.store.GetServer(ctx, id)
	if err != nil {
		return RemoteDocker{}, err
	}
	if sv.Kind != domain.ServerSSH {
		return RemoteDocker{}, fmt.Errorf("server %s is not remote", sv.Name)
	}
	signer, err := c.managerKey(ctx)
	if err != nil {
		return RemoteDocker{}, err
	}
	return RemoteDocker{
		Host: sv.Host, Port: sv.Port, User: sv.SSHUser, Socket: sv.Socket, Signer: signer,
		HostKey: sv.HostKey,
		// Trust on first use: pin the key the first connection saw.
		OnNewHostKey: func(key string) error {
			sv.HostKey = strings.TrimSpace(key)
			_, err := c.store.UpdateServer(ctx, sv)
			c.log.Info("pinned SSH host key", "server", sv.Name, "key", sv.HostKey)
			return err
		},
	}, nil
}

type ServerInput struct {
	Name    string `json:"name"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	SSHUser string `json:"sshUser"`
	Socket  string `json:"socket"`
	// ResetHostKey forgets the pinned host key (after a server reinstall).
	ResetHostKey bool `json:"resetHostKey"`
}

type ServerView struct {
	domain.Server
	// DetectedIP is the public IP found when none is set.
	DetectedIP string `json:"detectedIp,omitempty"`
	// Tunnel is where its Cloudflare tunnel token comes from: "server"
	// (set in the UI), "env" (KIPITINY_CLOUDFLARE_TUNNEL_TOKEN) or "".
	Tunnel      string             `json:"tunnel,omitempty"`
	Docker      *domain.DockerInfo `json:"docker,omitempty"`
	DockerError string             `json:"dockerError,omitempty"`
	Projects    int                `json:"projects"`
}

// ListServers returns every server with a live status (a short timeout per
// server, queried in parallel).
func (c *Servers) ListServers(ctx context.Context) ([]ServerView, error) {
	servers, err := c.store.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	projects, err := c.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]ServerView, len(servers))
	done := make(chan struct{})
	for i, sv := range servers {
		views[i] = c.serverView(sv)
		for _, p := range projects {
			if p.ServerID == sv.ID {
				views[i].Projects++
			}
		}
		go func() {
			defer func() { done <- struct{}{} }()
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if info, err := c.DockerFor(sv.ID).Info(ctx); err != nil {
				views[i].DockerError = err.Error()
			} else {
				views[i].Docker = &info
			}
		}()
	}
	for range servers {
		<-done
	}
	if identity.IsMember(ctx) { // how to reach a server is the admins'
		for i, v := range views {
			views[i] = ServerView{Server: domain.Server{ID: v.ID, Name: v.Name, Kind: v.Kind, PublicIP: v.PublicIP, CreatedAt: v.CreatedAt},
				DetectedIP: v.DetectedIP, Projects: v.Projects}
		}
	}
	return views, nil
}

// CreateServer registers a remote server, connects to it (pinning its host
// key) and prepares it (proxy network, Traefik). A server that can't be
// reached is not saved.
func (c *Servers) CreateServer(ctx context.Context, in ServerInput) (ServerView, error) {
	sv := domain.Server{Kind: domain.ServerSSH}
	if err := applyServerInput(&sv, in); err != nil {
		return ServerView{}, err
	}
	sv, err := c.store.CreateServer(ctx, sv)
	if err != nil {
		return ServerView{}, err
	}
	view, err := c.checkServer(ctx, sv)
	if err != nil {
		c.pool.Invalidate(sv.ID)
		_ = c.store.DeleteServer(context.WithoutCancel(ctx), sv.ID)
		return ServerView{}, err
	}
	c.Kick() // start watching its events
	return view, nil
}

func (c *Servers) UpdateServer(ctx context.Context, id string, in ServerInput) (ServerView, error) {
	sv, err := c.store.GetServer(ctx, id)
	if err != nil {
		return ServerView{}, err
	}
	if sv.Kind != domain.ServerSSH {
		return ServerView{}, fmt.Errorf("%w: this server can't be edited", ErrInvalid)
	}
	if err := applyServerInput(&sv, in); err != nil {
		return ServerView{}, err
	}
	if in.ResetHostKey {
		sv.HostKey = ""
	}
	if sv, err = c.store.UpdateServer(ctx, sv); err != nil {
		return ServerView{}, err
	}
	c.pool.Invalidate(sv.ID)
	return c.checkServer(ctx, sv)
}

// DeleteServer forgets a server without projects and removes what the
// manager put there (Traefik, proxy network).
func (c *Servers) DeleteServer(ctx context.Context, id string) error {
	sv, err := c.store.GetServer(ctx, id)
	if err != nil {
		return err
	}
	if sv.Kind != domain.ServerSSH {
		return fmt.Errorf("%w: this server can't be removed", ErrInvalid)
	}
	if err := c.store.DeleteServer(ctx, id); errors.Is(err, domain.ErrConflict) {
		return fmt.Errorf("%w: %v; move or delete them first", ErrInvalid, err)
	} else if err != nil {
		return err
	}
	dk := c.DockerFor(id)
	for _, component := range []string{network.TraefikComponent, network.TunnelComponent} {
		cts, err := dk.ListContainers(ctx, map[string]string{domain.LabelComponent: component})
		if err != nil {
			continue
		}
		for _, ct := range cts {
			_ = dk.RemoveContainer(ctx, ct.ID, 10*time.Second)
		}
		_ = dk.RemoveNetwork(ctx, domain.ProxyNetwork)
	}
	c.pool.Invalidate(id)
	c.probes.Delete(id)
	return nil
}

// Steps of saving a server, reported with progress.Report.
const (
	// ServerStepConnect: connecting over SSH (pinning the host key the
	// first time) and asking Docker for its version.
	ServerStepConnect = "connect"
	// ServerStepNetwork: creating the network apps and the proxy share.
	ServerStepNetwork = "network"
	// ServerStepTraefik: starting the reverse proxy.
	ServerStepTraefik = "traefik"
	// ServerStepTunnel: starting the Cloudflare tunnel, when one is set.
	ServerStepTunnel = "tunnel"
)

func (c *Servers) checkServer(ctx context.Context, sv domain.Server) (ServerView, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	progress.Report(ctx, ServerStepConnect)
	info, err := c.DockerFor(sv.ID).Info(ctx)
	if err != nil {
		return ServerView{}, fmt.Errorf("%w: cannot reach Docker on %s: %v", ErrInvalid, sv.Name, err)
	}
	if sv, err = c.store.GetServer(ctx, sv.ID); err != nil { // host key may have been pinned
		return ServerView{}, err
	}
	if err := c.bootstrapServer(ctx, sv); err != nil {
		return ServerView{}, fmt.Errorf("%w: prepare %s: %v", ErrInvalid, sv.Name, err)
	}
	v := c.serverView(sv)
	v.Docker = &info
	return v, nil
}

func applyServerInput(sv *domain.Server, in ServerInput) error {
	sv.Name = strings.TrimSpace(in.Name)
	sv.Host = strings.TrimSpace(in.Host)
	sv.Port = in.Port
	if sv.Port == 0 {
		sv.Port = 22
	}
	sv.SSHUser = strings.TrimSpace(in.SSHUser)
	if sv.SSHUser == "" {
		sv.SSHUser = "root"
	}
	sv.Socket = strings.TrimSpace(in.Socket)
	if sv.Socket == "" {
		sv.Socket = "/var/run/docker.sock"
	}
	switch {
	case sv.Name == "" || len(sv.Name) > 60:
		return fmt.Errorf("%w: name is required (max 60 characters)", ErrInvalid)
	case sv.Host == "" || strings.ContainsAny(sv.Host, " /@:") && net.ParseIP(sv.Host) == nil:
		return fmt.Errorf("%w: host must be a hostname or IP address", ErrInvalid)
	case sv.Port < 1 || sv.Port > 65535:
		return fmt.Errorf("%w: invalid SSH port", ErrInvalid)
	case !sshUserRe.MatchString(sv.SSHUser):
		return fmt.Errorf("%w: invalid SSH user", ErrInvalid)
	case !strings.HasPrefix(sv.Socket, "/"):
		return fmt.Errorf("%w: socket must be an absolute path", ErrInvalid)
	}
	return nil
}

// SetServerNetwork sets a server's public IP and tunnel
// (network.SetServerNetwork), returning the server as clients see it.
func (c *Servers) SetServerNetwork(ctx context.Context, id string, in network.ServerNetwork) (ServerView, error) {
	sv, err := c.mgr.SetServerNetwork(ctx, id, in)
	if err != nil {
		return ServerView{}, err
	}
	return c.serverView(sv), nil
}

// serverView is a server as clients see it, without live status.
func (c *Servers) serverView(sv domain.Server) ServerView {
	v := ServerView{Server: sv, DetectedIP: c.mgr.DetectedPublicIP(sv.ID)}
	switch {
	case sv.TunnelToken != "":
		v.Tunnel = "server"
	case sv.Kind == domain.ServerLocal && c.cfg.Tunnel.Token != "":
		v.Tunnel = "env"
	}
	return v
}
