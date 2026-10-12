package core

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store/sqlite"
)

func TestDatabaseSize(t *testing.T) {
	cfg := config.Config{DataDir: t.TempDir()}
	c := newTestCore(t, cfg)
	if d := c.databaseSize(); d != nil {
		t.Errorf("no database yet: %+v", d)
	}
	st, err := sqlite.Open(context.Background(), filepath.Join(cfg.DataDir, "kipitiny.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if d := c.databaseSize(); d == nil || d.SizeBytes+d.WALBytes == 0 {
		t.Errorf("size = %+v", d)
	}
}
