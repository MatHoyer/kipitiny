package network

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/kipit-labs/kipitiny/internal/cloudflare"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

// StartDNSSync keeps Cloudflare DNS records and tunnel routes in line with
// service domains: now, after changes, every ten minutes, and on demand
// (SyncDNS).
func (c *Network) StartDNSSync() error {
	return c.mgr.GoLoop(func() {
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

// SyncDNS syncs DNS records and tunnel routes now instead of waiting for the
// next round, detecting servers' public IPs again.
func (c *Network) SyncDNS(ctx context.Context) (CloudflareView, error) {
	if c.cfToken(ctx) == "" {
		return CloudflareView{}, fmt.Errorf("%w: Cloudflare isn't connected", domain.ErrInvalid)
	}
	s := c.cf(ctx)
	s.mu.Lock()
	s.ips = nil
	s.mu.Unlock()
	// Finish even if the caller goes away: a half-applied sync is redone
	// only at the next round.
	if err := c.syncDNS(context.WithoutCancel(ctx)); err != nil {
		return CloudflareView{}, fmt.Errorf("%w: %s", domain.ErrInvalid, err.Error())
	}
	return c.CloudflareStatus(ctx), nil
}

// DNSRecords is the state of every hostname whose record the manager keeps,
// as of the last sync.
func (c *Network) DNSRecords(ctx context.Context) map[string]DNSStatus {
	s := c.cf(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]DNSStatus, len(s.status))
	maps.Copy(out, s.status)
	return out
}

// Want is a DNS record the manager should hold.
type Want struct {
	Zone    cloudflare.Zone
	Record  cloudflare.Record
	Tunnel  string // ID of the tunnel serving it, if any
	Problem string // why it can't be created
	Warning string // why it may not work once created
}

func (c *Network) syncDNS(ctx context.Context) error {
	token := c.cfToken(ctx)
	if token == "" {
		return nil
	}
	s := &c.dns
	s.syncing.Lock()
	defer s.syncing.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cf := cloudflare.New(token)

	zones, err := cf.Zones(ctx)
	if err != nil {
		s.mu.Lock()
		s.zonesErr = err.Error()
		s.mu.Unlock()
		return err
	}
	c.saveZoneNames(ctx, zones)

	wants, err := c.WantedRecords(ctx, zones)
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

// WantedRecords computes the record every service domain (and the manager's
// own) in a Cloudflare zone should have.
func (c *Network) WantedRecords(ctx context.Context, zones []cloudflare.Zone) (map[string]Want, error) {
	svcs, err := c.store.ListAllServices(ctx)
	if err != nil {
		return nil, err
	}
	servers, err := c.store.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[string]domain.Server{}
	for _, sv := range servers {
		byID[sv.ID] = sv
	}
	domains, err := c.store.ListDomains(ctx)
	if err != nil {
		return nil, err
	}

	wants := map[string]Want{}
	// direct: the record points at the server, never proxied, for traffic
	// Cloudflare doesn't carry (published ports).
	add := func(host, serverID string, direct bool) {
		z, ok := cloudflare.ZoneFor(zones, host)
		if !ok || IsLocalDomain(host) {
			return
		}
		w := Want{Zone: z, Record: cloudflare.Record{Name: host, TTL: 1}}
		sv := byID[serverID]
		if token := c.TunnelToken(sv); token != "" && !direct {
			t, err := cloudflare.ParseTunnelToken(token)
			if err != nil {
				w.Problem = fmt.Sprintf("%s's tunnel token: %v", sv.Name, err)
			}
			// Tunnel hostnames must be proxied.
			w.Record.Type, w.Record.Content, w.Record.Proxied, w.Tunnel = "CNAME", t.Target(), true, t.ID
		} else {
			ip, err := c.publicIP(ctx, sv)
			if err != nil {
				w.Problem = err.Error()
			}
			w.Record.Type, w.Record.Content, w.Record.Proxied = "A", ip, proxiedFor(domains, host) && !direct
		}
		if w.Record.Proxied && !edgeCertCovers(z.Name, host) {
			w.Warning = fmt.Sprintf("%s is more than one level under %s: Cloudflare's free certificate only covers *.%s, "+
				"so visitors get a TLS error through the proxy. Use a name like %s, turn off Proxied for its domain, "+
				"or order an advanced certificate in Cloudflare", host, z.Name, z.Name, flatName(z.Name, host))
		}
		wants[host] = w
	}
	// Services of a project may share a domain: its one record is direct
	// when any of them publishes ports.
	direct, server := map[string]bool{}, map[string]string{}
	for _, svc := range svcs {
		if svc.Domain != "" {
			direct[svc.Domain] = direct[svc.Domain] || len(svc.PublishedPorts) > 0
			server[svc.Domain] = svc.ServerID
		}
	}
	for host, d := range direct {
		add(host, server[host], d)
	}
	if c.cfg.Domain != "" && c.cfg.Traefik.Enabled {
		add(c.cfg.Domain, domain.LocalServerID, false)
	}
	if d := c.mgr.AnalyticsDomain(ctx); d != "" && c.cfg.Traefik.Enabled {
		add(d, domain.LocalServerID, false)
	}
	return wants, nil
}

// edgeCertCovers reports whether Cloudflare's Universal SSL certificate for
// zone (the apex and *.zone) covers host.
func edgeCertCovers(zone, host string) bool {
	sub := strings.TrimSuffix(strings.TrimSuffix(host, zone), ".")
	return !strings.Contains(sub, ".")
}

// flatName suggests a one-level name for host: a.b.example.com → a-b.example.com.
func flatName(zone, host string) string {
	sub := strings.TrimSuffix(strings.TrimSuffix(host, zone), ".")
	return strings.ReplaceAll(sub, ".", "-") + "." + zone
}

// proxiedFor is the proxy setting of the listed domain covering host.
func proxiedFor(domains []domain.Domain, host string) bool {
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
func (c *Network) applyRecord(ctx context.Context, cf *cloudflare.Client, w Want) DNSStatus {
	if w.Problem != "" {
		return DNSStatus{State: "error", Message: w.Problem}
	}
	host := w.Record.Name
	recs, err := cf.Records(ctx, w.Zone.ID, host)
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
		c.log.Info("dns: creating record", "name", host, "type", w.Record.Type, "content", w.Record.Content)
		err = cf.CreateRecord(ctx, w.Zone.ID, w.Record)
	case mine[0].Type == w.Record.Type:
		r := mine[0]
		if r.Content != w.Record.Content || r.Proxied != w.Record.Proxied {
			c.log.Info("dns: updating record", "name", host, "content", w.Record.Content, "proxied", w.Record.Proxied)
			err = cf.UpdateRecord(ctx, w.Zone.ID, r.ID, w.Record)
		}
		mine = mine[1:]
	default: // A <-> CNAME: a name can't hold both
		for _, r := range mine {
			if err = cf.DeleteRecord(ctx, w.Zone.ID, r.ID); err != nil {
				break
			}
		}
		mine = nil
		if err == nil {
			err = cf.CreateRecord(ctx, w.Zone.ID, w.Record)
		}
	}
	for _, r := range mine { // duplicates
		_ = cf.DeleteRecord(ctx, w.Zone.ID, r.ID)
	}
	if err != nil {
		if cloudflare.Forbidden(err) {
			return DNSStatus{State: "error", Message: "the Cloudflare token can't edit DNS here (it needs Zone › DNS › Edit)"}
		}
		return DNSStatus{State: "error", Message: err.Error()}
	}
	if w.Warning != "" {
		return DNSStatus{State: "warning", Message: w.Warning}
	}
	return DNSStatus{State: "synced"}
}

// publicIP is where records point for apps on sv: the configured address,
// else detected (and remembered for the process's lifetime).
func (c *Network) publicIP(ctx context.Context, sv domain.Server) (string, error) {
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
		return "", fmt.Errorf("can't tell %s's public IP (%w): set it in Infrastructure › Servers", sv.Name, err)
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
func (c *Network) DetectedPublicIP(id string) string {
	s := &c.dns
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ips[id]
}

func detectPublicIP(ctx context.Context, sv domain.Server) (string, error) {
	if sv.Kind == domain.ServerSSH {
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
