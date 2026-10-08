// Package store is the SQLite-backed metadata store: users, courses,
// memberships, repositories, pull requests and so on. Git objects live on
// disk, not here.
//
// The SQL is kept driver-agnostic (database/sql, plain SQLite dialect) so the
// driver can be swapped without touching callers.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no cgo); see TODO #2.
)

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("not found")

// Store wraps the database handle.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at path and applies all
// pending migrations. Use ":memory:" for an isolated in-memory database in
// tests.
func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if path == ":memory:" {
		// Each connection to :memory: is a separate database.
		db.SetMaxOpenConns(1)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// dsn builds a modernc DSN. Pragmas are set per connection.
func dsn(path string) string {
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	if path == ":memory:" {
		return "file::memory:?" + q.Encode()
	}
	return "file:" + filepath.ToSlash(path) + "?" + q.Encode()
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// DB returns the underlying handle for packages that run their own queries.
func (s *Store) DB() *sql.DB { return s.db }

// Backup writes a consistent snapshot of the database to dst using
// VACUUM INTO. dst must not already exist. It is safe to run while the server
// is serving requests.
func (s *Store) Backup(ctx context.Context, dst string) error {
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", dst); err != nil {
		return fmt.Errorf("backup to %q: %w", dst, err)
	}
	return nil
}
