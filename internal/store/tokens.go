package store

import (
	"context"
	"time"
)

// Token is the stored metadata of a personal access token. The secret itself
// is never stored.
type Token struct {
	ID         int64
	Name       string
	CreatedAt  time.Time
	ExpiresAt  time.Time // zero when the token does not expire
	LastUsedAt time.Time // zero when never used
}

// CreateToken stores a token for userID identified by the hash of its secret.
// A zero expires means no expiry.
func (s *Store) CreateToken(ctx context.Context, userID int64, name, secretHash string, now, expires time.Time) error {
	var exp any
	if !expires.IsZero() {
		exp = formatTime(expires)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO tokens (user_id, name, token_hash, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		userID, name, secretHash, formatTime(now), exp)
	return err
}

// TokenUser returns the user owning the unexpired token with the given secret
// hash and records its use, or returns ErrNotFound.
func (s *Store) TokenUser(ctx context.Context, secretHash string, now time.Time) (User, error) {
	u, err := s.scanUser(s.db.QueryRowContext(ctx, `
		SELECT u.id, u.saml_subject, u.username, u.email, u.display_name, u.is_admin
		FROM tokens t JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = ? AND (t.expires_at IS NULL OR t.expires_at > ?)`, secretHash, formatTime(now)))
	if err != nil {
		return User{}, err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE tokens SET last_used_at = ? WHERE token_hash = ?`, formatTime(now), secretHash)
	return u, err
}

// ListTokens returns the user's tokens, newest first.
func (s *Store) ListTokens(ctx context.Context, userID int64) ([]Token, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, created_at, expires_at, last_used_at FROM tokens
		WHERE user_id = ? ORDER BY created_at DESC, id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Token
	for rows.Next() {
		var (
			t             Token
			created       string
			expires, used *string
		)
		if err := rows.Scan(&t.ID, &t.Name, &created, &expires, &used); err != nil {
			return nil, err
		}
		t.CreatedAt, _ = time.Parse(timeLayout, created)
		if expires != nil {
			t.ExpiresAt, _ = time.Parse(timeLayout, *expires)
		}
		if used != nil {
			t.LastUsedAt, _ = time.Parse(timeLayout, *used)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeToken deletes the token id if it belongs to userID, or returns
// ErrNotFound.
func (s *Store) RevokeToken(ctx context.Context, userID, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM tokens WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
