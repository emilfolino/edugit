package store

import (
	"context"
	"fmt"
)

// RepoEntry is a repository together with whether the asking user is an
// explicit member of it.
type RepoEntry struct {
	Repo
	Member bool
}

// CourseRepos lists the course's repositories by name, with userID's
// membership of each. Callers filter by permission; this does not.
func (s *Store) CourseRepos(ctx context.Context, courseID, userID int64) ([]RepoEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.id, r.course_id, r.name, r.kind, r.is_template, r.archived,
		       EXISTS (SELECT 1 FROM repo_members m WHERE m.repo_id = r.id AND m.user_id = ?)
		FROM repos r WHERE r.course_id = ? ORDER BY r.name`, userID, courseID)
	if err != nil {
		return nil, fmt.Errorf("list repos: %w", err)
	}
	defer rows.Close()
	var out []RepoEntry
	for rows.Next() {
		var e RepoEntry
		if err := rows.Scan(&e.ID, &e.CourseID, &e.Name, &e.Kind, &e.IsTemplate, &e.Archived, &e.Member); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SetTemplate marks or unmarks the repository as a template. It returns
// ErrNotFound if the repository does not exist.
func (s *Store) SetTemplate(ctx context.Context, repoID int64, isTemplate bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE repos SET is_template = ? WHERE id = ?`, isTemplate, repoID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteRepo removes the repository's metadata. It does not touch the disk.
func (s *Store) DeleteRepo(ctx context.Context, repoID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM repos WHERE id = ?`, repoID)
	return err
}

// BranchRule is a branch protection rule as stored.
type BranchRule struct {
	Pattern           string
	RequirePR         bool
	AllowForce        bool
	RequiredApprovals int
	RequireChecks     bool
}

// BranchRules returns the protection rules of the named repository, ordered
// by pattern. An unknown repository has no rules.
func (s *Store) BranchRules(ctx context.Context, courseSlug, name string) ([]BranchRule, error) {
	return s.rules(ctx, `
		SELECT b.pattern, b.require_pr, b.allow_force_push, b.required_approvals, b.require_checks
		FROM branch_protections b
		JOIN repos r ON r.id = b.repo_id JOIN courses c ON c.id = r.course_id
		WHERE c.slug = ? AND r.name = ? ORDER BY b.pattern`, courseSlug, name)
}

func (s *Store) repoRules(ctx context.Context, repoID int64) ([]BranchRule, error) {
	return s.rules(ctx, `
		SELECT pattern, require_pr, allow_force_push, required_approvals, require_checks
		FROM branch_protections WHERE repo_id = ? ORDER BY pattern`, repoID)
}

func (s *Store) rules(ctx context.Context, q string, args ...any) ([]BranchRule, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("load branch rules: %w", err)
	}
	defer rows.Close()
	var out []BranchRule
	for rows.Next() {
		var b BranchRule
		if err := rows.Scan(&b.Pattern, &b.RequirePR, &b.AllowForce, &b.RequiredApprovals, &b.RequireChecks); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
