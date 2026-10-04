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

	"github.com/MatHoyer/kipitiny/internal/api"
	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/core"
	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/mcp"
	"github.com/MatHoyer/kipitiny/internal/probe"
	"github.com/MatHoyer/kipitiny/internal/secrets"
	"github.com/MatHoyer/kipitiny/internal/store/sqlite"
	"github.com/MatHoyer/kipitiny/web"
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
		case "probe":
			// Healthcheck injected into app containers: keep it dependency-free.
			if len(os.Args) != 3 || probe.Check(os.Args[2]) != nil {
				os.Exit(1)
			}
			os.Exit(0)
		case "self-update":
			// Run by the updater container the manager starts (core.ApplyUpdate).
			run = selfUpdate
		case "secrets-env":
			// Started by a password manager CLI to hand resolved references back
			// (secrets.RefEnv).
			if secrets.PrintEnv(os.Stdout) != nil {
				os.Exit(1)
			}
			os.Exit(0)
		case "deploy":
			// Run from CI against a manager's API (deployCmd).
			run = deployCmd
		case "healthcheck":
			// Docker HEALTHCHECK for the manager image, which has no curl.
			if probe.Check(healthURL(config.Load().Addr)) != nil {
				os.Exit(1)
			}
			os.Exit(0)
		default:
			fmt.Fprintf(os.Stderr, "usage: %s [reset-password <username> | disable-2fa <username> | deploy --service <project/service> [--tag <tag>] | healthcheck]\n", os.Args[0])
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

	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		level = slog.LevelInfo
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	st, err := sqlite.Open(ctx, cfg.DBPath())
	if err != nil {
		return err
	}
	defer st.Close()

	dc, err := docker.New()
	if err != nil {
		return err
	}
	defer dc.Close()

	c := core.New(cfg, st, dc, log)
	if err := c.InitAuth(ctx); err != nil {
		return err
	}
	if err := c.Bootstrap(ctx); err != nil {
		// Keep serving the UI so the problem is visible there.
		log.Warn("docker bootstrap failed", "err", err)
	}
	if err := c.StartScheduler(ctx); err != nil {
		return err
	}
	if err := c.StartReconciler(ctx); err != nil {
		return err
	}
	if err := c.StartUpdateChecker(); err != nil {
		return err
	}
	if err := c.StartDNSSync(); err != nil {
		return err
	}
	if err := c.StartStats(); err != nil {
		return err
	}
	if err := c.StartUptime(); err != nil {
		return err
	}

	mux := http.NewServeMux()
	a := api.New(c, log)
	mux.Handle("/api/", a)
	mux.Handle("/mcp", a.Authenticated(mcp.Handler(c, version)))
	mux.Handle("/", web.Handler())

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.SecureHeaders(mux),
		ReadHeaderTimeout: 10 * time.Second,
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
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), core.DrainTimeout)
	defer cancelDrain()
	if err := c.Shutdown(drainCtx); err != nil {
		log.Warn("background work did not stop in time", "err", err)
	}
	return srvErr
}

// selfUpdate replaces the manager container os.Args[2] with image os.Args[3].
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
	return dc.ReplaceContainer(context.Background(), os.Args[2], os.Args[3], core.StopTimeout, core.UpdateHealthTimeout, logf)
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
