package core

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/sqlite"
	"github.com/kipit-labs/kipitiny/internal/mail"
	"github.com/kipit-labs/kipitiny/internal/mail/mailtest"
	"github.com/kipit-labs/kipitiny/internal/usecase/data"
	"github.com/kipit-labs/kipitiny/internal/usecase/monitoring"
)

func newTestCore(t *testing.T, cfg config.Config) *Core {
	t.Helper()
	st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New(cfg, st, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

const webhook = "https://discord.com/api/webhooks/1/secret"

// redirect sends every request to srv, whatever its URL.
type redirect struct{ srv *httptest.Server }

func (r redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	u, _ := url.Parse(r.srv.URL)
	req.URL.Scheme, req.URL.Host = u.Scheme, u.Host
	return r.srv.Client().Transport.RoundTrip(req)
}

// localProvider is an email provider sending to a fake server.
func localProvider(name string, srv *mailtest.Server) monitoring.EmailProviderInput {
	return monitoring.EmailProviderInput{Name: name, Config: mail.Config{Host: "127.0.0.1", Port: srv.Port, Security: mail.None, From: "ops@example.com"}}
}

// The database services of the spec tests (the data tests have their own).
func pgService() domain.Service {
	return domain.Service{
		ID: "01DB", ProjectID: "P1", Name: "db", Kind: domain.ServiceKindPostgres,
		Image: data.DefaultPostgresImage, Replicas: 1, MemoryMB: 512, Env: data.NewPostgresEnv(),
	}
}

func redisService() domain.Service {
	return domain.Service{
		ID: "01CACHE", ProjectID: "P1", Name: "cache", Kind: domain.ServiceKindRedis,
		Image: data.DefaultRedisImage, Replicas: 1, MemoryMB: 256, Env: data.NewRedisEnv(),
	}
}

func mysqlService(kind domain.ServiceKind) domain.Service {
	return domain.Service{
		ID: "01SQL", ProjectID: "P1", Name: "sql", Kind: kind,
		Image: data.MySQLImageFor(kind), Replicas: 1, MemoryMB: 512, Env: data.NewMySQLEnv(),
	}
}

func mongoService() domain.Service {
	return domain.Service{
		ID: "01DOCS", ProjectID: "P1", Name: "docs", Kind: domain.ServiceKindMongoDB,
		Image: data.DefaultMongoImage, Replicas: 1, MemoryMB: 1024, Env: data.NewMongoEnv(),
	}
}
