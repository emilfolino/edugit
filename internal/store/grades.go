package store

import (
	"context"
	"database/sql"
	"errors"
)

// Criterion is one rubric line of an assignment.
type Criterion struct {
	ID        int64
	Title     string
	MaxPoints int
}

// Grade is the staff grading of one generated repo. Scores is keyed by
// criterion ID.
type Grade struct {
	RepoID   int64
	Feedback string
	Scores   map[int64]int
}

// GradeRow is one student's line in the grade export; team repos yield one
// row per member.
type GradeRow struct {
	Username string
	Email    string
	Repo     string
	Graded   bool
	Points   int
	Feedback string
}

// Rubric lists the criteria of an assignment in display order.
func (s *Store) Rubric(ctx context.Context, assignmentID int64) ([]Criterion, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, title, max_points FROM rubric_criteria
		WHERE assignment_id = ? ORDER BY position, id`, assignmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Criterion
	for rows.Next() {
		var c Criterion
		if err := rows.Scan(&c.ID, &c.Title, &c.MaxPoints); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AddCriterion appends a criterion to the assignment's rubric.
func (s *Store) AddCriterion(ctx context.Context, assignmentID int64, title string, maxPoints int) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO rubric_criteria (assignment_id, title, max_points, position)
		VALUES (?, ?, ?, (SELECT COALESCE(MAX(position), 0) + 1 FROM rubric_criteria WHERE assignment_id = ?))`,
		assignmentID, title, maxPoints, assignmentID)
	return err
}

// DeleteCriterion removes a criterion and the scores given against it.
func (s *Store) DeleteCriterion(ctx context.Context, assignmentID, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM rubric_criteria WHERE id = ? AND assignment_id = ?`, id, assignmentID)
	return err
}

// GradeFor returns the grade of a repo, or ErrNotFound if it is ungraded.
func (s *Store) GradeFor(ctx context.Context, repoID int64) (Grade, error) {
	g := Grade{RepoID: repoID, Scores: map[int64]int{}}
	err := s.db.QueryRowContext(ctx, `SELECT feedback FROM grades WHERE repo_id = ?`, repoID).Scan(&g.Feedback)
	if errors.Is(err, sql.ErrNoRows) {
		return Grade{}, ErrNotFound
	}
	if err != nil {
		return Grade{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT criterion_id, points FROM grade_scores WHERE repo_id = ?`, repoID)
	if err != nil {
		return Grade{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var p int
		if err := rows.Scan(&id, &p); err != nil {
			return Grade{}, err
		}
		g.Scores[id] = p
	}
	return g, rows.Err()
}

// GradeTotals returns the total points per graded repo of an assignment.
func (s *Store) GradeTotals(ctx context.Context, assignmentID int64) (map[int64]int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT g.repo_id, COALESCE(SUM(gs.points), 0)
		FROM grades g LEFT JOIN grade_scores gs ON gs.repo_id = g.repo_id
		WHERE g.assignment_id = ? GROUP BY g.repo_id`, assignmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var p int
		if err := rows.Scan(&id, &p); err != nil {
			return nil, err
		}
		out[id] = p
	}
	return out, rows.Err()
}

// SaveGrade replaces the grade of a repo. Scores for criteria outside the
// assignment's rubric or above their maximum are rejected.
func (s *Store) SaveGrade(ctx context.Context, assignmentID, repoID, graderID int64, feedback string, scores map[int64]int) error {
	rubric, err := s.Rubric(ctx, assignmentID)
	if err != nil {
		return err
	}
	max := map[int64]int{}
	for _, c := range rubric {
		max[c.ID] = c.MaxPoints
	}
	for id, p := range scores {
		m, ok := max[id]
		if !ok || p < 0 || p > m {
			return errors.New("store: score outside rubric")
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO grades (repo_id, assignment_id, feedback, graded_by) VALUES (?, ?, ?, ?)
		ON CONFLICT (repo_id) DO UPDATE SET feedback = excluded.feedback,
			graded_by = excluded.graded_by, graded_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
		repoID, assignmentID, feedback, graderID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM grade_scores WHERE repo_id = ?`, repoID); err != nil {
		return err
	}
	for id, p := range scores {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO grade_scores (repo_id, criterion_id, points) VALUES (?, ?, ?)`, repoID, id, p); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GradeRows lists every student of the assignment's repos with their grade,
// for export. Team repos are expanded to their members.
func (s *Store) GradeRows(ctx context.Context, assignmentID int64) ([]GradeRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.username, u.email, r.name, g.repo_id IS NOT NULL,
			COALESCE((SELECT SUM(points) FROM grade_scores WHERE repo_id = r.id), 0),
			COALESCE(g.feedback, '')
		FROM assignment_repos ar
		JOIN repos r ON r.id = ar.repo_id
		JOIN users u ON u.id = ar.user_id
			OR u.id IN (SELECT user_id FROM team_members WHERE team_id = ar.team_id)
		LEFT JOIN grades g ON g.repo_id = r.id
		WHERE ar.assignment_id = ? ORDER BY u.username`, assignmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GradeRow
	for rows.Next() {
		var g GradeRow
		if err := rows.Scan(&g.Username, &g.Email, &g.Repo, &g.Graded, &g.Points, &g.Feedback); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
