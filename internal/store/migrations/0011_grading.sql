-- Rubric criteria per assignment, and per-repo grades scored against them.
CREATE TABLE rubric_criteria (
    id            INTEGER PRIMARY KEY,
    assignment_id INTEGER NOT NULL REFERENCES assignments(id) ON DELETE CASCADE,
    title         TEXT NOT NULL,
    max_points    INTEGER NOT NULL CHECK (max_points > 0),
    position      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX rubric_criteria_assignment ON rubric_criteria (assignment_id, position, id);

CREATE TABLE grades (
    repo_id       INTEGER PRIMARY KEY REFERENCES repos(id) ON DELETE CASCADE,
    assignment_id INTEGER NOT NULL REFERENCES assignments(id) ON DELETE CASCADE,
    feedback      TEXT NOT NULL DEFAULT '',
    graded_by     INTEGER REFERENCES users(id) ON DELETE SET NULL,
    graded_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE grade_scores (
    repo_id      INTEGER NOT NULL REFERENCES grades(repo_id) ON DELETE CASCADE,
    criterion_id INTEGER NOT NULL REFERENCES rubric_criteria(id) ON DELETE CASCADE,
    points       INTEGER NOT NULL CHECK (points >= 0),
    PRIMARY KEY (repo_id, criterion_id)
);
