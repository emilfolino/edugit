package store

import (
	"context"
	"database/sql"
	"errors"
)

// Site is a course's static-site configuration: the teacher repo, branch and
// subdirectory it is published from.
type Site struct {
	CourseID int64
	RepoID   int64
	Repo     string
	Branch   string
	Dir      string
}

// SetSite creates or replaces the course's site configuration.
func (s *Store) SetSite(ctx context.Context, courseID, repoID int64, branch, dir string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO course_sites (course_id, repo_id, branch, dir) VALUES (?, ?, ?, ?)
		ON CONFLICT (course_id) DO UPDATE SET repo_id = excluded.repo_id, branch = excluded.branch, dir = excluded.dir`,
		courseID, repoID, branch, dir)
	return err
}

// ClearSite removes the course's site configuration.
func (s *Store) ClearSite(ctx context.Context, courseID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM course_sites WHERE course_id = ?`, courseID)
	return err
}

// SiteByCourse returns the site configuration, or ErrNotFound.
func (s *Store) SiteByCourse(ctx context.Context, slug string) (Site, error) {
	var st Site
	err := s.db.QueryRowContext(ctx, `
		SELECT c.id, r.id, r.name, s.branch, s.dir
		FROM course_sites s JOIN courses c ON c.id = s.course_id JOIN repos r ON r.id = s.repo_id
		WHERE c.slug = ?`, slug).Scan(&st.CourseID, &st.RepoID, &st.Repo, &st.Branch, &st.Dir)
	if errors.Is(err, sql.ErrNoRows) {
		return Site{}, ErrNotFound
	}
	return st, err
}
