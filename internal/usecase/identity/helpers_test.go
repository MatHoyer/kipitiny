package identity

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/sqlite"
)

// newTestIdentity runs the identity use cases on a temp SQLite store,
// without a mailer.
func newTestIdentity(t *testing.T, cfg config.Config) *Identity {
	t.Helper()
	st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New(cfg, st, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}
