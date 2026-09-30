package docker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/moby/moby/client"
	"golang.org/x/crypto/ssh"
)

// SSHConfig reaches a remote Docker daemon's Unix socket through an SSH
// connection (direct-streamlocal channel), so the manager needs no ssh
// binary and the daemon never listens on TCP.
type SSHConfig struct {
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

// NewSSH returns a client for a remote daemon. Nothing is dialed until the
// first request.
func NewSSH(cfg SSHConfig) (*Client, error) {
	d := &sshDialer{cfg: cfg}
	c, err := client.New(
		// The address is never dialed: every connection goes through SSH.
		client.WithHost("tcp://"+net.JoinHostPort(cfg.Host, "2375")),
		client.WithDialContext(d.DialContext),
	)
	if err != nil {
		return nil, err
	}
	return &Client{Client: c, closer: d}, nil
}

type sshDialer struct {
	cfg  SSHConfig
	mu   sync.Mutex
	conn *ssh.Client
}

func (d *sshDialer) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	for attempt := 0; ; attempt++ {
		conn, err := d.client(ctx)
		if err != nil {
			return nil, err
		}
		c, err := conn.Dial("unix", d.cfg.Socket)
		if err == nil {
			return c, nil
		}
		var open *ssh.OpenChannelError
		if errors.As(err, &open) {
			// sshd answered: forwarding is refused or the socket is missing.
			return nil, fmt.Errorf("sshd refused to open %s (%s): it needs AllowTcpForwarding yes or local, "+
				"and AllowStreamLocalForwarding yes, and the user access to the Docker socket", d.cfg.Socket, open.Message)
		}
		// A dropped SSH connection surfaces here: reconnect once.
		d.reset(conn)
		if attempt == 1 {
			return nil, fmt.Errorf("docker socket %s over ssh: %w", d.cfg.Socket, err)
		}
	}
}

func (d *sshDialer) client(ctx context.Context) (*ssh.Client, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn != nil {
		return d.conn, nil
	}
	addr := net.JoinHostPort(d.cfg.Host, strconv.Itoa(d.cfg.Port))
	var dialer net.Dialer
	tcp, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("ssh %s: %w", addr, err)
	}
	cc, chans, reqs, err := ssh.NewClientConn(tcp, addr, &ssh.ClientConfig{
		User:            d.cfg.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(d.cfg.Signer)},
		HostKeyCallback: d.hostKeyCallback,
		Timeout:         10 * time.Second,
	})
	if err != nil {
		tcp.Close()
		return nil, fmt.Errorf("ssh %s: %w", addr, err)
	}
	d.conn = ssh.NewClient(cc, chans, reqs)
	return d.conn, nil
}

func (d *sshDialer) hostKeyCallback(host string, remote net.Addr, key ssh.PublicKey) error {
	got := string(ssh.MarshalAuthorizedKey(key))
	if d.cfg.HostKey == "" {
		if d.cfg.OnNewHostKey != nil {
			if err := d.cfg.OnNewHostKey(got); err != nil {
				return err
			}
		}
		d.cfg.HostKey = got
		return nil
	}
	want, _, _, _, err := ssh.ParseAuthorizedKey([]byte(d.cfg.HostKey))
	if err != nil {
		return fmt.Errorf("pinned host key: %w", err)
	}
	return ssh.FixedHostKey(want)(host, remote, key)
}

func (d *sshDialer) reset(stale *ssh.Client) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn == stale {
		d.conn.Close()
		d.conn = nil
	}
}

func (d *sshDialer) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn == nil {
		return nil
	}
	err := d.conn.Close()
	d.conn = nil
	return err
}

// Pool hands out a client per server ID. The local daemon is always there;
// remote clients are built on demand and kept until invalidated.
type Pool struct {
	local   *Client
	connect func(id string) (*Client, error)

	mu      sync.Mutex
	clients map[string]*Client
}

func NewPool(local *Client, connect func(id string) (*Client, error)) *Pool {
	return &Pool{local: local, connect: connect, clients: map[string]*Client{}}
}

// Local is the daemon the manager runs next to.
func (p *Pool) Local() *Client { return p.local }

// For returns the client of a server. If it can't be built (unknown server,
// bad key), the returned client fails every call with that error.
func (p *Pool) For(id string) *Client {
	if id == "" || id == "local" {
		return p.local
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.clients[id]; ok {
		return c
	}
	c, err := p.connect(id)
	if err != nil {
		return unavailable(fmt.Errorf("server %s: %w", id, err))
	}
	p.clients[id] = c
	return c
}

// Invalidate drops a cached client (after its server changed or was deleted).
func (p *Pool) Invalidate(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.clients[id]; ok {
		c.Close()
		delete(p.clients, id)
	}
}

func unavailable(err error) *Client {
	c, _ := client.New(
		client.WithHost("tcp://unavailable:2375"),
		client.WithDialContext(func(context.Context, string, string) (net.Conn, error) { return nil, err }),
	)
	return &Client{Client: c}
}
