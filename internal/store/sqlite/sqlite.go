// Package sqlite implements store.Store on SQLite (modernc.org/sqlite, no CGO).
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/MatHoyer/kipitiny/internal/ids"
	"github.com/MatHoyer/kipitiny/internal/store"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct {
	db *bun.DB
}

var _ store.Store = (*Store)(nil)

// Open opens (or creates) the database at path and applies pending migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(ON)" +
		"&_pragma=synchronous(NORMAL)"
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// Single writer: serialize all access through one connection.
	sqldb.SetMaxOpenConns(1)

	if err := migrate(ctx, sqldb); err != nil {
		sqldb.Close()
		return nil, err
	}
	return &Store{db: bun.NewDB(sqldb, sqlitedialect.New())}, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, db, sub)
	if err != nil {
		return fmt.Errorf("goose: %w", err)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) CreateProject(ctx context.Context, p store.Project) (store.Project, error) {
	now := now()
	p.ID, p.CreatedAt, p.UpdatedAt = ids.New(), now, now
	if _, err := s.db.NewInsert().Model(&p).Exec(ctx); err != nil {
		return store.Project{}, mapErr(err)
	}
	return p, nil
}

func (s *Store) GetProject(ctx context.Context, id string) (store.Project, error) {
	var p store.Project
	err := s.db.NewSelect().Model(&p).Where("id = ?", id).Scan(ctx)
	return p, mapErr(err)
}

func (s *Store) ListProjects(ctx context.Context) ([]store.Project, error) {
	ps := []store.Project{}
	err := s.db.NewSelect().Model(&ps).Order("name").Scan(ctx)
	return ps, mapErr(err)
}

func (s *Store) DeleteProject(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "projects", id)
}

func (s *Store) CreateService(ctx context.Context, svc store.Service) (store.Service, error) {
	now := now()
	svc.ID, svc.CreatedAt, svc.UpdatedAt = ids.New(), now, now
	if _, err := s.db.NewInsert().Model(&svc).Exec(ctx); err != nil {
		return store.Service{}, mapErr(err)
	}
	return svc, nil
}

func (s *Store) GetService(ctx context.Context, id string) (store.Service, error) {
	var svc store.Service
	err := s.db.NewSelect().Model(&svc).Where("id = ?", id).Scan(ctx)
	return svc, mapErr(err)
}

func (s *Store) ListServices(ctx context.Context, projectID string) ([]store.Service, error) {
	svcs := []store.Service{}
	err := s.db.NewSelect().Model(&svcs).
		Where("project_id = ?", projectID).Order("name").Scan(ctx)
	return svcs, mapErr(err)
}

func (s *Store) DeleteService(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "services", id)
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
		return store.ErrNotFound
	}
	return nil
}

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() {
		case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
			return fmt.Errorf("%w: %v", store.ErrConflict, err)
		}
	}
	return err
}
