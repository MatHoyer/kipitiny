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
	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/cron"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/sqlite"
	"github.com/kipit-labs/kipitiny/internal/mail"
	"github.com/kipit-labs/kipitiny/internal/mail/mailtest"
	"github.com/kipit-labs/kipitiny/internal/usecase/monitoring"
	"github.com/kipit-labs/kipitiny/internal/usecase/servers"
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

// useDocker has c's servers reach dk for the local server.
func (c *Core) useDocker(dk *docker.Client) {
	c.Servers = servers.New(c.bg, &c.cfg, c.store, dk, cron.Cron{}, &c.steps, &c.probes, serversPorts{c}, c.log)
}
