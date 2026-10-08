package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Issue states.
const (
	IssueOpen   = "open"
	IssueClosed = "closed"
)

// Issue is one issue of a repository.
type Issue struct {
	ID        int64
	RepoID    int64
	Number    int
	AuthorID  int64
	Author    string
	Title     string
	Body      string
	State     string
	Milestone string
	Labels    []string
	Assignees []string
	CreatedAt time.Time
}

// IssueComment is one entry of an issue's discussion.
type IssueComment struct {
	ID        int64
	Author    string
	Body      string
	CreatedAt time.Time
}

// Milestone groups issues towards a due date.
type Milestone struct {
	ID      int64
	Title   string
	DueDate string
	Open    int
	Closed  int
}

const issueColumns = `i.id, i.repo_id, i.number, i.author_id, u.username, i.title, i.body, i.state,
	COALESCE(m.title, ''), i.created_at`

const issueFrom = ` FROM issues i JOIN users u ON u.id = i.author_id LEFT JOIN milestones m ON m.id = i.milestone_id `

func scanIssue(row rowScanner) (Issue, error) {
	var i Issue
	var created string
	err := row.Scan(&i.ID, &i.RepoID, &i.Number, &i.AuthorID, &i.Author, &i.Title, &i.Body, &i.State, &i.Milestone, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Issue{}, ErrNotFound
	}
	if err != nil {
		return Issue{}, err
	}
	i.CreatedAt, _ = time.Parse(timeLayout, created)
	return i, nil
}

// stringsOf runs a query returning one string column.
func (s *Store) stringsOf(ctx context.Context, q string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// fill loads the labels and assignees of i.
func (s *Store) fillIssue(ctx context.Context, i *Issue) error {
	var err error
	if i.Labels, err = s.stringsOf(ctx, `
		SELECT l.name FROM issue_labels il JOIN labels l ON l.id = il.label_id
		WHERE il.issue_id = ? ORDER BY l.name`, i.ID); err != nil {
		return err
	}
	i.Assignees, err = s.stringsOf(ctx, `
		SELECT u.username FROM issue_assignees a JOIN users u ON u.id = a.user_id
		WHERE a.issue_id = ? ORDER BY u.username`, i.ID)
	return err
}

// CreateIssue opens an issue, numbering it within the repository.
func (s *Store) CreateIssue(ctx context.Context, repoID, authorID int64, title, body string) (Issue, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO issues (repo_id, number, author_id, title, body)
		VALUES (?, (SELECT COALESCE(MAX(number), 0) + 1 FROM issues WHERE repo_id = ?), ?, ?, ?)
		RETURNING id`, repoID, repoID, authorID, title, body).Scan(&id)
	if err != nil {
		return Issue{}, fmt.Errorf("create issue: %w", err)
	}
	return s.issueWhere(ctx, "i.id = ?", id)
}

func (s *Store) issueWhere(ctx context.Context, where string, args ...any) (Issue, error) {
	i, err := scanIssue(s.db.QueryRowContext(ctx, `SELECT `+issueColumns+issueFrom+`WHERE `+where, args...))
	if err != nil {
		return Issue{}, err
	}
	return i, s.fillIssue(ctx, &i)
}

// Issue returns the issue with the given number, or ErrNotFound.
func (s *Store) Issue(ctx context.Context, repoID int64, number int) (Issue, error) {
	return s.issueWhere(ctx, "i.repo_id = ? AND i.number = ?", repoID, number)
}

// Issues lists a repository's issues, newest first, optionally narrowed to a
// state and a label (empty means no filter).
func (s *Store) Issues(ctx context.Context, repoID int64, state, label string) ([]Issue, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+issueColumns+issueFrom+`
		WHERE i.repo_id = ? AND (? = '' OR i.state = ?)
		AND (? = '' OR EXISTS (SELECT 1 FROM issue_labels il JOIN labels l ON l.id = il.label_id
		                       WHERE il.issue_id = i.id AND l.name = ?))
		ORDER BY i.number DESC`, repoID, state, state, label, label)
	if err != nil {
		return nil, fmt.Errorf("list issues: %w", err)
	}
	var out []Issue
	for rows.Next() {
		i, err := scanIssue(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for k := range out {
		if err := s.fillIssue(ctx, &out[k]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// SetIssueState opens or closes an issue.
func (s *Store) SetIssueState(ctx context.Context, id int64, state string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE issues SET state = ?, closed_at = CASE WHEN ? = 'closed' THEN strftime('%Y-%m-%dT%H:%M:%fZ','now') END
		WHERE id = ?`, state, state, id)
	return err
}

// CloseIssues closes the open issues with the given numbers and returns the
// numbers it actually closed.
func (s *Store) CloseIssues(ctx context.Context, repoID int64, numbers []int) ([]int, error) {
	var closed []int
	for _, n := range numbers {
		res, err := s.db.ExecContext(ctx, `
			UPDATE issues SET state = 'closed', closed_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
			WHERE repo_id = ? AND number = ? AND state = 'open'`, repoID, n)
		if err != nil {
			return closed, err
		}
		if c, _ := res.RowsAffected(); c > 0 {
			closed = append(closed, n)
		}
	}
	return closed, nil
}

// AddIssueComment appends a comment to an issue.
func (s *Store) AddIssueComment(ctx context.Context, issueID, authorID int64, body string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO issue_comments (issue_id, author_id, body) VALUES (?, ?, ?)`,
		issueID, authorID, body)
	return err
}

// IssueComments lists an issue's comments, oldest first.
func (s *Store) IssueComments(ctx context.Context, issueID int64) ([]IssueComment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, u.username, c.body, c.created_at FROM issue_comments c
		JOIN users u ON u.id = c.author_id WHERE c.issue_id = ? ORDER BY c.id`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IssueComment
	for rows.Next() {
		var c IssueComment
		var at string
		if err := rows.Scan(&c.ID, &c.Author, &c.Body, &at); err != nil {
			return nil, err
		}
		c.CreatedAt, _ = time.Parse(timeLayout, at)
		out = append(out, c)
	}
	return out, rows.Err()
}

// AddIssueLabel attaches the named label, creating it in the repository if
// needed.
func (s *Store) AddIssueLabel(ctx context.Context, repoID, issueID int64, name string) error {
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO labels (repo_id, name) VALUES (?, ?)`, repoID, name); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO issue_labels (issue_id, label_id)
		SELECT ?, id FROM labels WHERE repo_id = ? AND name = ?`, issueID, repoID, name)
	return err
}

// RemoveIssueLabel detaches the named label from the issue.
func (s *Store) RemoveIssueLabel(ctx context.Context, repoID, issueID int64, name string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM issue_labels WHERE issue_id = ?
		AND label_id IN (SELECT id FROM labels WHERE repo_id = ? AND name = ?)`, issueID, repoID, name)
	return err
}

// Labels lists the label names used in the repository.
func (s *Store) Labels(ctx context.Context, repoID int64) ([]string, error) {
	return s.stringsOf(ctx, `SELECT name FROM labels WHERE repo_id = ? ORDER BY name`, repoID)
}

// SetIssueAssignee adds or removes an assignee.
func (s *Store) SetIssueAssignee(ctx context.Context, issueID, userID int64, assigned bool) error {
	q := `DELETE FROM issue_assignees WHERE issue_id = ? AND user_id = ?`
	if assigned {
		q = `INSERT OR IGNORE INTO issue_assignees (issue_id, user_id) VALUES (?, ?)`
	}
	_, err := s.db.ExecContext(ctx, q, issueID, userID)
	return err
}

// CreateMilestone adds a milestone; due is an optional YYYY-MM-DD date.
func (s *Store) CreateMilestone(ctx context.Context, repoID int64, title, due string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO milestones (repo_id, title, due_date) VALUES (?, ?, ?)
		ON CONFLICT (repo_id, title) DO UPDATE SET due_date = excluded.due_date`, repoID, title, due)
	return err
}

// Milestones lists the repository's milestones with their issue counts.
func (s *Store) Milestones(ctx context.Context, repoID int64) ([]Milestone, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.title, m.due_date,
		       COALESCE(SUM(i.state = 'open'), 0), COALESCE(SUM(i.state = 'closed'), 0)
		FROM milestones m LEFT JOIN issues i ON i.milestone_id = m.id
		WHERE m.repo_id = ? GROUP BY m.id ORDER BY m.due_date = '', m.due_date, m.title`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Milestone
	for rows.Next() {
		var m Milestone
		if err := rows.Scan(&m.ID, &m.Title, &m.DueDate, &m.Open, &m.Closed); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetIssueMilestone moves the issue to the named milestone of its repository;
// an empty title clears it. It returns ErrNotFound for an unknown title.
func (s *Store) SetIssueMilestone(ctx context.Context, repoID, issueID int64, title string) error {
	if title == "" {
		_, err := s.db.ExecContext(ctx, `UPDATE issues SET milestone_id = NULL WHERE id = ?`, issueID)
		return err
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE issues SET milestone_id = (SELECT id FROM milestones WHERE repo_id = ? AND title = ?)
		WHERE id = ? AND EXISTS (SELECT 1 FROM milestones WHERE repo_id = ? AND title = ?)`,
		repoID, title, issueID, repoID, title)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
