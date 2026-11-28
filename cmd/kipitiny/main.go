package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kipit-labs/kipitiny/internal/analytics"
	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/core"
	httpdelivery "github.com/kipit-labs/kipitiny/internal/delivery/http"
	"github.com/kipit-labs/kipitiny/internal/delivery/mcp"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/docker"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/selfcontainer"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/sqlite"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/sqlitehelper"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
	"github.com/kipit-labs/kipitiny/internal/usecase/servers"
	"github.com/kipit-labs/kipitiny/web"
)

// version is set at build time (-ldflags "-X main.version=...").
var version = "dev"

func main() {
	run := serve
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "reset-password":
			run = resetPassword
		case "disable-2fa":
			run = disable2FA
		case "make-admin":
			run = makeAdmin
		case "allow-password-login":
			run = allowPasswordLogin
		case "probe":
			// Healthcheck injected into app containers: keep it dependency-free.
			if len(os.Args) != 3 || probeCheck(os.Args[2]) != nil {
				os.Exit(1)
			}
			os.Exit(0)
		case "self-update":
			// Run by the updater container the manager starts (core.ApplyUpdate).
			run = selfUpdate
		case "deploy":
			// Run from CI against a manager's API (deployCmd).
			run = deployCmd
		case "preview":
			// Run from CI against a manager's API (previewCmd).
			run = previewCmd
		case "analytics":
			// The analytics server's container, started by the manager from
			// its own image (core.ensureAnalytics).
			if len(os.Args) == 3 && os.Args[2] == "healthcheck" {
				if analytics.Healthcheck() != nil {
					os.Exit(1)
				}
				os.Exit(0)
			}
			run = serveAnalytics
		case "sqlite":
			// The data browser's SQLite client, run in a helper container of
			// this image that mounts an app's volumes (core.sqliteRun).
			os.Exit(sqlitehelper.Main(os.Args[2:], os.Stdin, os.Stdout))
		case "healthcheck":
			// Docker HEALTHCHECK for the manager image, which has no curl.
			if probeCheck(healthURL(config.Load().Addr)) != nil {
				os.Exit(1)
			}
			os.Exit(0)
		default:
			fmt.Fprintf(os.Stderr, "usage: %s [reset-password <username> | disable-2fa <username> | make-admin <username> | allow-password-login | analytics [healthcheck] | deploy --service <project/service> [--tag <tag>] | preview --project <project> --name <name> [--tag <service>=<tag>] [--close] | healthcheck]\n", os.Args[0])
			os.Exit(2)
		}
	}
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func serve() error {
	cfg := config.Load()
	cfg.Version = version
	cfg.SelfContainerID = selfcontainer.ID()

	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		level = slog.LevelInfo
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	handler, shutdown, err := wire(ctx, cfg, log)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		// Closes keep-alive connections nobody uses. No read or write
		// timeout: uploads, downloads and log streams can take long.
		IdleTimeout: 2 * time.Minute,
		// Request contexts end on SIGTERM so long-lived SSE log streams don't
		// hold up Shutdown (which only waits, it never cancels).
		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Addr, "data", cfg.DataDir)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Info("shutting down")
	}

	httpCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	srvErr := srv.Shutdown(httpCtx)
	// Running deploys and backups get to finish, within Docker's stop timeout
	// (stop_grace_period in the compose file; the updater uses the same).
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), servers.DrainTimeout)
	defer cancelDrain()
	if err := shutdown(drainCtx); err != nil {
		log.Warn("background work did not stop in time", "err", err)
	}
	return srvErr
}

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

	c := core.New(cfg, st, dc, log)
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

// selfUpdate replaces the manager container os.Args[2] with image os.Args[3].
func serveAnalytics() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return analytics.Serve(ctx, slog.New(slog.NewTextHandler(os.Stderr, nil)))
}

func selfUpdate() error {
	if len(os.Args) != 4 {
		return errors.New("usage: self-update <container> <image>")
	}
	dc, err := docker.New()
	if err != nil {
		return err
	}
	defer dc.Close()
	logf := func(format string, args ...any) { slog.Info(fmt.Sprintf(format, args...)) }
	return dc.ReplaceContainer(context.Background(), os.Args[2], os.Args[3], servers.StopTimeout, servers.UpdateHealthTimeout, logf)
}

// healthURL is the manager's health endpoint as seen from inside its container.
func healthURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://127.0.0.1:3000/api/health"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/api/health"
}

// resetPassword reads a new password from stdin. Run it where the manager's
// data dir is, e.g. `docker compose exec -it manager /kipitiny reset-password admin`.
func resetPassword() error {
	if len(os.Args) != 3 {
		return errors.New("usage: reset-password <username>")
	}
	cfg := config.Load()
	ctx := context.Background()
	st, err := sqlite.Open(ctx, cfg.DBPath())
	if err != nil {
		return err
	}
	defer st.Close()

	fmt.Fprint(os.Stderr, "New password: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return err
	}
	password := strings.TrimRight(line, "\r\n")

	c := core.New(cfg, st, nil, slog.Default())
	if err := c.ResetPassword(ctx, os.Args[2], password); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Password updated; existing sessions were signed out.")
	return nil
}

// makeAdmin makes a user an admin, e.g. when the only admin left can't
// sign in.
func makeAdmin() error {
	if len(os.Args) != 3 {
		return errors.New("usage: make-admin <username>")
	}
	cfg := config.Load()
	ctx := context.Background()
	st, err := sqlite.Open(ctx, cfg.DBPath())
	if err != nil {
		return err
	}
	defer st.Close()

	if err := core.New(cfg, st, nil, slog.Default()).MakeAdmin(ctx, os.Args[2]); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, os.Args[2]+" is an admin; their sessions were signed out.")
	return nil
}

// allowPasswordLogin stops requiring single sign-on, e.g. when the provider
// is down and nobody can sign in.
func allowPasswordLogin() error {
	if len(os.Args) != 2 {
		return errors.New("usage: allow-password-login")
	}
	cfg := config.Load()
	ctx := context.Background()
	st, err := sqlite.Open(ctx, cfg.DBPath())
	if err != nil {
		return err
	}
	defer st.Close()

	if err := core.New(cfg, st, nil, slog.Default()).AllowPasswordLogin(ctx); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Single sign-on is no longer required: passwords and passkeys sign in again.")
	return nil
}

// disable2FA turns two-factor authentication off for a user who lost both
// the authenticator and the recovery codes. Passkeys are kept.
func disable2FA() error {
	if len(os.Args) != 3 {
		return errors.New("usage: disable-2fa <username>")
	}
	cfg := config.Load()
	ctx := context.Background()
	st, err := sqlite.Open(ctx, cfg.DBPath())
	if err != nil {
		return err
	}
	defer st.Close()

	if err := core.New(cfg, st, nil, slog.Default()).ResetTOTP(ctx, os.Args[2]); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Two-factor authentication is off; the password alone signs in again.")
	return nil
}
