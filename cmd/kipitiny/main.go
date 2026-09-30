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
	"github.com/MatHoyer/kipitiny/internal/store/sqlite"
	"github.com/MatHoyer/kipitiny/web"
)

func main() {
	run := serve
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "reset-password":
			run = resetPassword
		default:
			fmt.Fprintf(os.Stderr, "usage: %s [reset-password <username>]\n", os.Args[0])
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

	mux := http.NewServeMux()
	mux.Handle("/api/", api.New(c, log))
	mux.Handle("/", web.Handler())

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
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

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	srvErr := srv.Shutdown(shutdownCtx)
	if err := c.Shutdown(shutdownCtx); err != nil {
		log.Warn("background work did not stop in time", "err", err)
	}
	return srvErr
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
