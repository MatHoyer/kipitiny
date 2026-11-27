// Package sqlite implements domain.Store on SQLite (modernc.org/sqlite, no CGO).
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/sqlitedb"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct {
	db *bun.DB
}

var _ domain.Store = (*Store)(nil)

// Open opens (or creates) the database at path and applies pending migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, err
	}
	sqldb, err := sqlitedb.Open(ctx, path, sub)
	if err != nil {
		return nil, err
	}
	return &Store{db: bun.NewDB(sqldb, sqlitedialect.New())}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Snapshot uses VACUUM INTO: a consistent, compacted copy taken without
// blocking writers for long.
func (s *Store) Snapshot(ctx context.Context, path string) error {
	_, err := s.db.ExecContext(ctx, "VACUUM INTO ?", path)
	return err
}

func (s *Store) FailRunningOperations(ctx context.Context, errMsg string) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for _, model := range []any{(*domain.Backup)(nil), (*domain.Restore)(nil), (*domain.JobRun)(nil)} {
			_, err := tx.NewUpdate().Model(model).
				Set("status = ?", domain.OpFailed).Set("error = ?", errMsg).Set("finished_at = ?", now()).
				Where("status = ?", domain.OpRunning).Exec(ctx)
			if err != nil {
				return mapErr(err)
			}
		}
		_, err := tx.NewUpdate().Model((*domain.Backup)(nil)).
			Set("verify_status = ?", domain.OpFailed).Set("verify_error = ?", errMsg).Set("verified_at = ?", now()).
			Where("verify_status = ?", domain.OpRunning).Exec(ctx)
		return mapErr(err)
	})
}

func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&v)
	return v, mapErr(err)
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value",
		key, value)
	return mapErr(err)
}

// now matches the microsecond precision bun persists, so returned structs
// equal what a later read yields.
func now() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}

func deleteByID(ctx context.Context, db *bun.DB, table, id string) error {
	res, err := db.NewDelete().TableExpr(table).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	}
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() {
		case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
			return fmt.Errorf("%w: %s", domain.ErrConflict, err.Error())
		}
	}
	return err
}
