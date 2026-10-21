package analytics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Healthcheck is the container's: the image is distroless, without curl.
func Healthcheck() error {
	res, err := http.Get("http://127.0.0.1:" + strconv.Itoa(PublicPort) + "/healthz")
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz: %s", res.Status)
	}
	return nil
}

// Serve runs the server `kipitiny analytics` starts, until ctx is done:
// public tracker and hits on PublicPort, stats for the manager on
// PrivatePort, events in one SQLite file.
func Serve(ctx context.Context, log *slog.Logger) error {
	dir := env("KIPITINY_ANALYTICS_DATA_DIR", "/data")
	token := os.Getenv("KIPITINY_ANALYTICS_TOKEN")
	if token == "" {
		return errors.New("KIPITINY_ANALYTICS_TOKEN is required")
	}
	days, err := strconv.Atoi(env("KIPITINY_ANALYTICS_RETENTION_DAYS", "365"))
	if err != nil || days < 1 {
		return fmt.Errorf("KIPITINY_ANALYTICS_RETENTION_DAYS: want a number of days")
	}
	st, err := Open(ctx, filepath.Join(dir, "analytics.db"))
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
		{Addr: ":" + strconv.Itoa(PublicPort), Handler: Public(st, log), ReadHeaderTimeout: 10 * time.Second},
		{Addr: ":" + strconv.Itoa(PrivatePort), Handler: Private(st, token), ReadHeaderTimeout: 10 * time.Second},
	}
	errs := make(chan error, len(servers))
	for _, s := range servers {
		go func() { errs <- s.ListenAndServe() }()
	}
	log.Info("analytics listening", "public", PublicPort, "private", PrivatePort)
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
