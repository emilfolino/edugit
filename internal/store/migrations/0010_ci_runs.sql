-- One row per CI job execution. spec is the validated job as JSON, kept so a
-- queued run can resume after a restart without re-reading the repo.
CREATE TABLE ci_runs (
    id          INTEGER PRIMARY KEY,
    repo_id     INTEGER NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    sha         TEXT NOT NULL,
    branch      TEXT NOT NULL,
    job         TEXT NOT NULL,
    spec        TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'success', 'failure', 'error')),
    log         TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    started_at  TEXT,
    finished_at TEXT
);
CREATE INDEX ci_runs_repo ON ci_runs (repo_id, id DESC);
CREATE INDEX ci_runs_sha ON ci_runs (repo_id, sha);
