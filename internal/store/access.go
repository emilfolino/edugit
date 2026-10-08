package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// SetMembership enrolls the user in the course with role, replacing any
// existing role. role must be course_admin, teacher or student; source must
// be manual, saml or import.
func (s *Store) SetMembership(ctx context.Context, courseID, userID int64, role, source string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO memberships (course_id, user_id, role, source) VALUES (?, ?, ?, ?)
		ON CONFLICT (course_id, user_id) DO UPDATE SET role = excluded.role, source = excluded.source`,
		courseID, userID, role, source)
	if err != nil {
		return fmt.Errorf("set membership: %w", err)
	}
	return nil
}

// RemoveMembership unenrolls the user. It returns ErrNotFound if they were
// not enrolled.
func (s *Store) RemoveMembership(ctx context.Context, courseID, userID int64) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM memberships WHERE course_id = ? AND user_id = ?`, courseID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Roles returns the user's role name for every course they are enrolled in,
// keyed by course ID.
func (s *Store) Roles(ctx context.Context, userID int64) (map[int64]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT course_id, role FROM memberships WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	roles := map[int64]string{}
	for rows.Next() {
		var id int64
		var role string
		if err := rows.Scan(&id, &role); err != nil {
			return nil, err
		}
		roles[id] = role
	}
	return roles, rows.Err()
}

// Repo is a repository row.
type Repo struct {
	ID         int64
	CourseID   int64
	Name       string
	Kind       string
	IsTemplate bool
	Archived   bool
}

// CreateRepo inserts repository metadata. It does not touch the disk.
func (s *Store) CreateRepo(ctx context.Context, courseID int64, name, kind string, isTemplate bool) (Repo, error) {
	r := Repo{CourseID: courseID, Name: name, Kind: kind, IsTemplate: isTemplate}
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO repos (course_id, name, kind, is_template) VALUES (?, ?, ?, ?) RETURNING id`,
		courseID, name, kind, isTemplate).Scan(&r.ID)
	if err != nil {
		return Repo{}, fmt.Errorf("create repo %q: %w", name, err)
	}
	return r, nil
}

// AddRepoMember grants the user explicit access to a student or team repo.
func (s *Store) AddRepoMember(ctx context.Context, repoID, userID int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO repo_members (repo_id, user_id) VALUES (?, ?)`, repoID, userID)
	return err
}

// RepoByName looks up a repository by course slug and name, and reports
// whether userID is an explicit member. It returns ErrNotFound if the repo
// does not exist.
func (s *Store) RepoByName(ctx context.Context, courseSlug, name string, userID int64) (r Repo, member bool, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT r.id, r.course_id, r.name, r.kind, r.is_template, r.archived,
		       EXISTS (SELECT 1 FROM repo_members m WHERE m.repo_id = r.id AND m.user_id = ?)
		FROM repos r JOIN courses c ON c.id = r.course_id
		WHERE c.slug = ? AND r.name = ?`, userID, courseSlug, name).
		Scan(&r.ID, &r.CourseID, &r.Name, &r.Kind, &r.IsTemplate, &r.Archived, &member)
	if errors.Is(err, sql.ErrNoRows) {
		return Repo{}, false, ErrNotFound
	}
	return r, member, err
}
