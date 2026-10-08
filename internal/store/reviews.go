package store

import (
	"context"
	"database/sql"
	"errors"
	"path"
	"time"
)

// Review states.
const (
	ReviewComment        = "comment"
	ReviewApprove        = "approve"
	ReviewRequestChanges = "request_changes"
)

// Review is one submitted review of a pull request.
type Review struct {
	ID         int64
	PRID       int64
	ReviewerID int64
	Reviewer   string
	State      string
	Body       string
	CommitSHA  string
	CreatedAt  time.Time
}

// Comment is a general (empty Path) or inline review comment.
type Comment struct {
	ID        int64
	PRID      int64
	ReviewID  int64 // 0 when posted outside a review
	AuthorID  int64
	Author    string
	Body      string
	Path      string
	Line      int
	CommitSHA string
	Resolved  bool
	CreatedAt time.Time
}

// AddReview records a review of the pull request at head commit sha.
func (s *Store) AddReview(ctx context.Context, prID, reviewerID int64, state, body, sha string) (Review, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO reviews (pr_id, reviewer_id, state, body, commit_sha) VALUES (?, ?, ?, ?, ?)`,
		prID, reviewerID, state, body, sha)
	if err != nil {
		return Review{}, err
	}
	id, _ := res.LastInsertId()
	return s.reviewByID(ctx, id)
}

func (s *Store) reviewByID(ctx context.Context, id int64) (Review, error) {
	rs, err := s.reviews(ctx, `WHERE r.id = ?`, id)
	if err != nil {
		return Review{}, err
	}
	if len(rs) == 0 {
		return Review{}, ErrNotFound
	}
	return rs[0], nil
}

func (s *Store) reviews(ctx context.Context, where string, args ...any) ([]Review, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.id, r.pr_id, r.reviewer_id, u.username, r.state, r.body, r.commit_sha, r.created_at
		FROM reviews r JOIN users u ON u.id = r.reviewer_id `+where+` ORDER BY r.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Review
	for rows.Next() {
		var r Review
		var created string
		if err := rows.Scan(&r.ID, &r.PRID, &r.ReviewerID, &r.Reviewer, &r.State, &r.Body, &r.CommitSHA, &created); err != nil {
			return nil, err
		}
		r.CreatedAt, _ = time.Parse(timeLayout, created)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Reviews lists the reviews of a pull request, oldest first.
func (s *Store) Reviews(ctx context.Context, prID int64) ([]Review, error) {
	return s.reviews(ctx, `WHERE r.pr_id = ?`, prID)
}

// AddComment records a comment. An empty path makes it a general comment.
func (s *Store) AddComment(ctx context.Context, prID, reviewID, authorID int64, body, path string, line int, sha string) (Comment, error) {
	var rid, ln, p any
	if reviewID != 0 {
		rid = reviewID
	}
	if path != "" {
		p, ln = path, line
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO comments (pr_id, review_id, author_id, body, path, line, commit_sha)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, prID, rid, authorID, body, p, ln, sha)
	if err != nil {
		return Comment{}, err
	}
	id, _ := res.LastInsertId()
	cs, err := s.comments(ctx, `WHERE c.id = ?`, id)
	if err != nil {
		return Comment{}, err
	}
	return cs[0], nil
}

func (s *Store) comments(ctx context.Context, where string, args ...any) ([]Comment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.pr_id, COALESCE(c.review_id, 0), c.author_id, u.username, c.body,
		       COALESCE(c.path, ''), COALESCE(c.line, 0), c.commit_sha, c.resolved, c.created_at
		FROM comments c JOIN users u ON u.id = c.author_id `+where+` ORDER BY c.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Comment
	for rows.Next() {
		var c Comment
		var created string
		if err := rows.Scan(&c.ID, &c.PRID, &c.ReviewID, &c.AuthorID, &c.Author, &c.Body,
			&c.Path, &c.Line, &c.CommitSHA, &c.Resolved, &created); err != nil {
			return nil, err
		}
		c.CreatedAt, _ = time.Parse(timeLayout, created)
		out = append(out, c)
	}
	return out, rows.Err()
}

// Comments lists the comments of a pull request, oldest first.
func (s *Store) Comments(ctx context.Context, prID int64) ([]Comment, error) {
	return s.comments(ctx, `WHERE c.pr_id = ?`, prID)
}

// ResolveComment marks a comment of the pull request resolved or not.
func (s *Store) ResolveComment(ctx context.Context, prID, commentID int64, resolved bool) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE comments SET resolved = ? WHERE id = ? AND pr_id = ?`, resolved, commentID, prID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ApprovalState summarizes the decisive reviews of a pull request.
type ApprovalState struct {
	// Approvals counts reviewers whose latest decisive review is an approval
	// of the head commit. An approval of an older commit is stale.
	Approvals int
	// ChangesRequested counts reviewers whose latest decisive review is a
	// change request.
	ChangesRequested int
}

// Approvals computes the approval state at head commit sha. Plain comment
// reviews are not decisive. The author's own reviews never count.
func (s *Store) Approvals(ctx context.Context, prID int64, sha string) (ApprovalState, error) {
	var st ApprovalState
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.state, r.commit_sha FROM reviews r
		JOIN pull_requests p ON p.id = r.pr_id
		WHERE r.pr_id = ? AND r.state != 'comment' AND r.reviewer_id != p.author_id
		  AND r.id = (SELECT MAX(r2.id) FROM reviews r2
		              WHERE r2.pr_id = r.pr_id AND r2.reviewer_id = r.reviewer_id AND r2.state != 'comment')`, prID)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var state, at string
		if err := rows.Scan(&state, &at); err != nil {
			return st, err
		}
		switch {
		case state == ReviewRequestChanges:
			st.ChangesRequested++
		case at == sha:
			st.Approvals++
		}
	}
	return st, rows.Err()
}

// RequestReview asks reviewerID to review the pull request; repeating is a
// no-op.
func (s *Store) RequestReview(ctx context.Context, prID, reviewerID, byID int64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO review_requests (pr_id, reviewer_id, requested_by) VALUES (?, ?, ?)`,
		prID, reviewerID, byID)
	return err
}

// ReviewRequests lists the usernames asked to review the pull request.
func (s *Store) ReviewRequests(ctx context.Context, prID int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.username FROM review_requests q JOIN users u ON u.id = q.reviewer_id
		WHERE q.pr_id = ? ORDER BY q.created_at, u.username`, prID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// IsRequestedReviewer reports whether the user was asked to review any open
// pull request of the repository.
func (s *Store) IsRequestedReviewer(ctx context.Context, repoID, userID int64) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `
		SELECT 1 FROM review_requests q JOIN pull_requests p ON p.id = q.pr_id
		WHERE p.repo_id = ? AND q.reviewer_id = ? AND p.state = 'open' LIMIT 1`, repoID, userID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// RequiredApprovals returns the approvals the repository's rules demand for
// merging into branch: the maximum over matching rules.
func (s *Store) RequiredApprovals(ctx context.Context, repoID int64, branch string) (int, error) {
	rules, err := s.repoRules(ctx, repoID)
	if err != nil {
		return 0, err
	}
	need := 0
	for _, r := range rules {
		if ok, _ := path.Match(r.Pattern, branch); ok && r.RequiredApprovals > need {
			need = r.RequiredApprovals
		}
	}
	return need, nil
}

// SetRequiredApprovals sets (creating the rule if needed) the approvals
// required to merge into branches matching pattern.
func (s *Store) SetRequiredApprovals(ctx context.Context, repoID int64, pattern string, n int) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO branch_protections (repo_id, pattern, require_pr, required_approvals)
		VALUES (?, ?, 1, ?)
		ON CONFLICT (repo_id, pattern) DO UPDATE SET required_approvals = excluded.required_approvals`,
		repoID, pattern, n)
	return err
}
