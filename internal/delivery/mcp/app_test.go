package mcp

import (
	"log/slog"
	"net/http"

	"github.com/kipit-labs/kipitiny/internal/config"
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
	"github.com/kipit-labs/kipitiny/internal/infrastructure/storage"
	"github.com/kipit-labs/kipitiny/internal/usecase/app"
	"github.com/kipit-labs/kipitiny/internal/usecase/data"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"
	"github.com/kipit-labs/kipitiny/internal/usecase/monitoring"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
	"github.com/kipit-labs/kipitiny/internal/usecase/servers"
)

// newTestApp builds the use cases on the adapters cmd/kipitiny/wire.go
// gives them.
func newTestApp(cfg config.Config, st domain.Store, local *docker.Client, log *slog.Logger) *app.App {
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
