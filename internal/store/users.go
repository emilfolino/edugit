package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// User is an authenticated person known to edugit.
type User struct {
	ID          int64
	Subject     string
	Username    string
	Email       string
	DisplayName string
	IsAdmin     bool
}

// LoginUser provisions or updates the user with the given IdP subject and
// records the login. Email and display name follow the IdP. When
// grantAdmin is true the user becomes a global admin; admin is never revoked
// here, so config bootstrap cannot silently demote anyone.
func (s *Store) LoginUser(ctx context.Context, subject, email, displayName string, grantAdmin bool) (User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit

	admin := 0
	if grantAdmin {
		admin = 1
	}
	var u User
	err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE saml_subject = ?`, subject).Scan(&u.ID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		base := usernameFrom(email, subject)
		for i := 0; ; i++ {
			name := base
			if i > 0 {
				name = fmt.Sprintf("%s-%d", base, i+1)
			}
			err = tx.QueryRowContext(ctx, `
				INSERT INTO users (saml_subject, username, email, display_name, is_admin, last_login_at)
				VALUES (?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))
				ON CONFLICT (username) DO NOTHING
				RETURNING id`, subject, name, email, displayName, admin).Scan(&u.ID)
			if errors.Is(err, sql.ErrNoRows) {
				continue // username taken
			}
			break
		}
	case err == nil:
		_, err = tx.ExecContext(ctx, `
			UPDATE users SET email = ?, display_name = ?, is_admin = MAX(is_admin, ?),
			    last_login_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
			WHERE id = ?`, email, displayName, admin, u.ID)
	}
	if err != nil {
		return User{}, fmt.Errorf("login user: %w", err)
	}
	err = tx.QueryRowContext(ctx, `
		SELECT id, saml_subject, username, email, display_name, is_admin FROM users WHERE id = ?`, u.ID).
		Scan(&u.ID, &u.Subject, &u.Username, &u.Email, &u.DisplayName, &u.IsAdmin)
	if err != nil {
		return User{}, fmt.Errorf("login user: %w", err)
	}
	if err := bindEnrollments(ctx, tx, u.ID, email); err != nil {
		return User{}, fmt.Errorf("login user: bind enrolments: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("login user: %w", err)
	}
	return u, nil
}

// usernameFrom derives a URL-safe username from the email local part.
func usernameFrom(email, subject string) string {
	local, _, _ := strings.Cut(email, "@")
	var b strings.Builder
	for _, r := range strings.ToLower(local) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == '.':
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "user-" + subject[:min(8, len(subject))]
	}
	return b.String()
}
