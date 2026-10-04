package core

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/MatHoyer/kipitiny/internal/config"
)

func TestTrustedProxy(t *testing.T) {
	traefik := netip.MustParseAddr("172.18.0.2")
	c := &Core{cfg: config.Config{
		Traefik:        config.Traefik{Enabled: true},
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.9.0.0/24")},
	}}
	// A fresh cache: lookups of unknown peers don't reach Docker.
	c.proxy.addrs, c.proxy.refreshed = []netip.Addr{traefik}, time.Now()

	tests := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"::ffff:172.18.0.2", true},
		{"172.18.0.2", true},
		{"172.18.0.5", false}, // another container on kipitiny-proxy
		{"10.9.0.7", true},
		{"10.9.1.7", false},
		{"203.0.113.7", false},
	}
	for _, tt := range tests {
		if got := c.TrustedProxy(context.Background(), netip.MustParseAddr(tt.addr)); got != tt.want {
			t.Errorf("TrustedProxy(%s) = %v, want %v", tt.addr, got, tt.want)
		}
	}

	c.cfg.Traefik.Enabled = false
	if c.TrustedProxy(context.Background(), traefik) {
		t.Error("Traefik's address trusted while the manager doesn't run Traefik")
	}
}
