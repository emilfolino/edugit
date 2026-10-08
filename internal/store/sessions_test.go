package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSessionsAndTokens(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	u, err := s.LoginUser(ctx, "oid-1", "a@bth.se", "A", false)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.LoginUser(ctx, "oid-2", "b@bth.se", "B", false)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	t.Run("session", func(t *testing.T) {
		if err := s.CreateSession(ctx, u.ID, "h1", now, now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		got, err := s.SessionUser(ctx, "h1", now.Add(time.Minute))
		if err != nil || got.ID != u.ID {
			t.Fatalf("got %+v, %v, want user %d", got, err, u.ID)
		}
		if _, err := s.SessionUser(ctx, "h1", now.Add(2*time.Hour)); !errors.Is(err, ErrNotFound) {
			t.Errorf("expired: got %v, want ErrNotFound", err)
		}
		if _, err := s.SessionUser(ctx, "nope", now); !errors.Is(err, ErrNotFound) {
			t.Errorf("unknown: got %v, want ErrNotFound", err)
		}
		if err := s.DeleteSession(ctx, "h1"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SessionUser(ctx, "h1", now); !errors.Is(err, ErrNotFound) {
			t.Errorf("deleted: got %v, want ErrNotFound", err)
		}
	})

	t.Run("purge", func(t *testing.T) {
		if err := s.CreateSession(ctx, u.ID, "old", now, now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if err := s.PurgeSessions(ctx, now.Add(2*time.Hour)); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&n); err != nil || n != 0 {
			t.Errorf("got %d sessions, %v, want 0", n, err)
		}
	})

	t.Run("token", func(t *testing.T) {
		if err := s.CreateToken(ctx, u.ID, "laptop", "t1", now, time.Time{}); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateToken(ctx, u.ID, "short", "t2", now, now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		later := now.Add(2 * time.Hour)
		if got, err := s.TokenUser(ctx, "t1", later); err != nil || got.ID != u.ID {
			t.Fatalf("no expiry: got %+v, %v", got, err)
		}
		if _, err := s.TokenUser(ctx, "t2", later); !errors.Is(err, ErrNotFound) {
			t.Errorf("expired: got %v, want ErrNotFound", err)
		}
		list, err := s.ListTokens(ctx, u.ID)
		if err != nil || len(list) != 2 {
			t.Fatalf("got %d tokens, %v, want 2", len(list), err)
		}
		var laptop Token
		for _, tk := range list {
			if tk.Name == "laptop" {
				laptop = tk
			}
		}
		if !laptop.ExpiresAt.IsZero() || !laptop.LastUsedAt.Equal(later) {
			t.Errorf("laptop: got %+v, want no expiry and last used %v", laptop, later)
		}
		if err := s.RevokeToken(ctx, other.ID, laptop.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("revoke by other user: got %v, want ErrNotFound", err)
		}
		if err := s.RevokeToken(ctx, u.ID, laptop.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.TokenUser(ctx, "t1", later); !errors.Is(err, ErrNotFound) {
			t.Errorf("revoked: got %v, want ErrNotFound", err)
		}
	})
}
