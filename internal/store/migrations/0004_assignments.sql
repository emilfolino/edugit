-- Assignment options, repo ownership, teams and deadline extensions (TODO #11).

ALTER TABLE assignments ADD COLUMN history   TEXT NOT NULL DEFAULT 'fresh' CHECK (history IN ('fresh', 'copy'));
ALTER TABLE assignments ADD COLUMN team_size INTEGER NOT NULL DEFAULT 3 CHECK (team_size >= 1);

-- Owner of a generated repo: a user (individual mode) or a team.
ALTER TABLE assignment_repos ADD COLUMN user_id INTEGER REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE assignment_repos ADD COLUMN team_id INTEGER;

CREATE TABLE teams (
    id            INTEGER PRIMARY KEY,
    assignment_id INTEGER NOT NULL REFERENCES assignments(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE (assignment_id, name)
);

-- A student is in at most one team per assignment.
CREATE TABLE team_members (
    team_id       INTEGER NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    assignment_id INTEGER NOT NULL REFERENCES assignments(id) ON DELETE CASCADE,
    PRIMARY KEY (team_id, user_id),
    UNIQUE (assignment_id, user_id)
);

CREATE TABLE assignment_extensions (
    assignment_id INTEGER NOT NULL REFERENCES assignments(id) ON DELETE CASCADE,
    user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    deadline      TEXT NOT NULL,
    PRIMARY KEY (assignment_id, user_id)
);
