package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/domain/cloudflare"
	"github.com/kipit-labs/kipitiny/internal/progress"
)

const (
	cfTokenSetting = "cloudflare_api_token"
	cfZonesSetting = "cloudflare_zones" // zone names, for CertResolverTLS at startup
	dnsEvery       = 10 * time.Minute
	dnsDebounce    = 2 * time.Second
	// CertResolverDNS gets certificates through a Cloudflare DNS challenge:
	// it works behind Cloudflare's proxy, where the TLS challenge can't.
	CertResolverDNS = "letsencrypt-dns"
)

// traefikOrigin is where tunnel routes send traffic: Traefik on the proxy
// network, which cloudflared shares.
var traefikOrigin = "https://" + TraefikName + ":443"

// DNSStatus is the state of one hostname's managed DNS record.
type DNSStatus struct {
	State   string `json:"state"` // synced, warning, conflict, error
	Message string `json:"message,omitempty"`
}

// CloudflareView is the connection state shown in Settings.
type CloudflareView struct {
	Connected bool     `json:"connected"`
	Zones     []string `json:"zones"`
	Error     string   `json:"error,omitempty"`
	// Tunnels are the servers whose tunnel routes the manager keeps.
	Tunnels   []TunnelView `json:"tunnels"`
	TunnelErr string       `json:"tunnelError,omitempty"`
	SyncedAt  *time.Time   `json:"syncedAt,omitempty"`
}

type TunnelView struct {
	Server string `json:"server"`
	ID     string `json:"id"`
}

// DomainView adds what Cloudflare knows about a listed domain.
type DomainView struct {
	domain.Domain
	Cloudflare bool   `json:"cloudflare"`        // in a zone the token manages
	SSLMode    string `json:"sslMode,omitempty"` // the zone's SSL/TLS mode
}

type dnsState struct {
	syncing   sync.Mutex // one sync at a time: the loop's or a manual one
	mu        sync.Mutex
	loaded    bool
	token     string
	zones     []cloudflare.Zone
	sslModes  map[string]string // zone ID -> mode
	zonesErr  string
	tunnelErr string
	syncedAt  time.Time
	status    map[string]DNSStatus // hostname -> status
	ips       map[string]string    // server ID -> detected public IP
}

// cf returns the connection state, loading the token and last known zones
// from the store on first use.
func (c *Network) cf(ctx context.Context) *dnsState {
	s := &c.dns
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded {
		s.token, _ = c.store.GetSetting(ctx, cfTokenSetting)
		if names, err := c.store.GetSetting(ctx, cfZonesSetting); err == nil {
			var zs []string
			_ = json.Unmarshal([]byte(names), &zs)
			for _, n := range zs {
				s.zones = append(s.zones, cloudflare.Zone{ID: "cached:" + n, Name: n})
			}
		}
		s.loaded = true
	}
	return s
}

// onCloudflare reports whether host is in a zone the connected token manages.
func (c *Network) onCloudflare(ctx context.Context, host string) bool {
	s := c.cf(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := cloudflare.ZoneFor(s.zones, host)
	return s.token != "" && ok
}

// CertResolver picks how a router on serverID gets a certificate for domain:
// none behind the tunnel (Cloudflare serves it) or for .localhost, the DNS
// challenge for Cloudflare zones, the TLS challenge otherwise.
func (c *Network) CertResolver(ctx context.Context, serverID, domain string) string {
	switch {
	case c.ViaTunnel(ctx, serverID) || IsLocalDomain(domain):
		return ""
	case c.onCloudflare(ctx, domain):
		return CertResolverDNS
	default:
		return CertResolverTLS
	}
}

// behindCloudflare reports whether requests for host on serverID come
// through Cloudflare: always behind the tunnel, else when the manager keeps
// host's record proxied.
func (c *Network) behindCloudflare(ctx context.Context, serverID, host string) bool {
	if c.ViaTunnel(ctx, serverID) {
		return true
	}
	if !c.onCloudflare(ctx, host) {
		return false
	}
	domains, err := c.store.ListDomains(ctx)
	return err == nil && proxiedFor(domains, host)
}

// cfToken is the Cloudflare API token, or "" when not connected.
func (c *Network) cfToken(ctx context.Context) string {
	s := c.cf(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.token
}

func (c *Network) CloudflareStatus(ctx context.Context) CloudflareView {
	tunnels := []TunnelView{}
	if servers, err := c.store.ListServers(ctx); err == nil {
		for _, sv := range servers {
			if t, err := cloudflare.ParseTunnelToken(c.TunnelToken(sv)); err == nil {
				tunnels = append(tunnels, TunnelView{Server: sv.Name, ID: t.ID})
			}
		}
	}
	s := c.cf(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	v := CloudflareView{Connected: s.token != "", Zones: []string{}, Tunnels: tunnels, Error: s.zonesErr, TunnelErr: s.tunnelErr}
	if !v.Connected {
		return v
	}
	for _, z := range s.zones {
		v.Zones = append(v.Zones, z.Name)
	}
	slices.Sort(v.Zones)
	if !s.syncedAt.IsZero() {
		t := s.syncedAt
		v.SyncedAt = &t
	}
	return v
}

// ConnectCloudflare checks and saves an API token, then syncs DNS.
func (c *Network) ConnectCloudflare(ctx context.Context, token string) (CloudflareView, error) {
	token = strings.TrimSpace(token)
	zones, err := c.newDNS(token).Zones(ctx)
	if err != nil {
		return CloudflareView{}, fmt.Errorf("%w: %s", domain.ErrInvalid, err.Error())
	}
	if len(zones) == 0 {
		return CloudflareView{}, fmt.Errorf("%w: the token can't see any zone (it needs Zone › Zone › Read)", domain.ErrInvalid)
	}
	if err := c.store.SetSetting(ctx, cfTokenSetting, token); err != nil {
		return CloudflareView{}, err
	}
	s := c.cf(ctx)
	s.mu.Lock()
	s.token, s.zones, s.zonesErr = token, zones, ""
	s.mu.Unlock()
	c.saveZoneNames(ctx, zones)
	// The token's zones become domains: no need to type them in. Proxied
	// only where the SSL/TLS mode allows it, else sites loop on HTTPS.
	cf := c.newDNS(token)
	for _, z := range zones {
		mode, _ := cf.SSLMode(ctx, z.ID)
		d := domain.Domain{Name: z.Name, Proxied: mode == "full" || mode == "strict"}
		if _, err := c.store.CreateDomain(ctx, d); err != nil && !errors.Is(err, domain.ErrConflict) {
			c.log.Warn("cannot add Cloudflare zone as a domain", "zone", z.Name, "err", err)
		}
	}
	c.KickDNS()
	c.mgr.Kick() // Traefik gets the DNS challenge
	return c.CloudflareStatus(ctx), nil
}

// TestCloudflare checks the saved token can still list zones.
func (c *Network) TestCloudflare(ctx context.Context) error {
	token := c.cfToken(ctx)
	if token == "" {
		return fmt.Errorf("%w: Cloudflare isn't connected", domain.ErrInvalid)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	progress.Report(ctx, "zones")
	zones, err := c.newDNS(token).Zones(ctx)
	if err != nil {
		return fmt.Errorf("%w: %s", domain.ErrInvalid, err.Error())
	}
	if len(zones) == 0 {
		return fmt.Errorf("%w: the token can't see any zone anymore (it needs Zone › Zone › Read)", domain.ErrInvalid)
	}
	return nil
}

// DisconnectCloudflare forgets the token. Existing records stay in Cloudflare.
func (c *Network) DisconnectCloudflare(ctx context.Context) error {
	if err := c.store.SetSetting(ctx, cfTokenSetting, ""); err != nil {
		return err
	}
	s := c.cf(ctx)
	s.mu.Lock()
	s.token, s.zonesErr, s.tunnelErr, s.status = "", "", "", nil
	s.mu.Unlock()
	c.mgr.Kick()
	return nil
}

func (c *Network) saveZoneNames(ctx context.Context, zones []cloudflare.Zone) {
	names := make([]string, len(zones))
	for i, z := range zones {
		names[i] = z.Name
	}
	b, _ := json.Marshal(names)
	if err := c.store.SetSetting(ctx, cfZonesSetting, string(b)); err != nil {
		c.log.Warn("cannot save Cloudflare zones", "err", err)
	}
}

// DomainViews lists the domains with their Cloudflare state.
func (c *Network) DomainViews(ctx context.Context) ([]DomainView, error) {
	ds, err := c.store.ListDomains(ctx)
	if err != nil {
		return nil, err
	}
	s := c.cf(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]DomainView, len(ds))
	for i, d := range ds {
		out[i] = DomainView{Domain: d}
		if z, ok := cloudflare.ZoneFor(s.zones, d.Name); ok && s.token != "" {
			out[i].Cloudflare = true
			out[i].SSLMode = s.sslModes[z.ID]
		}
	}
	return out, nil
}

func (c *Network) SetDomainProxied(ctx context.Context, id string, proxied bool) (domain.Domain, error) {
	d, err := c.store.SetDomainProxied(ctx, id, proxied)
	if err == nil {
		c.KickDNS()
	}
	return d, err
}

// DNSStatusOf is the managed-record state of host, if the manager manages it.
func (c *Network) DNSStatusOf(ctx context.Context, host string) *DNSStatus {
	s := c.cf(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.status[host]; ok {
		return &st
	}
	return nil
}

func (c *Network) KickDNS() {
	select {
	case c.dnsKick <- struct{}{}:
	default:
	}
}
