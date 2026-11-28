// Package monitoring is what tells people how their apps do: notifications
// (channels, email providers, browser push), uptime checks, the analytics
// server with its visits and error tracking.
package monitoring

import (
	"context"
	"log/slog"
	"net/http"
	"sync"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/domain/mail"
	"github.com/kipit-labs/kipitiny/internal/domain/notify"
)

// Manager is what monitoring uses from the rest of the manager.
type Manager interface {
	// DockerFor is the Docker client of a server; RegistryAuth the
	// credential to pull image with, "" for none.
	DockerFor(serverID string) domain.ContainerRuntime
	RegistryAuth(ctx context.Context, image string) string
	// OwnImageRef is the manager's own image, which runs the analytics
	// server.
	OwnImageRef() string
	// GoLoop runs f in the background until the manager stops;
	// GoBackground runs an operation the manager waits for on shutdown.
	GoLoop(f func()) error
	GoBackground(f func()) error
	// BrandName, BrandLogo and BrandAccent are the branding emails and push
	// notifications carry.
	BrandName(ctx context.Context) string
	BrandLogo(ctx context.Context) string
	BrandAccent(ctx context.Context) string
	// CurrentUser is the signed-in user of ctx.
	CurrentUser(ctx context.Context) (domain.User, error)
	// CheckGitOwned refuses changes to a project its git repository manages.
	CheckGitOwned(ctx context.Context, projectID string) error
	// ResourceAlerts are the thresholds shown with the notification settings.
	ResourceAlerts(ctx context.Context) domain.ResourceAlerts
	// CertResolver picks how a router gets its certificate; KickDNS syncs
	// DNS records soon (the analytics domain).
	CertResolver(ctx context.Context, serverID, domain string) string
	KickDNS()
}

// SMTP is the admin's mail server (infrastructure/smtp).
type SMTP interface {
	// Check connects and signs in without sending anything.
	Check(ctx context.Context, cfg mail.Config) error
	// Send delivers m; ctx bounds the whole exchange.
	Send(ctx context.Context, cfg mail.Config, m mail.Message) error
}

// Channels are the notification delivery methods and web push
// (infrastructure/notify).
type Channels interface {
	Kinds() []notify.Kind
	Lookup(name string) (notify.Kind, bool)
	// New returns the sender of a channel, checking its configuration.
	New(kind string, cfg map[string]string, d notify.Deps) (notify.Sender, error)
	// WebPush delivers m to one browser.
	WebPush(ctx context.Context, hc *http.Client, v notify.VAPID, subject string, s notify.Subscription, m notify.PushMessage) error
}

type Monitoring struct {
	// cfg is the manager's configuration, shared with core.
	cfg   *config.Config
	store domain.Store
	// smtp sends email, channels the notifications.
	smtp     SMTP
	channels Channels
	mgr      Manager
	log      *slog.Logger
	// bg ends when the manager stops: the uptime and error loops run under it.
	bg context.Context

	notified sync.Map // notification key -> time.Time last sent
	// NotifyHTTP and UptimeHTTP send notifications and run uptime checks;
	// nil: their package's default. Tests redirect them.
	NotifyHTTP *http.Client
	UptimeHTTP *http.Client
	pushMu     sync.Mutex // generating the VAPID keys
	uptime     uptimeState

	analyticsMu    sync.Mutex
	analyticsSites string     // container ID and hostnames last sent to the analytics server
	errorsMu       sync.Mutex // giving apps their DSN
	// analyticsErrors is the container ID and DSN keys last sent to the
	// analytics server.
	analyticsErrors string
	// errorsSeq is the last error change alerted about, -1 before the
	// first look; spiked the deployments alerted about.
	errorsSeq int64
	spiked    map[string]bool
}

// New builds the monitoring use cases; bg is the manager's background context.
func New(bg context.Context, cfg *config.Config, store domain.Store, smtp SMTP, channels Channels, mgr Manager, log *slog.Logger) *Monitoring {
	return &Monitoring{cfg: cfg, store: store, smtp: smtp, channels: channels, mgr: mgr, log: log, bg: bg, errorsSeq: -1, spiked: map[string]bool{}}
}
