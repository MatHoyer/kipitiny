package core

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

	"golang.org/x/crypto/ssh"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const sshKeySetting = "ssh_private_key"

var sshUserRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// managerKey returns the manager's SSH key, generating it on first use. Its
// public half goes into ~/.ssh/authorized_keys on every remote server.
func (c *Core) managerKey(ctx context.Context) (ssh.Signer, error) {
	pemText, err := c.store.GetSetting(ctx, sshKeySetting)
	if errors.Is(err, store.ErrNotFound) {
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
func (c *Core) SSHPublicKey(ctx context.Context) (string, error) {
	signer, err := c.managerKey(ctx)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) + " kipitiny", nil
}

// connectServer builds the Docker client of a remote server (used by the pool).
func (c *Core) connectServer(id string) (*docker.Client, error) {
	ctx := context.WithoutCancel(c.bg)
	sv, err := c.store.GetServer(ctx, id)
	if err != nil {
		return nil, err
	}
	if sv.Kind != store.ServerSSH {
		return nil, fmt.Errorf("server %s is not remote", sv.Name)
	}
	signer, err := c.managerKey(ctx)
	if err != nil {
		return nil, err
	}
	return docker.NewSSH(docker.SSHConfig{
		Host: sv.Host, Port: sv.Port, User: sv.SSHUser, Socket: sv.Socket, Signer: signer,
		HostKey: sv.HostKey,
		// Trust on first use: pin the key the first connection saw.
		OnNewHostKey: func(key string) error {
			sv.HostKey = strings.TrimSpace(key)
			_, err := c.store.UpdateServer(ctx, sv)
			c.log.Info("pinned SSH host key", "server", sv.Name, "key", sv.HostKey)
			return err
		},
	})
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
	store.Server
	// DetectedIP is the public IP found when none is set.
	DetectedIP  string       `json:"detectedIp,omitempty"`
	Docker      *docker.Info `json:"docker,omitempty"`
	DockerError string       `json:"dockerError,omitempty"`
	Projects    int          `json:"projects"`
}

// ListServers returns every server with a live status (a short timeout per
// server, queried in parallel).
func (c *Core) ListServers(ctx context.Context) ([]ServerView, error) {
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
		views[i] = ServerView{Server: sv, DetectedIP: c.DetectedPublicIP(sv.ID)}
		for _, p := range projects {
			if p.ServerID == sv.ID {
				views[i].Projects++
			}
		}
		go func() {
			defer func() { done <- struct{}{} }()
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if info, err := c.dockerFor(sv.ID).Info(ctx); err != nil {
				views[i].DockerError = err.Error()
			} else {
				views[i].Docker = &info
			}
		}()
	}
	for range servers {
		<-done
	}
	return views, nil
}

// CreateServer registers a remote server, connects to it (pinning its host
// key) and prepares it (proxy network, Traefik). A server that can't be
// reached is not saved.
func (c *Core) CreateServer(ctx context.Context, in ServerInput) (ServerView, error) {
	sv := store.Server{Kind: store.ServerSSH}
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
	c.kick() // start watching its events
	return view, nil
}

func (c *Core) UpdateServer(ctx context.Context, id string, in ServerInput) (ServerView, error) {
	sv, err := c.store.GetServer(ctx, id)
	if err != nil {
		return ServerView{}, err
	}
	if sv.Kind != store.ServerSSH {
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
func (c *Core) DeleteServer(ctx context.Context, id string) error {
	sv, err := c.store.GetServer(ctx, id)
	if err != nil {
		return err
	}
	if sv.Kind != store.ServerSSH {
		return fmt.Errorf("%w: this server can't be removed", ErrInvalid)
	}
	if err := c.store.DeleteServer(ctx, id); errors.Is(err, store.ErrConflict) {
		return fmt.Errorf("%w: %v; move or delete them first", ErrInvalid, err)
	} else if err != nil {
		return err
	}
	dk := c.dockerFor(id)
	if cts, err := dk.ListContainers(ctx, map[string]string{docker.LabelComponent: traefikComponent}); err == nil {
		for _, ct := range cts {
			_ = dk.RemoveContainer(ctx, ct.ID, 10*time.Second)
		}
		_ = dk.RemoveNetwork(ctx, docker.ProxyNetwork)
	}
	c.pool.Invalidate(id)
	c.probes.Delete(id)
	return nil
}

func (c *Core) checkServer(ctx context.Context, sv store.Server) (ServerView, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	info, err := c.dockerFor(sv.ID).Info(ctx)
	if err != nil {
		return ServerView{}, fmt.Errorf("%w: cannot reach Docker on %s: %v", ErrInvalid, sv.Name, err)
	}
	if sv, err = c.store.GetServer(ctx, sv.ID); err != nil { // host key may have been pinned
		return ServerView{}, err
	}
	if err := c.bootstrapServer(ctx, sv); err != nil {
		return ServerView{}, fmt.Errorf("%w: prepare %s: %v", ErrInvalid, sv.Name, err)
	}
	return ServerView{Server: sv, Docker: &info}, nil
}

func applyServerInput(sv *store.Server, in ServerInput) error {
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
