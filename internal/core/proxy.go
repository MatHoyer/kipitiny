package core

import (
	"context"
	"net/netip"
	"slices"
	"sync"
	"time"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// proxyRefreshEvery bounds how often a request from an unknown private peer
// re-reads Traefik's addresses (Traefik may have been recreated).
const proxyRefreshEvery = 10 * time.Second

// proxyState caches the addresses of the manager server's Traefik.
type proxyState struct {
	mu        sync.Mutex
	addrs     []netip.Addr
	refreshed time.Time
}

// TrustedProxy reports whether a request from addr may set the client
// address in forwarding headers: loopback, KIPITINY_TRUSTED_PROXIES, or the
// Traefik container the manager runs. Other containers on kipitiny-proxy can
// reach the manager directly, so being on a private network isn't enough.
func (c *Core) TrustedProxy(ctx context.Context, addr netip.Addr) bool {
	addr = addr.Unmap()
	if addr.IsLoopback() {
		return true
	}
	for _, p := range c.cfg.TrustedProxies {
		if p.Contains(addr) {
			return true
		}
	}
	if !c.cfg.Traefik.Enabled || !addr.IsPrivate() {
		return false
	}
	c.proxy.mu.Lock()
	defer c.proxy.mu.Unlock()
	if slices.Contains(c.proxy.addrs, addr) {
		return true
	}
	if time.Since(c.proxy.refreshed) < proxyRefreshEvery {
		return false
	}
	c.refreshProxyLocked(ctx)
	return slices.Contains(c.proxy.addrs, addr)
}

// refreshProxy re-reads Traefik's addresses after the reconciler touched it.
func (c *Core) refreshProxy(ctx context.Context) {
	c.proxy.mu.Lock()
	defer c.proxy.mu.Unlock()
	c.refreshProxyLocked(ctx)
}

func (c *Core) refreshProxyLocked(ctx context.Context) {
	c.proxy.refreshed = time.Now()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cts, err := c.dockerFor(store.LocalServerID).ListContainers(ctx, map[string]string{docker.LabelComponent: traefikComponent})
	if err != nil {
		c.log.Warn("read traefik addresses", "err", err)
		return // keep the last known ones
	}
	var addrs []netip.Addr
	for _, ct := range cts {
		if ct.NetworkSettings == nil {
			continue
		}
		for _, ep := range ct.NetworkSettings.Networks {
			if ep != nil && ep.IPAddress.IsValid() {
				addrs = append(addrs, ep.IPAddress.Unmap())
			}
		}
	}
	c.proxy.addrs = addrs
}
