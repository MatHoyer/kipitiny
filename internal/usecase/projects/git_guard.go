package projects

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/go-git/go-git/v5/plumbing/transport/client"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

// checkRepoHost looks at the name when linking; the address a fetch
// connects to can differ (a redirect, a name resolving again), so a
// member's fetches over http(s) also refuse private addresses as they dial.
// shortcut: ssh:// fetches are only checked by name, guard their dialer too if members use them.

type repoGuardKey struct{}

// guardRepo marks ctx for fetches checkRepoHost would refuse at a private
// address.
func guardRepo(ctx context.Context, g domain.ProjectGit) context.Context {
	if a, ok := identity.ActorFrom(ctx); ok && !a.Admin() && g.ProviderID == "" {
		return context.WithValue(ctx, repoGuardKey{}, true)
	}
	return ctx
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

func repoDialControl(ctx context.Context, _, addr string, _ syscall.RawConn) error {
	if on, _ := ctx.Value(repoGuardKey{}).(bool); !on {
		return nil
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return err
	}
	if privateAddr(ip) {
		return fmt.Errorf("%w: %s is a private address; only admins link repositories there", identity.ErrForbidden, ip)
	}
	return nil
}

func init() {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, ControlContext: repoDialControl}).DialContext
	t := githttp.NewClient(&http.Client{Transport: tr})
	client.InstallProtocol("http", t)
	client.InstallProtocol("https", t)
}
