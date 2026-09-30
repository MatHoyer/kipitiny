package core

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MatHoyer/kipitiny/internal/cloudflare"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	cfTokenSetting = "cloudflare_api_token"
	cfZonesSetting = "cloudflare_zones" // zone names, for certResolver at startup
	dnsEvery       = 10 * time.Minute
	dnsDebounce    = 2 * time.Second
	// certResolverDNS gets certificates through a Cloudflare DNS challenge:
	// it works behind Cloudflare's proxy, where the TLS challenge can't.
	certResolverDNS = "letsencrypt-dns"
)

// traefikOrigin is where tunnel routes send traffic: Traefik on the proxy
// network, which cloudflared shares.
var traefikOrigin = "https://" + traefikName + ":443"

// DNSStatus is the state of one hostname's managed DNS record.
type DNSStatus struct {
	State   string `json:"state"` // synced, conflict, error
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
	store.Domain
	Cloudflare bool   `json:"cloudflare"`        // in a zone the token manages
	SSLMode    string `json:"sslMode,omitempty"` // the zone's SSL/TLS mode
}

type dnsState struct {
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
func (c *Core) cf(ctx context.Context) *dnsState {
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
func (c *Core) onCloudflare(ctx context.Context, host string) bool {
	s := c.cf(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := cloudflare.ZoneFor(s.zones, host)
	return s.token != "" && ok
}

// certResolver picks how a router on serverID gets a certificate for domain:
// none behind the tunnel (Cloudflare serves it) or for .localhost, the DNS
// challenge for Cloudflare zones, the TLS challenge otherwise.
func (c *Core) certResolver(ctx context.Context, serverID, domain string) string {
	switch {
	case c.viaTunnel(ctx, serverID) || isLocalDomain(domain):
		return ""
	case c.onCloudflare(ctx, domain):
		return certResolverDNS
	default:
		return certResolver
	}
}

// cfToken is the Cloudflare API token, or "" when not connected.
func (c *Core) cfToken(ctx context.Context) string {
	s := c.cf(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.token
}

func (c *Core) CloudflareStatus(ctx context.Context) CloudflareView {
	tunnels := []TunnelView{}
	if servers, err := c.store.ListServers(ctx); err == nil {
		for _, sv := range servers {
			if t, err := cloudflare.ParseTunnelToken(c.tunnelToken(sv)); err == nil {
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
func (c *Core) ConnectCloudflare(ctx context.Context, token string) (CloudflareView, error) {
	token = strings.TrimSpace(token)
	zones, err := cloudflare.New(token).Zones(ctx)
	if err != nil {
		return CloudflareView{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if len(zones) == 0 {
		return CloudflareView{}, fmt.Errorf("%w: the token can't see any zone (it needs Zone › Zone › Read)", ErrInvalid)
	}
	if err := c.store.SetSetting(ctx, cfTokenSetting, token); err != nil {
		return CloudflareView{}, err
	}
	s := c.cf(ctx)
	s.mu.Lock()
	s.token, s.zones, s.zonesErr = token, zones, ""
	s.mu.Unlock()
	c.saveZoneNames(ctx, zones)
	c.kickDNS()
	c.kick() // Traefik gets the DNS challenge
	return c.CloudflareStatus(ctx), nil
}

// DisconnectCloudflare forgets the token. Existing records stay in Cloudflare.
func (c *Core) DisconnectCloudflare(ctx context.Context) error {
	if err := c.store.SetSetting(ctx, cfTokenSetting, ""); err != nil {
		return err
	}
	s := c.cf(ctx)
	s.mu.Lock()
	s.token, s.zonesErr, s.tunnelErr, s.status = "", "", "", nil
	s.mu.Unlock()
	c.kick()
	return nil
}

func (c *Core) saveZoneNames(ctx context.Context, zones []cloudflare.Zone) {
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
func (c *Core) DomainViews(ctx context.Context) ([]DomainView, error) {
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

func (c *Core) SetDomainProxied(ctx context.Context, id string, proxied bool) (store.Domain, error) {
	d, err := c.store.SetDomainProxied(ctx, id, proxied)
	if err == nil {
		c.kickDNS()
	}
	return d, err
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
func (c *Core) SetServerNetwork(ctx context.Context, id string, in ServerNetwork) (ServerView, error) {
	ip := strings.TrimSpace(in.PublicIP)
	if ip != "" {
		if a := net.ParseIP(ip); a == nil || a.To4() == nil {
			return ServerView{}, fmt.Errorf("%w: %q is not an IPv4 address", ErrInvalid, ip)
		}
	}
	sv, err := c.store.GetServer(ctx, id)
	if err != nil {
		return ServerView{}, err
	}
	sv.PublicIP = ip
	if token := strings.TrimSpace(in.TunnelToken); token != SecretMask {
		if token != "" {
			if _, err := cloudflare.ParseTunnelToken(token); err != nil {
				return ServerView{}, fmt.Errorf("%w: %v", ErrInvalid, err)
			}
		}
		sv.TunnelToken = token
	}
	if sv, err = c.store.UpdateServer(ctx, sv); err != nil {
		return ServerView{}, err
	}
	c.kick()
	c.kickDNS()
	return c.serverView(sv), nil
}

// serverView is a server as clients see it, without live status.
func (c *Core) serverView(sv store.Server) ServerView {
	v := ServerView{Server: sv, DetectedIP: c.DetectedPublicIP(sv.ID)}
	switch {
	case sv.TunnelToken != "":
		v.Tunnel = "server"
	case sv.Kind == store.ServerLocal && c.cfg.Tunnel.Token != "":
		v.Tunnel = "env"
	}
	return v
}

// dnsStatus is the managed-record state of host, if the manager manages it.
func (c *Core) dnsStatus(ctx context.Context, host string) *DNSStatus {
	s := c.cf(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.status[host]; ok {
		return &st
	}
	return nil
}

func (c *Core) kickDNS() {
	select {
	case c.dnsKick <- struct{}{}:
	default:
	}
}

// StartDNSSync keeps Cloudflare DNS records and tunnel routes in line with
// service domains: now, after changes, and every ten minutes.
func (c *Core) StartDNSSync() error {
	return c.goLoop(func() {
		timer := time.NewTimer(0)
		defer timer.Stop()
		for {
			select {
			case <-c.bg.Done():
				return
			case <-c.dnsKick:
				timer.Reset(dnsDebounce)
			case <-timer.C:
				if err := c.syncDNS(c.bg); err != nil {
					c.log.Warn("dns sync", "err", err)
				}
				timer.Reset(dnsEvery)
			}
		}
	})
}

// want is a DNS record the manager should hold.
type want struct {
	zone    cloudflare.Zone
	record  cloudflare.Record
	tunnel  string // ID of the tunnel serving it, if any
	problem string // why it can't be created
}

func (c *Core) syncDNS(ctx context.Context) error {
	token := c.cfToken(ctx)
	if token == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cf := cloudflare.New(token)
	s := &c.dns

	zones, err := cf.Zones(ctx)
	if err != nil {
		s.mu.Lock()
		s.zonesErr = err.Error()
		s.mu.Unlock()
		return err
	}
	c.saveZoneNames(ctx, zones)

	wants, err := c.wantedRecords(ctx, zones)
	if err != nil {
		return err
	}
	status := map[string]DNSStatus{}
	for host, w := range wants {
		status[host] = c.applyRecord(ctx, cf, w)
	}

	// Records of domains that went away (service deleted, domain changed).
	for _, z := range zones {
		recs, err := cf.ManagedRecords(ctx, z.ID)
		if err != nil {
			c.log.Warn("dns: list managed records", "zone", z.Name, "err", err)
			continue
		}
		for _, r := range recs {
			if _, ok := wants[r.Name]; !ok {
				c.log.Info("dns: removing record", "name", r.Name, "type", r.Type)
				if err := cf.DeleteRecord(ctx, z.ID, r.ID); err != nil {
					c.log.Warn("dns: remove record", "name", r.Name, "err", err)
				}
			}
		}
	}

	tunnelErr := c.syncTunnelRoutes(ctx, cf, wants)

	sslModes := map[string]string{}
	domains, _ := c.store.ListDomains(ctx)
	for _, d := range domains {
		if z, ok := cloudflare.ZoneFor(zones, d.Name); ok {
			if _, done := sslModes[z.ID]; !done {
				sslModes[z.ID], _ = cf.SSLMode(ctx, z.ID)
			}
		}
	}

	s.mu.Lock()
	s.zones, s.zonesErr, s.tunnelErr, s.status, s.sslModes, s.syncedAt = zones, "", tunnelErr, status, sslModes, time.Now()
	s.mu.Unlock()
	return nil
}

// wantedRecords computes the record every service domain (and the manager's
// own) in a Cloudflare zone should have.
func (c *Core) wantedRecords(ctx context.Context, zones []cloudflare.Zone) (map[string]want, error) {
	svcs, err := c.store.ListAllServices(ctx)
	if err != nil {
		return nil, err
	}
	servers, err := c.store.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[string]store.Server{}
	for _, sv := range servers {
		byID[sv.ID] = sv
	}
	domains, err := c.store.ListDomains(ctx)
	if err != nil {
		return nil, err
	}

	wants := map[string]want{}
	add := func(host, serverID string) {
		z, ok := cloudflare.ZoneFor(zones, host)
		if !ok || isLocalDomain(host) {
			return
		}
		w := want{zone: z, record: cloudflare.Record{Name: host, TTL: 1}}
		sv := byID[serverID]
		if token := c.tunnelToken(sv); token != "" {
			t, err := cloudflare.ParseTunnelToken(token)
			if err != nil {
				w.problem = fmt.Sprintf("%s's tunnel token: %v", sv.Name, err)
			}
			// Tunnel hostnames must be proxied.
			w.record.Type, w.record.Content, w.record.Proxied, w.tunnel = "CNAME", t.Target(), true, t.ID
		} else {
			ip, err := c.publicIP(ctx, sv)
			if err != nil {
				w.problem = err.Error()
			}
			w.record.Type, w.record.Content, w.record.Proxied = "A", ip, proxiedFor(domains, host)
		}
		wants[host] = w
	}
	for _, svc := range svcs {
		if svc.Domain != "" {
			add(svc.Domain, svc.ServerID)
		}
	}
	if c.cfg.Domain != "" && c.cfg.Traefik.Enabled {
		add(c.cfg.Domain, store.LocalServerID)
	}
	return wants, nil
}

// proxiedFor is the proxy setting of the listed domain covering host.
func proxiedFor(domains []store.Domain, host string) bool {
	best := ""
	proxied := false
	for _, d := range domains {
		if (host == d.Name || strings.HasSuffix(host, "."+d.Name)) && len(d.Name) > len(best) {
			best, proxied = d.Name, d.Proxied
		}
	}
	return proxied
}

// applyRecord makes Cloudflare hold w's record, never touching records the
// manager didn't create.
func (c *Core) applyRecord(ctx context.Context, cf *cloudflare.Client, w want) DNSStatus {
	if w.problem != "" {
		return DNSStatus{State: "error", Message: w.problem}
	}
	host := w.record.Name
	recs, err := cf.Records(ctx, w.zone.ID, host)
	if err != nil {
		return DNSStatus{State: "error", Message: err.Error()}
	}
	var mine []cloudflare.Record
	for _, r := range recs {
		switch {
		case r.Managed():
			mine = append(mine, r)
		case r.Type == "A" || r.Type == "AAAA" || r.Type == "CNAME":
			return DNSStatus{State: "conflict", Message: fmt.Sprintf(
				"%s already has a %s record kipitiny didn't create; delete it in Cloudflare to let kipitiny manage it", host, r.Type)}
		}
	}
	switch {
	case len(mine) == 0:
		c.log.Info("dns: creating record", "name", host, "type", w.record.Type, "content", w.record.Content)
		err = cf.CreateRecord(ctx, w.zone.ID, w.record)
	case mine[0].Type == w.record.Type:
		r := mine[0]
		if r.Content != w.record.Content || r.Proxied != w.record.Proxied {
			c.log.Info("dns: updating record", "name", host, "content", w.record.Content, "proxied", w.record.Proxied)
			err = cf.UpdateRecord(ctx, w.zone.ID, r.ID, w.record)
		}
		mine = mine[1:]
	default: // A <-> CNAME: a name can't hold both
		for _, r := range mine {
			if err = cf.DeleteRecord(ctx, w.zone.ID, r.ID); err != nil {
				break
			}
		}
		mine = nil
		if err == nil {
			err = cf.CreateRecord(ctx, w.zone.ID, w.record)
		}
	}
	for _, r := range mine { // duplicates
		_ = cf.DeleteRecord(ctx, w.zone.ID, r.ID)
	}
	if err != nil {
		if cloudflare.Forbidden(err) {
			return DNSStatus{State: "error", Message: "the Cloudflare token can't edit DNS here (it needs Zone › DNS › Edit)"}
		}
		return DNSStatus{State: "error", Message: err.Error()}
	}
	return DNSStatus{State: "synced"}
}

// syncTunnelRoutes points each tunnel's routes for its hostnames at the
// Traefik of its server, keeping routes the user added. It returns the
// problems to show, if any.
func (c *Core) syncTunnelRoutes(ctx context.Context, cf *cloudflare.Client, wants map[string]want) string {
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
		token := c.tunnelToken(sv)
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

func (c *Core) managedTunnels(ctx context.Context) map[string]cloudflare.Tunnel {
	out := map[string]cloudflare.Tunnel{}
	if v, err := c.store.GetSetting(ctx, cfTunnelsSetting); err == nil {
		_ = json.Unmarshal([]byte(v), &out)
	}
	return out
}

func (c *Core) saveManagedTunnels(ctx context.Context, ts map[string]cloudflare.Tunnel) {
	b, _ := json.Marshal(ts)
	if err := c.store.SetSetting(ctx, cfTunnelsSetting, string(b)); err != nil {
		c.log.Warn("cannot save managed tunnels", "err", err)
	}
}

func (c *Core) syncTunnel(ctx context.Context, cf *cloudflare.Client, t cloudflare.Tunnel, wants map[string]want) error {
	rules, cfg, err := cf.TunnelIngress(ctx, t)
	if err != nil {
		if cloudflare.Forbidden(err) {
			return errors.New("the Cloudflare token can't edit this tunnel (it needs Account › Cloudflare Tunnel › Edit)")
		}
		return err
	}
	var hosts []string
	for host, w := range wants {
		if w.tunnel == t.ID {
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

// publicIP is where records point for apps on sv: the configured address,
// else detected (and remembered for the process's lifetime).
func (c *Core) publicIP(ctx context.Context, sv store.Server) (string, error) {
	if sv.PublicIP != "" {
		return sv.PublicIP, nil
	}
	s := &c.dns
	s.mu.Lock()
	ip := s.ips[sv.ID]
	s.mu.Unlock()
	if ip != "" {
		return ip, nil
	}
	ip, err := detectPublicIP(ctx, sv)
	if err != nil {
		return "", fmt.Errorf("can't tell %s's public IP (%v): set it in Settings › Servers", sv.Name, err)
	}
	s.mu.Lock()
	if s.ips == nil {
		s.ips = map[string]string{}
	}
	s.ips[sv.ID] = ip
	s.mu.Unlock()
	return ip, nil
}

// DetectedPublicIP is the address detected for a server without one set.
func (c *Core) DetectedPublicIP(id string) string {
	s := &c.dns
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ips[id]
}

func detectPublicIP(ctx context.Context, sv store.Server) (string, error) {
	if sv.Kind == store.ServerSSH {
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", sv.Host)
		if err != nil {
			return "", err
		}
		for _, ip := range ips {
			if !ip.IsPrivate() && !ip.IsLoopback() {
				return ip.String(), nil
			}
		}
		return "", errors.New("its SSH host has no public IPv4 address")
	}
	// The manager's own server: ask Cloudflare which address we come from.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://1.1.1.1/cdn-cgi/trace", nil)
	if err != nil {
		return "", err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	sc := bufio.NewScanner(res.Body)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "ip="); ok {
			if ip := net.ParseIP(v); ip != nil && ip.To4() != nil {
				return v, nil
			}
		}
	}
	return "", errors.New("no IPv4 address in 1.1.1.1/cdn-cgi/trace")
}
