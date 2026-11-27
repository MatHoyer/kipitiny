package projects

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

// checkRepoHost looks at the name when linking; the address a fetch
// connects to can differ (a redirect, a name resolving again), so a
// member's fetches over http(s) also refuse private addresses as they dial.
// shortcut: ssh:// fetches are only checked by name, guard their dialer too if members use them.

// repoAddrCheck refuses the private addresses of a member's fetches; nil
// for those checkRepoHost doesn't check.
func repoAddrCheck(ctx context.Context, g domain.ProjectGit) func(netip.Addr) error {
	if a, ok := identity.ActorFrom(ctx); !ok || a.Admin() || g.ProviderID != "" {
		return nil
	}
	return func(ip netip.Addr) error {
		if privateAddr(ip) {
			return fmt.Errorf("%w: %s is a private address; only admins link repositories there", identity.ErrForbidden, ip)
		}
		return nil
	}
}

var (
	// cgnat is where Tailscale and carrier-grade NAT addresses live.
	cgnat   = netip.MustParsePrefix("100.64.0.0/10")
	thisNet = netip.MustParsePrefix("0.0.0.0/8")
	nat64   = netip.MustParsePrefix("64:ff9b::/96")
)

// privateAddr is an address a member's repository may not be at: the
// manager would reach inside the network (other containers, the cloud
// metadata endpoint).
func privateAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() ||
		cgnat.Contains(ip) || thisNet.Contains(ip) || nat64.Contains(ip)
}
