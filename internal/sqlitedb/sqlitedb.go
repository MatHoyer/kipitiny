// Package sqlitedb opens the SQLite files of kipitiny's binaries (the
// manager's state, the analytics server's events) the same way: WAL, one
// connection, goose migrations with a snapshot before each upgrade.
package sqlitedb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

// Open opens (or creates) the database at path and applies the pending
// migrations of fsys, a folder of goose SQL files.
func Open(ctx context.Context, path string, fsys fs.FS) (*sql.DB, error) {
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(ON)" +
		"&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// Single writer: serialize all access through one connection.
	db.SetMaxOpenConns(1)

	if err := migrate(ctx, db, path, fsys); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// keepPreMigrate is how many pre-migration snapshots are kept next to the DB.
const keepPreMigrate = 3

// migrate applies pending migrations, first snapshotting an existing database
// to <path>.pre-migrate-<version> so an upgrade can be undone.
func migrate(ctx context.Context, db *sql.DB, path string, fsys fs.FS) error {
	p, err := goose.NewProvider(goose.DialectSQLite3, db, fsys)
	if err != nil {
		return fmt.Errorf("goose: %w", err)
	}
	v, err := p.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if err := checkNotAhead(path, v, p.ListSources()); err != nil {
		return err
	}
	pending, err := p.HasPending(ctx)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if !pending {
		return nil
	}
	if v > 0 {
		snap := fmt.Sprintf("%s.pre-migrate-%d", path, v)
		_ = os.Remove(snap) // VACUUM INTO refuses to overwrite
		if _, err := db.ExecContext(ctx, "VACUUM INTO ?", snap); err != nil {
			return fmt.Errorf("snapshot before migrating: %w", err)
		}
		prunePreMigrate(path)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// checkNotAhead refuses a database migrated by a newer binary: this one
// would find nothing pending and run on a schema it doesn't know.
func checkNotAhead(path string, v int64, srcs []*goose.Source) error {
	if len(srcs) == 0 {
		return nil
	}
	latest := srcs[len(srcs)-1].Version
	if v <= latest {
		return nil
	}
	msg := fmt.Sprintf("database schema is at %d, this version knows up to %d: run a newer version", v, latest)
	snap := fmt.Sprintf("%s.pre-migrate-%d", path, latest)
	if _, err := os.Stat(snap); err == nil {
		msg += ", or restore " + snap
	}
	return errors.New(msg)
}

// prunePreMigrate keeps the newest keepPreMigrate snapshots.
func prunePreMigrate(path string) {
	snaps, _ := filepath.Glob(path + ".pre-migrate-*")
	version := func(p string) int {
		n, _ := strconv.Atoi(p[strings.LastIndex(p, "-")+1:])
		return n
	}
	slices.SortFunc(snaps, func(a, b string) int { return version(b) - version(a) })
	for _, old := range snaps[min(len(snaps), keepPreMigrate):] {
		_ = os.Remove(old)
	}
}
