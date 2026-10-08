package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// CI run states.
const (
	CIQueued  = "queued"
	CIRunning = "running"
	CISuccess = "success"
	CIFailure = "failure"
	CIError   = "error" // the runner failed, not the job
)

// CIRun is one execution of a CI job against a commit.
type CIRun struct {
	ID       int64
	RepoID   int64
	Course   string // filled by QueuedCIRuns
	Repo     string // filled by QueuedCIRuns
	SHA      string
	Branch   string
	Job      string
	Spec     string
	Status   string
	Log      string
	Created  time.Time
	Started  sql.NullTime
	Finished sql.NullTime
}

// Done reports whether the run has finished.
func (r CIRun) Done() bool { return r.Status != CIQueued && r.Status != CIRunning }

const ciCols = `id, repo_id, sha, branch, job, spec, status, log, created_at, started_at, finished_at`

func scanCIRun(sc interface{ Scan(...any) error }) (CIRun, error) {
	var r CIRun
	var created string
	var started, finished sql.NullString
	if err := sc.Scan(&r.ID, &r.RepoID, &r.SHA, &r.Branch, &r.Job, &r.Spec, &r.Status, &r.Log, &created, &started, &finished); err != nil {
		return CIRun{}, err
	}
	r.Created = parseTime(created)
	if started.Valid {
		r.Started = sql.NullTime{Time: parseTime(started.String), Valid: true}
	}
	if finished.Valid {
		r.Finished = sql.NullTime{Time: parseTime(finished.String), Valid: true}
	}
	return r, nil
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(timeLayout, s)
	return t
}

// CreateCIRun queues a job run.
func (s *Store) CreateCIRun(ctx context.Context, repoID int64, sha, branch, job, spec string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO ci_runs (repo_id, sha, branch, job, spec) VALUES (?, ?, ?, ?, ?)`,
		repoID, sha, branch, job, spec)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// StartCIRun marks a queued run as running.
func (s *Store) StartCIRun(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE ci_runs SET status = 'running', started_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ? AND status = 'queued'`, id)
	return err
}

// FinishCIRun records the outcome and log of a run.
func (s *Store) FinishCIRun(ctx context.Context, id int64, status, log string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE ci_runs SET status = ?, log = ?, finished_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`,
		status, log, id)
	return err
}

// FailInterruptedCIRuns marks runs left "running" by a previous process as
// errored, and returns how many there were.
func (s *Store) FailInterruptedCIRuns(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE ci_runs SET status = 'error', log = 'interrupted by a server restart',
		       finished_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
		WHERE status = 'running'`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// QueuedCIRuns lists queued runs oldest first, with their course and repo.
func (s *Store) QueuedCIRuns(ctx context.Context) ([]CIRun, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT q.id, q.repo_id, q.sha, q.branch, q.job, q.spec, q.status, q.log, q.created_at, q.started_at, q.finished_at,
		       c.slug, r.name
		FROM ci_runs q JOIN repos r ON r.id = q.repo_id JOIN courses c ON c.id = r.course_id
		WHERE q.status = 'queued' ORDER BY q.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CIRun
	for rows.Next() {
		var run CIRun
		var created string
		var started, finished sql.NullString
		if err := rows.Scan(&run.ID, &run.RepoID, &run.SHA, &run.Branch, &run.Job, &run.Spec, &run.Status, &run.Log,
			&created, &started, &finished, &run.Course, &run.Repo); err != nil {
			return nil, err
		}
		run.Created = parseTime(created)
		out = append(out, run)
	}
	return out, rows.Err()
}

// CIRuns lists a repo's most recent runs, newest first, without logs.
func (s *Store) CIRuns(ctx context.Context, repoID int64, limit int) ([]CIRun, error) {
	return s.ciList(ctx, `SELECT id, repo_id, sha, branch, job, spec, status, '', created_at, started_at, finished_at
		FROM ci_runs WHERE repo_id = ? ORDER BY id DESC LIMIT ?`, repoID, limit)
}

// CIRunsForSHA lists the runs of one commit, without logs.
func (s *Store) CIRunsForSHA(ctx context.Context, repoID int64, sha string) ([]CIRun, error) {
	return s.ciList(ctx, `SELECT id, repo_id, sha, branch, job, spec, status, '', created_at, started_at, finished_at
		FROM ci_runs WHERE repo_id = ? AND sha = ? ORDER BY id`, repoID, sha)
}

func (s *Store) ciList(ctx context.Context, q string, args ...any) ([]CIRun, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CIRun
	for rows.Next() {
		r, err := scanCIRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CIRunByID returns one run of the repo, with its log.
func (s *Store) CIRunByID(ctx context.Context, repoID, id int64) (CIRun, error) {
	r, err := scanCIRun(s.db.QueryRowContext(ctx,
		`SELECT `+ciCols+` FROM ci_runs WHERE repo_id = ? AND id = ?`, repoID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return CIRun{}, ErrNotFound
	}
	return r, err
}
