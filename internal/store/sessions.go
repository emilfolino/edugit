package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// timeLayout matches the strftime format used by column defaults, so stored
// timestamps sort and compare as strings.
const timeLayout = "2006-01-02T15:04:05.000Z"

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// CreateSession stores a session for userID identified by the hash of its
// secret, valid until expires.
func (s *Store) CreateSession(ctx context.Context, userID int64, secretHash string, now, expires time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (user_id, secret_hash, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		userID, secretHash, formatTime(now), formatTime(expires))
	return err
}

// SessionUser returns the user owning the unexpired session with the given
// secret hash, or ErrNotFound.
func (s *Store) SessionUser(ctx context.Context, secretHash string, now time.Time) (User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx, `
		SELECT u.id, u.saml_subject, u.username, u.email, u.display_name, u.is_admin
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.secret_hash = ? AND s.expires_at > ?`, secretHash, formatTime(now)))
}

// DeleteSession ends the session with the given secret hash. Deleting a
// missing session is not an error.
func (s *Store) DeleteSession(ctx context.Context, secretHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE secret_hash = ?`, secretHash)
	return err
}

// PurgeSessions deletes sessions that expired before now.
func (s *Store) PurgeSessions(ctx context.Context, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, formatTime(now))
	return err
}

func (s *Store) scanUser(row *sql.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Subject, &u.Username, &u.Email, &u.DisplayName, &u.IsAdmin)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}
