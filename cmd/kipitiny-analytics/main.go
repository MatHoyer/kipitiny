// Command kipitiny-analytics is the analytics server the manager runs as a
// container when analytics are on: public tracker and hits on PublicPort,
// stats for the manager on PrivatePort, events in one SQLite file.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/MatHoyer/kipitiny/internal/analytics"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		// distroless has no curl.
		res, err := http.Get("http://127.0.0.1:" + strconv.Itoa(analytics.PublicPort) + "/healthz")
		if err != nil || res.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		return
	}
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dir := env("KIPITINY_ANALYTICS_DATA_DIR", "/data")
	token := os.Getenv("KIPITINY_ANALYTICS_TOKEN")
	if token == "" {
		return errors.New("KIPITINY_ANALYTICS_TOKEN is required")
	}
	days, err := strconv.Atoi(env("KIPITINY_ANALYTICS_RETENTION_DAYS", "365"))
	if err != nil || days < 1 {
		return fmt.Errorf("KIPITINY_ANALYTICS_RETENTION_DAYS: want a number of days")
	}
	st, err := analytics.Open(ctx, filepath.Join(dir, "analytics.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	go func() {
		keep := time.Duration(days) * 24 * time.Hour
		for t := time.NewTicker(time.Hour); ; {
			if err := st.Prune(ctx, keep); err != nil && ctx.Err() == nil {
				log.Error("prune", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()

	servers := []*http.Server{
		{Addr: ":" + strconv.Itoa(analytics.PublicPort), Handler: analytics.Public(st, log), ReadHeaderTimeout: 10 * time.Second},
		{Addr: ":" + strconv.Itoa(analytics.PrivatePort), Handler: analytics.Private(st, token), ReadHeaderTimeout: 10 * time.Second},
	}
	errs := make(chan error, len(servers))
	for _, s := range servers {
		go func() { errs <- s.ListenAndServe() }()
	}
	log.Info("analytics listening", "public", analytics.PublicPort, "private", analytics.PrivatePort)
	select {
	case err = <-errs:
	case <-ctx.Done():
	}
	shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, s := range servers {
		s.Shutdown(shut)
	}
	return err
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
