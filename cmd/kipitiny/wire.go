package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/kipit-labs/kipitiny/internal/config"
	httpdelivery "github.com/kipit-labs/kipitiny/internal/delivery/http"
	"github.com/kipit-labs/kipitiny/internal/delivery/mcp"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/domain/secrets"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/age"
	analyticsclient "github.com/kipit-labs/kipitiny/internal/infrastructure/analytics"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/cloudflare"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/cron"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/docker"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/git"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/gitprovider"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/notify"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/protonpass"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/registry"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/smtp"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/sqlite"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/storage"
	"github.com/kipit-labs/kipitiny/internal/usecase/app"
	"github.com/kipit-labs/kipitiny/internal/usecase/data"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"
	"github.com/kipit-labs/kipitiny/internal/usecase/monitoring"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
	"github.com/kipit-labs/kipitiny/internal/usecase/servers"
	"github.com/kipit-labs/kipitiny/web"
)

// wire opens the store and the Docker daemon, starts the manager's
// background work (it stops with ctx) and returns the HTTP handler.
// shutdown waits for running operations until its ctx, then closes both.
func wire(ctx context.Context, cfg config.Config, log *slog.Logger) (handler http.Handler, shutdown func(context.Context) error, err error) {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, nil, err
	}
	st, err := sqlite.Open(ctx, cfg.DBPath())
	if err != nil {
		return nil, nil, err
	}
	dc, err := docker.New()
	if err != nil {
		st.Close()
		return nil, nil, err
	}
	closeAll := func() {
		dc.Close()
		st.Close()
	}

	c := newApp(cfg, st, dc, log)
	if err := c.InitAuth(ctx); err != nil {
		closeAll()
		return nil, nil, err
	}
	if err := c.Bootstrap(ctx); err != nil {
		// Keep serving the UI so the problem is visible there.
		log.Warn("docker bootstrap failed", "err", err)
	}
	for _, start := range []func() error{
		func() error { return c.StartScheduler(ctx) },
		func() error { return c.StartReconciler(ctx) },
		c.StartUpdateChecker,
		c.StartDNSSync,
		c.StartStats,
		c.StartUptime,
		c.StartErrorsWatch,
		c.StartSecurityChecks,
		c.StartSleep,
		c.StartPreviewExpiry,
		c.StartGitSync,
	} {
		if err := start(); err != nil {
			closeAll()
			return nil, nil, err
		}
	}

	mux := http.NewServeMux()
	a := httpdelivery.New(c, log)
	mux.Handle("/api/", a)
	mux.Handle(network.PagesPath, httpdelivery.Pages(c, log))
	mux.Handle("/mcp", a.Authenticated(mcp.Handler(c, version)))
	mux.Handle("/", web.Handler())
	shutdown = func(ctx context.Context) error {
		defer closeAll()
		return c.Shutdown(ctx)
	}
	return httpdelivery.SecureHeaders(mux, c.TrustedProxy), shutdown, nil
}

// newApp builds the use cases on their adapters, around the local Docker
// client (nil for the commands that don't reach Docker); remote servers are
// connected on demand over SSH.
func newApp(cfg config.Config, st domain.Store, local *docker.Client, log *slog.Logger) *app.App {
	return app.New(cfg, st, app.Adapters{
		Pool: func(remote func(id string) (servers.RemoteDocker, error)) servers.Pool {
			return docker.NewPool(local, func(id string) (*docker.Client, error) {
				r, err := remote(id)
				if err != nil {
					return nil, err
				}
				return docker.NewSSH(docker.SSHConfig(r))
			})
		},
		SecretKinds:  func(local domain.ContainerRuntime) []identity.SecretKind { return secretKinds(cfg, local) },
		Crypt:        age.Encryption{},
		Cron:         cron.Cron{},
		Git:          git.Git{},
		GitProviders: gitprovider.Client{},
		Registry:     registry.Client{HTTP: http.DefaultClient},
		NewDNS:       func(token string) network.DNS { return cloudflare.New(token) },
		// Local backup targets are under the data directory.
		OpenStorage: func(t domain.BackupTarget) (data.Storage, error) {
			return storage.Open(t, storage.Env{DataDir: cfg.DataDir})
		},
		SMTP:         smtp.Client{},
		Channels:     notify.Channels{},
		NewAnalytics: func(baseURL, token string) monitoring.Analytics { return analyticsclient.New(baseURL, token) },
	}, log)
}

// secretKinds are the password managers this build offers. Their CLIs run in
// helper containers on the manager's own Docker (none without one).
func secretKinds(cfg config.Config, local domain.ContainerRuntime) []identity.SecretKind {
	if local == nil {
		return nil
	}
	return []identity.SecretKind{
		identity.NewSecretKind(func(conn string) secrets.Provider {
			return protonpass.New(protonpass.NewContainerRunner(local, cfg.ProtonPass.Image, conn, identity.SecretHelperIdle))
		}),
	}
}
