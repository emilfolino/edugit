package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Course is a course row.
type Course struct {
	ID       int64
	Slug     string
	Title    string
	Term     string
	Archived bool
}

// CreateCourse inserts a course.
func (s *Store) CreateCourse(ctx context.Context, slug, title, term string) (Course, error) {
	c := Course{Slug: slug, Title: title, Term: term}
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO courses (slug, title, term) VALUES (?, ?, ?) RETURNING id`, slug, title, term).Scan(&c.ID)
	if err != nil {
		return Course{}, fmt.Errorf("create course %q: %w", slug, err)
	}
	return c, nil
}

// CourseBySlug returns the course or ErrNotFound.
func (s *Store) CourseBySlug(ctx context.Context, slug string) (Course, error) {
	var c Course
	err := s.db.QueryRowContext(ctx,
		`SELECT id, slug, title, term, archived FROM courses WHERE slug = ?`, slug).
		Scan(&c.ID, &c.Slug, &c.Title, &c.Term, &c.Archived)
	if errors.Is(err, sql.ErrNoRows) {
		return Course{}, ErrNotFound
	}
	return c, err
}

// SetArchived archives or restores a course.
func (s *Store) SetArchived(ctx context.Context, courseID int64, archived bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE courses SET archived = ? WHERE id = ?`, archived, courseID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CourseRole is a course together with the viewer's role in it; Role is
// empty when the viewer is not enrolled (global admin listing).
type CourseRole struct {
	Course
	Role string
}

// UserCourses lists the courses the user is enrolled in, newest first.
func (s *Store) UserCourses(ctx context.Context, userID int64) ([]CourseRole, error) {
	return s.courseRoles(ctx, `
		SELECT c.id, c.slug, c.title, c.term, c.archived, m.role
		FROM memberships m JOIN courses c ON c.id = m.course_id
		WHERE m.user_id = ? ORDER BY c.archived, c.created_at DESC, c.id DESC`, userID)
}

// AllCourses lists every course, for global admins.
func (s *Store) AllCourses(ctx context.Context) ([]CourseRole, error) {
	return s.courseRoles(ctx, `
		SELECT id, slug, title, term, archived, '' FROM courses
		ORDER BY archived, created_at DESC, id DESC`)
}

func (s *Store) courseRoles(ctx context.Context, q string, args ...any) ([]CourseRole, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CourseRole
	for rows.Next() {
		var c CourseRole
		if err := rows.Scan(&c.ID, &c.Slug, &c.Title, &c.Term, &c.Archived, &c.Role); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Member is a roster entry. UserID is zero for a pending enrolment, which
// has only an email.
type Member struct {
	UserID      int64
	Username    string
	DisplayName string
	Email       string
	Role        string
	Source      string
}

// Pending reports whether the person has not signed in yet.
func (m Member) Pending() bool { return m.UserID == 0 }

// Roster lists enrolled members and pending enrolments, staff first.
func (s *Store) Roster(ctx context.Context, courseID int64) ([]Member, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.username, u.display_name, u.email, m.role, m.source
		FROM memberships m JOIN users u ON u.id = m.user_id WHERE m.course_id = ?1
		UNION ALL
		SELECT 0, '', '', e.email, e.role, e.source FROM enrollments e WHERE e.course_id = ?1
		ORDER BY 5 DESC, 4`, courseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.Username, &m.DisplayName, &m.Email, &m.Role, &m.Source); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// EnrollEmail enrolls the person with that email: directly if they have
// already signed in, otherwise as a pending enrolment bound at first login.
// An existing role is replaced. The caller checks domain eligibility.
func (s *Store) EnrollEmail(ctx context.Context, courseID int64, email, role, source string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit

	var uid int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE lower(email) = ?`, email).Scan(&uid)
	switch {
	case err == nil:
		_, err = tx.ExecContext(ctx, `
			INSERT INTO memberships (course_id, user_id, role, source) VALUES (?, ?, ?, ?)
			ON CONFLICT (course_id, user_id) DO UPDATE SET role = excluded.role, source = excluded.source`,
			courseID, uid, role, source)
	case errors.Is(err, sql.ErrNoRows):
		_, err = tx.ExecContext(ctx, `
			INSERT INTO enrollments (course_id, email, role, source) VALUES (?, ?, ?, ?)
			ON CONFLICT (course_id, email) DO UPDATE SET role = excluded.role, source = excluded.source`,
			courseID, email, role, source)
	}
	if err != nil {
		return fmt.Errorf("enroll %q: %w", email, err)
	}
	return tx.Commit()
}

// RemoveEnrollment deletes a pending enrolment. It returns ErrNotFound if
// there is none.
func (s *Store) RemoveEnrollment(ctx context.Context, courseID int64, email string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM enrollments WHERE course_id = ? AND email = ?`, courseID, strings.ToLower(email))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UserByID returns the user or ErrNotFound.
func (s *Store) UserByID(ctx context.Context, id int64) (User, error) {
	var u User
	err := s.db.QueryRowContext(ctx, `
		SELECT id, saml_subject, username, email, display_name, is_admin FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Subject, &u.Username, &u.Email, &u.DisplayName, &u.IsAdmin)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// bindEnrollments turns pending enrolments for email into memberships.
// Existing memberships win, so a login never changes a role.
func bindEnrollments(ctx context.Context, tx *sql.Tx, userID int64, email string) error {
	email = strings.ToLower(email)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO memberships (course_id, user_id, role, source)
		SELECT course_id, ?, role, source FROM enrollments WHERE email = ?
		ON CONFLICT (course_id, user_id) DO NOTHING`, userID, email); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM enrollments WHERE email = ?`, email)
	return err
}

// SetInvite makes tokenHash the course's only invite link, replacing any
// earlier one.
func (s *Store) SetInvite(ctx context.Context, courseID int64, tokenHash string, expires time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO invites (course_id, token_hash, expires_at) VALUES (?, ?, ?)
		ON CONFLICT (course_id) DO UPDATE SET token_hash = excluded.token_hash,
		    expires_at = excluded.expires_at,
		    created_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
		courseID, tokenHash, expires.UTC().Format(timeLayout))
	return err
}

// InviteExpiry returns when the course's invite link expires, or ErrNotFound
// if it has none.
func (s *Store) InviteExpiry(ctx context.Context, courseID int64) (time.Time, error) {
	var at string
	err := s.db.QueryRowContext(ctx, `SELECT expires_at FROM invites WHERE course_id = ?`, courseID).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, ErrNotFound
	}
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(timeLayout, at)
}

// RevokeInvite deletes the course's invite link.
func (s *Store) RevokeInvite(ctx context.Context, courseID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM invites WHERE course_id = ?`, courseID)
	return err
}

// InviteCourse returns the course a live invite link belongs to, or
// ErrNotFound if the link is unknown or expired.
func (s *Store) InviteCourse(ctx context.Context, tokenHash string, now time.Time) (Course, error) {
	var c Course
	err := s.db.QueryRowContext(ctx, `
		SELECT c.id, c.slug, c.title, c.term, c.archived
		FROM invites i JOIN courses c ON c.id = i.course_id
		WHERE i.token_hash = ? AND i.expires_at > ?`, tokenHash, now.UTC().Format(timeLayout)).
		Scan(&c.ID, &c.Slug, &c.Title, &c.Term, &c.Archived)
	if errors.Is(err, sql.ErrNoRows) {
		return Course{}, ErrNotFound
	}
	return c, err
}

// JoinByInvite enrolls the user as a student through a live invite link. An
// existing membership, whatever its role, is left unchanged. Archived
// courses refuse new students.
func (s *Store) JoinByInvite(ctx context.Context, tokenHash string, userID int64, now time.Time) (Course, error) {
	c, err := s.InviteCourse(ctx, tokenHash, now)
	if err != nil {
		return Course{}, err
	}
	if c.Archived {
		return Course{}, ErrNotFound
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO memberships (course_id, user_id, role, source) VALUES (?, ?, 'student', 'manual')
		ON CONFLICT (course_id, user_id) DO NOTHING`, c.ID, userID)
	if err != nil {
		return Course{}, fmt.Errorf("join by invite: %w", err)
	}
	return c, nil
}
