package servers

import (
	"context"
	"errors"
	"fmt"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/progress"
)

// Bootstrap prepares host-level resources the manager relies on.
func (c *Servers) Bootstrap(ctx context.Context) error {
	if n, err := c.store.FailRunningDeployments(ctx, "interrupted by manager restart"); err != nil {
		return err
	} else if n > 0 {
		c.log.Warn("marked interrupted deployments as failed", "count", n)
	}
	if err := c.store.FailRunningOperations(ctx, "interrupted by manager restart"); err != nil {
		return err
	}
	if err := c.mgr.MigrateSMTPSetting(ctx); err != nil {
		return err
	}
	if c.cfg.Domain != "" && !c.cfg.Traefik.Enabled {
		c.log.Warn("KIPITINY_DOMAIN is ignored without the managed Traefik; route it in your own proxy", "domain", c.cfg.Domain)
	}
	if c.cfg.Tunnel.Token != "" && !c.cfg.Traefik.Enabled {
		c.log.Warn("KIPITINY_CLOUDFLARE_TUNNEL_TOKEN is ignored without the managed Traefik")
	}
	servers, err := c.store.ListServers(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, sv := range servers {
		// Nothing runs yet: what restore tests and volume helpers left
		// behind is from a crash. (Later bootstraps must not touch them.)
		if err := c.mgr.CleanupRestoreTests(ctx, c.DockerFor(sv.ID)); err != nil {
			errs = append(errs, fmt.Errorf("server %s: %w", sv.Name, err))
			continue
		}
		if err := c.bootstrapServer(ctx, sv); err != nil {
			errs = append(errs, fmt.Errorf("server %s: %w", sv.Name, err))
		}
	}
	return errors.Join(errs...)
}

// bootstrapServer prepares a Docker host: proxy network, Traefik, and
// cleanup of terminals left by a crash.
func (c *Servers) bootstrapServer(ctx context.Context, sv domain.Server) error {
	dk := c.DockerFor(sv.ID)
	progress.Report(ctx, ServerStepNetwork)
	if err := dk.EnsureNetwork(ctx, domain.ProxyNetwork); err != nil {
		return err
	}
	if err := c.cleanupTerminals(ctx, dk); err != nil {
		return err
	}
	if sv.Kind == domain.ServerLocal {
		c.cleanupUpdater(ctx, dk)
		if err := c.mgr.CleanupSecretHelpers(ctx, dk); err != nil {
			c.log.Warn("removing password manager helpers failed", "err", err)
		}
	}
	if c.cfg.Traefik.Enabled {
		progress.Report(ctx, ServerStepTraefik)
		if err := c.mgr.EnsureTraefik(ctx, sv); err != nil {
			return fmt.Errorf("traefik: %w", err)
		}
		progress.Report(ctx, ServerStepTunnel)
		if err := c.mgr.EnsureTunnel(ctx, sv); err != nil {
			return fmt.Errorf("cloudflared: %w", err)
		}
	}
	return nil
}
