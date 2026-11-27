package core

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/sqlite"
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
