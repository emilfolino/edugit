package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestOpenAppliesMigrations(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "edugit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var n int
	if err := s.DB().QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Errorf("migrations applied: got %d, want >= 1", n)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "edugit.db")
	for i := 0; i < 2; i++ {
		s, err := Open(ctx, path)
		if err != nil {
			t.Fatalf("open #%d: %v", i, err)
		}
		s.Close()
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	_, err = s.DB().ExecContext(ctx, "INSERT INTO memberships (course_id, user_id, role) VALUES (999, 999, 'student')")
	if err == nil {
		t.Fatal("expected foreign key violation")
	}
}

func TestRoleConstraint(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	db := s.DB()
	mustExec(t, db, "INSERT INTO users (saml_subject, username) VALUES ('s', 'u')")
	mustExec(t, db, "INSERT INTO courses (slug, title) VALUES ('c', 'C')")
	if _, err := db.ExecContext(ctx, "INSERT INTO memberships (course_id, user_id, role) VALUES (1, 1, 'admin')"); err == nil {
		t.Error("global 'admin' must not be a valid course role")
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO memberships (course_id, user_id, role) VALUES (1, 1, 'teacher')"); err != nil {
		t.Errorf("teacher role rejected: %v", err)
	}
}

func TestBackup(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, filepath.Join(dir, "edugit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mustExec(t, s.DB(), "INSERT INTO courses (slug, title) VALUES ('c', 'C')")

	dst := filepath.Join(dir, "backup.db")
	if err := s.Backup(ctx, dst); err != nil {
		t.Fatal(err)
	}
	b, err := Open(ctx, dst)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var n int
	if err := b.DB().QueryRowContext(ctx, "SELECT count(*) FROM courses").Scan(&n); err != nil || n != 1 {
		t.Errorf("backup courses: got %d, err %v, want 1", n, err)
	}
}

func mustExec(t *testing.T, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, query string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), query); err != nil {
		t.Fatal(err)
	}
}
