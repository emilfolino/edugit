package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Errors reported by team operations.
var (
	ErrTeamFull      = errors.New("team is full")
	ErrAlreadyInTeam = errors.New("already in a team for this assignment")
	ErrHasRepo       = errors.New("repository already exists for this assignment")
)

// Assignment is an assignment row. A zero Deadline means none.
type Assignment struct {
	ID             int64
	CourseID       int64
	TemplateRepoID int64
	TemplateName   string
	Slug           string
	Title          string
	Mode           string // "individual" or "team"
	History        string // "fresh" or "copy"
	TeamSize       int
	Deadline       time.Time
}

// Team is a group working on a team-mode assignment.
type Team struct {
	ID      int64
	Name    string
	RepoID  int64
	Repo    string
	Members []string // usernames
}

const assignmentCols = `a.id, a.course_id, a.template_repo_id, r.name, a.slug, a.title, a.mode, a.history, a.team_size, a.deadline`

type rowScanner interface{ Scan(...any) error }

func scanAssignment(row rowScanner) (Assignment, error) {
	var a Assignment
	var dl *string
	err := row.Scan(&a.ID, &a.CourseID, &a.TemplateRepoID, &a.TemplateName, &a.Slug, &a.Title, &a.Mode, &a.History, &a.TeamSize, &dl)
	if errors.Is(err, sql.ErrNoRows) {
		return Assignment{}, ErrNotFound
	}
	if err == nil && dl != nil {
		a.Deadline, err = time.Parse(timeLayout, *dl)
	}
	return a, err
}

// CreateAssignment inserts an assignment; ID is set on the result.
func (s *Store) CreateAssignment(ctx context.Context, a Assignment) (Assignment, error) {
	var dl *string
	if !a.Deadline.IsZero() {
		v := formatTime(a.Deadline)
		dl = &v
	}
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO assignments (course_id, template_repo_id, slug, title, mode, history, team_size, deadline)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		a.CourseID, a.TemplateRepoID, a.Slug, a.Title, a.Mode, a.History, a.TeamSize, dl).Scan(&a.ID)
	if err != nil {
		return Assignment{}, fmt.Errorf("create assignment %q: %w", a.Slug, err)
	}
	return a, nil
}

// CourseAssignments lists a course's assignments, newest first.
func (s *Store) CourseAssignments(ctx context.Context, courseID int64) ([]Assignment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+assignmentCols+` FROM assignments a JOIN repos r ON r.id = a.template_repo_id
		WHERE a.course_id = ? ORDER BY a.id DESC`, courseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Assignment
	for rows.Next() {
		a, err := scanAssignment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AssignmentBySlug returns the assignment or ErrNotFound.
func (s *Store) AssignmentBySlug(ctx context.Context, courseID int64, slug string) (Assignment, error) {
	return scanAssignment(s.db.QueryRowContext(ctx, `
		SELECT `+assignmentCols+` FROM assignments a JOIN repos r ON r.id = a.template_repo_id
		WHERE a.course_id = ? AND a.slug = ?`, courseID, slug))
}

// AssignmentRepoFor returns the repo the user works in for the assignment:
// their own repo, or their team's. It returns ErrNotFound if there is none.
func (s *Store) AssignmentRepoFor(ctx context.Context, assignmentID, userID int64) (Repo, error) {
	var r Repo
	err := s.db.QueryRowContext(ctx, `
		SELECT r.id, r.course_id, r.name, r.kind, r.is_template, r.archived
		FROM assignment_repos ar JOIN repos r ON r.id = ar.repo_id
		WHERE ar.assignment_id = ? AND (ar.user_id = ? OR ar.team_id IN
			(SELECT team_id FROM team_members WHERE assignment_id = ? AND user_id = ?))`,
		assignmentID, userID, assignmentID, userID).
		Scan(&r.ID, &r.CourseID, &r.Name, &r.Kind, &r.IsTemplate, &r.Archived)
	if errors.Is(err, sql.ErrNoRows) {
		return Repo{}, ErrNotFound
	}
	return r, err
}

// AssignmentRepos lists the generated repos of an assignment with their owners
// (username for individual repos, team name for team repos).
func (s *Store) AssignmentRepos(ctx context.Context, assignmentID int64) ([]AssignedRepo, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.id, r.name, r.archived, COALESCE(u.username, t.name, '')
		FROM assignment_repos ar JOIN repos r ON r.id = ar.repo_id
		LEFT JOIN users u ON u.id = ar.user_id
		LEFT JOIN teams t ON t.id = ar.team_id
		WHERE ar.assignment_id = ? ORDER BY r.name`, assignmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AssignedRepo
	for rows.Next() {
		var a AssignedRepo
		if err := rows.Scan(&a.RepoID, &a.Name, &a.Archived, &a.Owner); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AssignedRepo is a generated repo and who owns it.
type AssignedRepo struct {
	RepoID   int64
	Name     string
	Archived bool
	Owner    string
}

// CreateAssignmentRepo records a generated individual repo owned by userID:
// the repo row, the assignment link and the owner's membership, atomically.
// The disk repo is created by the caller.
func (s *Store) CreateAssignmentRepo(ctx context.Context, a Assignment, name string, userID int64) (Repo, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Repo{}, err
	}
	defer tx.Rollback()
	var exists bool
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM assignment_repos WHERE assignment_id = ? AND user_id = ?)`,
		a.ID, userID).Scan(&exists); err != nil {
		return Repo{}, err
	}
	if exists {
		return Repo{}, ErrHasRepo
	}
	r, err := insertAssignmentRepo(ctx, tx, a, name, "student", sql.NullInt64{Int64: userID, Valid: true}, sql.NullInt64{})
	if err != nil {
		return Repo{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO repo_members (repo_id, user_id) VALUES (?, ?)`, r.ID, userID); err != nil {
		return Repo{}, err
	}
	return r, tx.Commit()
}

func insertAssignmentRepo(ctx context.Context, tx *sql.Tx, a Assignment, name, kind string, userID, teamID sql.NullInt64) (Repo, error) {
	r := Repo{CourseID: a.CourseID, Name: name, Kind: kind}
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO repos (course_id, name, kind) VALUES (?, ?, ?) RETURNING id`, a.CourseID, name, kind).Scan(&r.ID); err != nil {
		return Repo{}, fmt.Errorf("create repo %q: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO assignment_repos (assignment_id, repo_id, user_id, team_id) VALUES (?, ?, ?, ?)`,
		a.ID, r.ID, userID, teamID); err != nil {
		return Repo{}, err
	}
	// Students work through pull requests; feedback is reviewed the same way.
	for _, pattern := range ProtectedBranches {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO branch_protections (repo_id, pattern, require_pr) VALUES (?, ?, 1)`, r.ID, pattern); err != nil {
			return Repo{}, err
		}
	}
	return r, nil
}

// ProtectedBranches are the branches of a generated repo that only pull
// requests may change.
var ProtectedBranches = []string{"main", FeedbackBranch}

// FeedbackBranch is the branch created at the starting commit of a generated
// repo; the teacher's feedback pull request targets it.
const FeedbackBranch = "feedback"

// deadlinePassed matches generated repos (ar, a) whose effective deadline,
// the assignment's or a later one from an extension of the owner or a team
// mate, is not after ?1.
const deadlinePassed = `a.deadline IS NOT NULL AND a.deadline <= ?1 AND NOT EXISTS (
	SELECT 1 FROM assignment_extensions e WHERE e.assignment_id = a.id AND e.deadline > ?1 AND
	(e.user_id = ar.user_id OR e.user_id IN (SELECT tm.user_id FROM team_members tm WHERE tm.team_id = ar.team_id)))`

// SyncLocks archives generated repos whose deadline has passed and reopens
// those it archived earlier whose deadline an extension has since moved past
// now. It returns the number of repos locked and reopened.
func (s *Store) SyncLocks(ctx context.Context, now time.Time) (locked, reopened int, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	at := formatTime(now)
	const from = ` FROM assignment_repos ar JOIN assignments a ON a.id = ar.assignment_id WHERE `
	ids := func(where string) ([]int64, error) {
		rows, err := tx.QueryContext(ctx, `SELECT ar.repo_id`+from+where, at)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			out = append(out, id)
		}
		return out, rows.Err()
	}
	set := func(ids []int64, archived bool, lockedAt any) error {
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `UPDATE repos SET archived = ? WHERE id = ?`, archived, id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE assignment_repos SET locked_at = ? WHERE repo_id = ?`, lockedAt, id); err != nil {
				return err
			}
		}
		return nil
	}
	reopen, err := ids(`ar.locked_at IS NOT NULL AND NOT (` + deadlinePassed + `)`)
	if err != nil {
		return 0, 0, fmt.Errorf("find repos to reopen: %w", err)
	}
	if err := set(reopen, false, nil); err != nil {
		return 0, 0, err
	}
	lock, err := ids(`ar.locked_at IS NULL AND ` + deadlinePassed)
	if err != nil {
		return 0, 0, fmt.Errorf("find repos to lock: %w", err)
	}
	if err := set(lock, true, at); err != nil {
		return 0, 0, err
	}
	return len(lock), len(reopen), tx.Commit()
}

// CreateTeam creates a team and its shared repo with userID as first member.
// It returns ErrAlreadyInTeam if the user already has a team.
func (s *Store) CreateTeam(ctx context.Context, a Assignment, teamName, repoName string, userID int64) (Team, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Team{}, err
	}
	defer tx.Rollback()
	t := Team{Name: teamName, Repo: repoName, Members: nil}
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO teams (assignment_id, name) VALUES (?, ?) RETURNING id`, a.ID, teamName).Scan(&t.ID); err != nil {
		return Team{}, fmt.Errorf("create team %q: %w", teamName, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO team_members (team_id, user_id, assignment_id) VALUES (?, ?, ?)`, t.ID, userID, a.ID); err != nil {
		if isUnique(err) {
			return Team{}, ErrAlreadyInTeam
		}
		return Team{}, err
	}
	r, err := insertAssignmentRepo(ctx, tx, a, repoName, "team", sql.NullInt64{}, sql.NullInt64{Int64: t.ID, Valid: true})
	if err != nil {
		return Team{}, err
	}
	t.RepoID = r.ID
	if _, err := tx.ExecContext(ctx, `INSERT INTO repo_members (repo_id, user_id) VALUES (?, ?)`, r.ID, userID); err != nil {
		return Team{}, err
	}
	return t, tx.Commit()
}

// JoinTeam adds userID to the team, enforcing the assignment's size limit and
// one team per student. It returns ErrNotFound for a team of another
// assignment.
func (s *Store) JoinTeam(ctx context.Context, a Assignment, teamID, userID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var repoID int64
	err = tx.QueryRowContext(ctx, `
		SELECT ar.repo_id FROM teams t JOIN assignment_repos ar ON ar.team_id = t.id
		WHERE t.id = ? AND t.assignment_id = ?`, teamID, a.ID).Scan(&repoID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM team_members WHERE team_id = ?`, teamID).Scan(&n); err != nil {
		return err
	}
	if n >= a.TeamSize {
		return ErrTeamFull
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO team_members (team_id, user_id, assignment_id) VALUES (?, ?, ?)`, teamID, userID, a.ID); err != nil {
		if isUnique(err) {
			return ErrAlreadyInTeam
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO repo_members (repo_id, user_id) VALUES (?, ?)`, repoID, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// LeaveTeam removes userID from their team of the assignment, and from the
// team repo. The repo and its history stay, even for an empty team. It
// returns ErrNotFound if the user is in no team.
func (s *Store) LeaveTeam(ctx context.Context, assignmentID, userID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var teamID int64
	err = tx.QueryRowContext(ctx,
		`SELECT team_id FROM team_members WHERE assignment_id = ? AND user_id = ?`, assignmentID, userID).Scan(&teamID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM repo_members WHERE user_id = ? AND repo_id IN
		(SELECT repo_id FROM assignment_repos WHERE team_id = ?)`, userID, teamID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM team_members WHERE team_id = ? AND user_id = ?`, teamID, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// Teams lists an assignment's teams with their members, by name.
func (s *Store) Teams(ctx context.Context, assignmentID int64) ([]Team, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.name, ar.repo_id, r.name, COALESCE(u.username, '')
		FROM teams t
		JOIN assignment_repos ar ON ar.team_id = t.id
		JOIN repos r ON r.id = ar.repo_id
		LEFT JOIN team_members m ON m.team_id = t.id
		LEFT JOIN users u ON u.id = m.user_id
		WHERE t.assignment_id = ? ORDER BY t.name, u.username`, assignmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Team
	for rows.Next() {
		var t Team
		var member string
		if err := rows.Scan(&t.ID, &t.Name, &t.RepoID, &t.Repo, &member); err != nil {
			return nil, err
		}
		if n := len(out); n > 0 && out[n-1].ID == t.ID {
			t = out[n-1]
			out = out[:n-1]
		}
		if member != "" {
			t.Members = append(t.Members, member)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SetExtension gives userID an individual deadline for the assignment, or
// removes it when deadline is zero.
func (s *Store) SetExtension(ctx context.Context, assignmentID, userID int64, deadline time.Time) error {
	if deadline.IsZero() {
		_, err := s.db.ExecContext(ctx,
			`DELETE FROM assignment_extensions WHERE assignment_id = ? AND user_id = ?`, assignmentID, userID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO assignment_extensions (assignment_id, user_id, deadline) VALUES (?, ?, ?)
		ON CONFLICT (assignment_id, user_id) DO UPDATE SET deadline = excluded.deadline`,
		assignmentID, userID, formatTime(deadline))
	return err
}

// EffectiveDeadline is the latest of the assignment deadline and the
// extensions of userID and, in team mode, of their team mates. It is zero when
// the assignment has no deadline.
func (s *Store) EffectiveDeadline(ctx context.Context, a Assignment, userID int64) (time.Time, error) {
	if a.Deadline.IsZero() {
		return time.Time{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT deadline FROM assignment_extensions WHERE assignment_id = ? AND (user_id = ? OR user_id IN
			(SELECT m2.user_id FROM team_members m1 JOIN team_members m2 ON m2.team_id = m1.team_id
			 WHERE m1.assignment_id = ? AND m1.user_id = ?))`, a.ID, userID, a.ID, userID)
	if err != nil {
		return time.Time{}, err
	}
	defer rows.Close()
	best := a.Deadline
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return time.Time{}, err
		}
		t, err := time.Parse(timeLayout, v)
		if err != nil {
			return time.Time{}, err
		}
		if t.After(best) {
			best = t
		}
	}
	return best, rows.Err()
}

// PendingStudents lists students enrolled in the course who have no repo for
// the individual-mode assignment yet.
func (s *Store) PendingStudents(ctx context.Context, a Assignment) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.username FROM memberships m JOIN users u ON u.id = m.user_id
		WHERE m.course_id = ? AND m.role = 'student'
		  AND NOT EXISTS (SELECT 1 FROM assignment_repos ar WHERE ar.assignment_id = ? AND ar.user_id = u.id)
		ORDER BY u.username`, a.CourseID, a.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// IsConflict reports whether err is a uniqueness violation. It matches on the
// message so the store stays driver-agnostic.
func IsConflict(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func isUnique(err error) bool { return IsConflict(err) }

// DeleteTeam removes a team, its memberships and its repo row. It undoes a
// CreateTeam whose disk step failed.
func (s *Store) DeleteTeam(ctx context.Context, teamID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM repos WHERE id IN (SELECT repo_id FROM assignment_repos WHERE team_id = ?)`, teamID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM teams WHERE id = ?`, teamID); err != nil {
		return err
	}
	return tx.Commit()
}
