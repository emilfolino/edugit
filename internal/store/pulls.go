package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrPullExists means an open pull request already covers the branch pair.
var ErrPullExists = errors.New("an open pull request already exists for these branches")

// ErrPullClosed means the pull request is no longer open.
var ErrPullClosed = errors.New("pull request is not open")

// Pull request states.
const (
	PullOpen   = "open"
	PullClosed = "closed"
	PullMerged = "merged"
)

// PullRequest is one pull request of a repository.
type PullRequest struct {
	ID          int64
	RepoID      int64
	Number      int
	AuthorID    int64
	Author      string
	Title       string
	Body        string
	Head        string
	Base        string
	State       string
	MergeCommit string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const pullColumns = `p.id, p.repo_id, p.number, p.author_id, u.username, p.title, p.body,
	p.head_branch, p.base_branch, p.state, COALESCE(p.merge_commit, ''), p.created_at, p.updated_at`

func scanPull(row rowScanner) (PullRequest, error) {
	var p PullRequest
	var created, updated string
	err := row.Scan(&p.ID, &p.RepoID, &p.Number, &p.AuthorID, &p.Author, &p.Title, &p.Body,
		&p.Head, &p.Base, &p.State, &p.MergeCommit, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return PullRequest{}, ErrNotFound
	}
	if err != nil {
		return PullRequest{}, err
	}
	p.CreatedAt, _ = time.Parse(timeLayout, created)
	p.UpdatedAt, _ = time.Parse(timeLayout, updated)
	return p, nil
}

// CreatePull opens a pull request from head into base, numbering it within
// the repository. It returns ErrPullExists if one is already open for the
// same branch pair.
func (s *Store) CreatePull(ctx context.Context, repoID, authorID int64, title, body, head, base string) (PullRequest, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PullRequest{}, err
	}
	defer tx.Rollback()
	var n int
	err = tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM pull_requests
		WHERE repo_id = ? AND head_branch = ? AND base_branch = ? AND state = 'open'`,
		repoID, head, base).Scan(&n)
	if err != nil {
		return PullRequest{}, err
	}
	if n > 0 {
		return PullRequest{}, ErrPullExists
	}
	var id int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO pull_requests (repo_id, number, author_id, title, body, head_branch, base_branch)
		VALUES (?, (SELECT COALESCE(MAX(number), 0) + 1 FROM pull_requests WHERE repo_id = ?), ?, ?, ?, ?, ?)
		RETURNING id`, repoID, repoID, authorID, title, body, head, base).Scan(&id)
	if err != nil {
		return PullRequest{}, fmt.Errorf("create pull request: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return PullRequest{}, err
	}
	return s.pullByID(ctx, id)
}

func (s *Store) pullByID(ctx context.Context, id int64) (PullRequest, error) {
	return scanPull(s.db.QueryRowContext(ctx, `
		SELECT `+pullColumns+` FROM pull_requests p JOIN users u ON u.id = p.author_id WHERE p.id = ?`, id))
}

// Pull returns the pull request with the given number, or ErrNotFound.
func (s *Store) Pull(ctx context.Context, repoID int64, number int) (PullRequest, error) {
	return scanPull(s.db.QueryRowContext(ctx, `
		SELECT `+pullColumns+` FROM pull_requests p JOIN users u ON u.id = p.author_id
		WHERE p.repo_id = ? AND p.number = ?`, repoID, number))
}

// Pulls lists a repository's pull requests, newest first. An empty state
// lists all of them.
func (s *Store) Pulls(ctx context.Context, repoID int64, state string) ([]PullRequest, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+pullColumns+` FROM pull_requests p JOIN users u ON u.id = p.author_id
		WHERE p.repo_id = ? AND (? = '' OR p.state = ?) ORDER BY p.number DESC`, repoID, state, state)
	if err != nil {
		return nil, fmt.Errorf("list pull requests: %w", err)
	}
	defer rows.Close()
	var out []PullRequest
	for rows.Next() {
		p, err := scanPull(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// OpenPullsTouching returns the open pull requests whose head or base is
// the named branch.
func (s *Store) OpenPullsTouching(ctx context.Context, repoID int64, branch string) ([]PullRequest, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+pullColumns+` FROM pull_requests p JOIN users u ON u.id = p.author_id
		WHERE p.repo_id = ? AND p.state = 'open' AND (p.head_branch = ? OR p.base_branch = ?)
		ORDER BY p.number`, repoID, branch, branch)
	if err != nil {
		return nil, fmt.Errorf("list pull requests: %w", err)
	}
	defer rows.Close()
	var out []PullRequest
	for rows.Next() {
		p, err := scanPull(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// TouchPull records activity on a pull request (a new push, say).
func (s *Store) TouchPull(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE pull_requests SET updated_at = ? WHERE id = ?`, formatTime(time.Now()), id)
	return err
}

// SetPullState moves an open pull request to closed or merged (recording the
// merge commit). It returns ErrPullClosed if the request was not open, which
// makes concurrent merges and closes safe.
func (s *Store) SetPullState(ctx context.Context, id int64, state, mergeCommit string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE pull_requests SET state = ?, merge_commit = NULLIF(?, ''), updated_at = ?
		WHERE id = ? AND state = 'open'`, state, mergeCommit, formatTime(time.Now()), id)
	if err != nil {
		return fmt.Errorf("update pull request: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrPullClosed
	}
	return nil
}

// ReopenPull reopens a closed (not merged) pull request, unless another one
// is open for the same branches.
func (s *Store) ReopenPull(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE pull_requests SET state = 'open', updated_at = ?
		WHERE id = ? AND state = 'closed' AND NOT EXISTS (
			SELECT 1 FROM pull_requests o JOIN pull_requests p ON p.id = ?
			WHERE o.repo_id = p.repo_id AND o.head_branch = p.head_branch
			  AND o.base_branch = p.base_branch AND o.state = 'open')`,
		formatTime(time.Now()), id, id)
	if err != nil {
		return fmt.Errorf("reopen pull request: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrPullExists
	}
	return nil
}

// RepoByID returns the repository with the given ID and its course slug.
func (s *Store) RepoByID(ctx context.Context, id int64) (r Repo, courseSlug string, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT r.id, r.course_id, r.name, r.kind, r.is_template, r.archived, c.slug
		FROM repos r JOIN courses c ON c.id = r.course_id WHERE r.id = ?`, id).
		Scan(&r.ID, &r.CourseID, &r.Name, &r.Kind, &r.IsTemplate, &r.Archived, &courseSlug)
	if errors.Is(err, sql.ErrNoRows) {
		return Repo{}, "", ErrNotFound
	}
	return r, courseSlug, err
}
