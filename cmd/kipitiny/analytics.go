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

	"github.com/kipit-labs/kipitiny/internal/delivery/collector"
	"github.com/kipit-labs/kipitiny/internal/domain/analytics"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/analyticsdb"
	"github.com/kipit-labs/kipitiny/internal/usecase/ingest"
)

// analyticsHealthcheck is the analytics container's: the image is
// distroless, without curl.
func analyticsHealthcheck() error {
	// Within the container healthcheck's 5s, so a hung server fails it here.
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(analytics.PublicPort)+"/healthz", nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz: %s", res.Status)
	}
	return nil
}

// serveAnalytics runs the server `kipitiny analytics` starts, until
// SIGTERM: public tracker and hits on PublicPort, stats for the manager on
// PrivatePort, events in one SQLite file.
func serveAnalytics() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	dir := env("KIPITINY_ANALYTICS_DATA_DIR", "/data")
	token := os.Getenv("KIPITINY_ANALYTICS_TOKEN")
	if token == "" {
		return errors.New("KIPITINY_ANALYTICS_TOKEN is required")
	}
	days, err := strconv.Atoi(env("KIPITINY_ANALYTICS_RETENTION_DAYS", "365"))
	if err != nil || days < 1 {
		return fmt.Errorf("KIPITINY_ANALYTICS_RETENTION_DAYS: want a number of days")
	}
	st, err := analyticsdb.Open(ctx, filepath.Join(dir, "analytics.db"), time.Now)
	if err != nil {
		return err
	}
	defer st.Close()

	svc := ingest.New(st, time.Now)
	go svc.RunPrune(ctx, time.Duration(days)*24*time.Hour, log)

	servers := []*http.Server{
		{Addr: ":" + strconv.Itoa(analytics.PublicPort), Handler: collector.Public(svc, log), ReadHeaderTimeout: 10 * time.Second},
		{Addr: ":" + strconv.Itoa(analytics.PrivatePort), Handler: collector.Private(svc, token), ReadHeaderTimeout: 10 * time.Second},
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
		err = errors.Join(err, s.Shutdown(shut))
	}
	return err
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
