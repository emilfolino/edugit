package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations reads migrations named NNNN_description.sql in version order.
func loadMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, "migrations")
	if err != nil {
		return nil, err
	}
	var ms []migration
	for _, e := range entries {
		name := e.Name()
		num, _, ok := strings.Cut(strings.TrimSuffix(name, ".sql"), "_")
		if !ok || !strings.HasSuffix(name, ".sql") {
			return nil, fmt.Errorf("bad migration file name %q", name)
		}
		v, err := strconv.Atoi(num)
		if err != nil {
			return nil, fmt.Errorf("bad migration version in %q: %w", name, err)
		}
		b, err := fs.ReadFile(fsys, "migrations/"+name)
		if err != nil {
			return nil, err
		}
		ms = append(ms, migration{version: v, name: name, sql: string(b)})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })
	for i := 1; i < len(ms); i++ {
		if ms[i].version == ms[i-1].version {
			return nil, fmt.Errorf("duplicate migration version %d", ms[i].version)
		}
	}
	return ms, nil
}

// migrate applies pending migrations, each in its own transaction. Migrations
// are forward-only.
func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT NOT NULL,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
	)`); err != nil {
		return err
	}
	ms, err := loadMigrations(migrationFS)
	if err != nil {
		return err
	}
	for _, m := range ms {
		var one int
		err := s.db.QueryRowContext(ctx, "SELECT 1 FROM schema_migrations WHERE version = ?", m.version).Scan(&one)
		if err == nil {
			continue
		}
		if err != sql.ErrNoRows {
			return err
		}
		if err := s.apply(ctx, m); err != nil {
			return fmt.Errorf("apply %s: %w", m.name, err)
		}
	}
	return nil
}

func (s *Store) apply(ctx context.Context, m migration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version, name) VALUES (?, ?)", m.version, m.name); err != nil {
		return err
	}
	return tx.Commit()
}
