package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/moby/moby/client"

	"github.com/kipit-labs/kipitiny/internal/cloudflare"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"
)

const (
	tunnelName      = "kipitiny-cloudflared"
	TunnelComponent = "cloudflared"
)

// TunnelToken is a server's Cloudflare tunnel token: its own, or for the
// manager's server KIPITINY_CLOUDFLARE_TUNNEL_TOKEN. Empty: public ports.
func (c *Network) TunnelToken(sv domain.Server) string {
	if sv.TunnelToken != "" {
		return sv.TunnelToken
	}
	if sv.Kind == domain.ServerLocal {
		return c.cfg.Tunnel.Token
	}
	return ""
}

// BehindTunnel reports whether the manager's traffic arrives through a
// Cloudflare tunnel.
func (c *Network) BehindTunnel(ctx context.Context) bool {
	return c.cfg.Traefik.Enabled && c.ViaTunnel(ctx, domain.LocalServerID)
}

// ViaTunnel reports whether a server's public traffic comes through a
// Cloudflare tunnel ("" is the manager's server).
func (c *Network) ViaTunnel(ctx context.Context, serverID string) bool {
	if serverID == "" {
		serverID = domain.LocalServerID
	}
	sv, err := c.store.GetServer(ctx, serverID)
	if err != nil {
		return serverID == domain.LocalServerID && c.cfg.Tunnel.Token != ""
	}
	return c.TunnelToken(sv) != ""
}

// EnsureTunnel runs cloudflared on a server with a tunnel token, and removes
// it otherwise. It forwards to https://kipitiny-traefik:443; hostnames are
// routed by the DNS sync (with Cloudflare connected) or in the dashboard.
func (c *Network) EnsureTunnel(ctx context.Context, sv domain.Server) error {
	dk := c.mgr.DockerFor(sv.ID)
	token := c.TunnelToken(sv)
	existing, err := dk.ListContainers(ctx, map[string]string{domain.LabelComponent: TunnelComponent})
	if err != nil {
		return err
	}

	var hash string
	var opts domain.ContainerSpec
	if token != "" {
		opts = c.tunnelSpec(token)
		hash = SpecHash(opts.Image, opts.Cmd, opts.Env)
		opts.Labels[domain.LabelConfigHash] = hash
		if len(existing) == 1 && existing[0].Labels[domain.LabelConfigHash] == hash {
			if existing[0].State == domain.StateRunning {
				return nil
			}
			_, err := dk.ContainerStart(ctx, existing[0].ID, client.ContainerStartOptions{})
			return err
		}
	}
	for _, ct := range existing {
		c.log.Info("removing outdated cloudflared container", "id", ct.ID[:12])
		if err := dk.RemoveContainer(ctx, ct.ID, 10*time.Second); err != nil {
			return err
		}
	}
	if hash == "" {
		return nil
	}

	if err := dk.EnsureImage(ctx, opts.Image, c.mgr.RegistryAuth(ctx, opts.Image), ""); err != nil {
		return fmt.Errorf("pull %s: %w", opts.Image, err)
	}
	if _, err := dk.Run(ctx, opts); err != nil {
		return err
	}
	c.log.Info("cloudflare tunnel started", "server", sv.Name, "image", opts.Image)
	return nil
}

func (c *Network) tunnelSpec(token string) domain.ContainerSpec {
	return domain.ContainerSpec{
		Name:  tunnelName,
		Image: c.cfg.Tunnel.Image,
		Cmd:   []string{"tunnel", "--no-autoupdate", "run"},
		// The token is read from the environment, keeping it out of `ps`.
		Env: []string{"TUNNEL_TOKEN=" + token},
		Labels: map[string]string{
			domain.LabelManaged:   "true",
			domain.LabelComponent: TunnelComponent,
		},
		Restart:  domain.RestartPolicy{Name: domain.RestartUnlessStopped},
		Log:      true,
		Networks: map[string][]string{domain.ProxyNetwork: nil},
	}
}

// ServerNetwork is how a server's apps are reached from the internet.
type ServerNetwork struct {
	// PublicIP is where DNS records point; empty means detect it.
	PublicIP string `json:"publicIp"`
	// TunnelToken serves the apps through a Cloudflare tunnel; empty means
	// public ports. SecretMask keeps the current token.
	TunnelToken string `json:"tunnelToken"`
}

// SetServerNetwork sets a server's public IP and tunnel. The reconciler then
// starts or removes its cloudflared and adjusts its Traefik.
func (c *Network) SetServerNetwork(ctx context.Context, id string, in ServerNetwork) (domain.Server, error) {
	ip := strings.TrimSpace(in.PublicIP)
	if ip != "" {
		if a := net.ParseIP(ip); a == nil || a.To4() == nil {
			return domain.Server{}, fmt.Errorf("%w: %q is not an IPv4 address", domain.ErrInvalid, ip)
		}
	}
	sv, err := c.store.GetServer(ctx, id)
	if err != nil {
		return domain.Server{}, err
	}
	sv.PublicIP = ip
	if token := strings.TrimSpace(in.TunnelToken); token != identity.SecretMask {
		if token != "" {
			if _, err := cloudflare.ParseTunnelToken(token); err != nil {
				return domain.Server{}, fmt.Errorf("%w: %s", domain.ErrInvalid, err.Error())
			}
		}
		sv.TunnelToken = token
	}
	if sv, err = c.store.UpdateServer(ctx, sv); err != nil {
		return domain.Server{}, err
	}
	c.mgr.Kick()
	c.KickDNS()
	return sv, nil
}

// syncTunnelRoutes points each tunnel's routes for its hostnames at the
// Traefik of its server, keeping routes the user added. It returns the
// problems to show, if any.
func (c *Network) syncTunnelRoutes(ctx context.Context, cf *cloudflare.Client, wants map[string]Want) string {
	if !c.cfg.Traefik.Enabled {
		return ""
	}
	servers, err := c.store.ListServers(ctx)
	if err != nil {
		return err.Error()
	}
	var problems []string
	current := map[string]cloudflare.Tunnel{}
	for _, sv := range servers {
		token := c.TunnelToken(sv)
		if token == "" {
			continue
		}
		t, err := cloudflare.ParseTunnelToken(token)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", sv.Name, err))
			continue
		}
		current[t.ID] = t
		if err := c.syncTunnel(ctx, cf, t, wants); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", sv.Name, err))
		}
	}

	// A tunnel no server uses anymore keeps the routes the manager added
	// until they're removed here; it's forgotten once that worked.
	remembered := c.managedTunnels(ctx)
	for id, t := range remembered {
		if _, ok := current[id]; ok {
			continue
		}
		if err := c.syncTunnel(ctx, cf, t, nil); err != nil {
			c.log.Warn("dns: clean up routes of a former tunnel", "tunnel", id, "err", err)
			current[id] = t // retry next time
		}
	}
	c.saveManagedTunnels(ctx, current)
	return strings.Join(problems, "; ")
}

const cfTunnelsSetting = "cloudflare_tunnels" // tunnels with routes we added

func (c *Network) managedTunnels(ctx context.Context) map[string]cloudflare.Tunnel {
	out := map[string]cloudflare.Tunnel{}
	if v, err := c.store.GetSetting(ctx, cfTunnelsSetting); err == nil {
		_ = json.Unmarshal([]byte(v), &out)
	}
	return out
}

func (c *Network) saveManagedTunnels(ctx context.Context, ts map[string]cloudflare.Tunnel) {
	b, _ := json.Marshal(ts)
	if err := c.store.SetSetting(ctx, cfTunnelsSetting, string(b)); err != nil {
		c.log.Warn("cannot save managed tunnels", "err", err)
	}
}

func (c *Network) syncTunnel(ctx context.Context, cf *cloudflare.Client, t cloudflare.Tunnel, wants map[string]Want) error {
	rules, cfg, err := cf.TunnelIngress(ctx, t)
	if err != nil {
		if cloudflare.Forbidden(err) {
			return errors.New("the Cloudflare token can't edit this tunnel (it needs Account › Cloudflare Tunnel › Edit)")
		}
		return err
	}
	var hosts []string
	for host, w := range wants {
		if w.Tunnel == t.ID {
			hosts = append(hosts, host)
		}
	}
	next := tunnelRoutes(rules, hosts)
	if reflect.DeepEqual(normalize(rules), normalize(next)) {
		return nil
	}
	c.log.Info("dns: updating tunnel routes", "tunnel", t.ID, "hostnames", len(hosts))
	return cf.SetTunnelIngress(ctx, t, cfg, next)
}

// tunnelRoutes returns rules with one route per host to Traefik first, the
// user's own routes next, and a catch-all last (cloudflared requires one).
func tunnelRoutes(rules []cloudflare.Ingress, hosts []string) []cloudflare.Ingress {
	slices.Sort(hosts)
	next := make([]cloudflare.Ingress, 0, len(rules)+len(hosts)+1)
	for _, h := range hosts {
		next = append(next, cloudflare.Ingress{
			"hostname": h,
			"service":  traefikOrigin,
			// Traefik's certificate isn't for the public name; the hop is
			// local to the proxy network.
			"originRequest": map[string]any{"noTLSVerify": true, "originServerName": h},
		})
	}
	var catchAll cloudflare.Ingress
	for _, r := range rules {
		switch {
		case r.Service() == traefikOrigin && r.Hostname() != "":
			// ours: rebuilt above
		case r.Hostname() == "":
			catchAll = r
		default:
			next = append(next, r)
		}
	}
	if catchAll == nil {
		catchAll = cloudflare.Ingress{"service": "http_status:404"}
	}
	return append(next, catchAll)
}

// normalize round-trips rules through JSON so comparisons ignore Go types.
func normalize(rules []cloudflare.Ingress) any {
	b, _ := json.Marshal(rules)
	var v any
	_ = json.Unmarshal(b, &v)
	return v
}
