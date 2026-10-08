-- Initial schema. Timestamps are RFC 3339 UTC text. IDs are integer rowids
-- unless noted. Roles are course-scoped except users.is_admin.

CREATE TABLE users (
    id           INTEGER PRIMARY KEY,
    saml_subject TEXT NOT NULL UNIQUE,          -- stable IdP identifier
    username     TEXT NOT NULL UNIQUE,
    email        TEXT NOT NULL DEFAULT '',
    display_name TEXT NOT NULL DEFAULT '',
    is_admin     INTEGER NOT NULL DEFAULT 0 CHECK (is_admin IN (0, 1)),
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    last_login_at TEXT
);

CREATE TABLE courses (
    id          INTEGER PRIMARY KEY,
    slug        TEXT NOT NULL UNIQUE,
    title       TEXT NOT NULL,
    term        TEXT NOT NULL DEFAULT '',
    archived    INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0, 1)),
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE memberships (
    course_id INTEGER NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role      TEXT NOT NULL CHECK (role IN ('course_admin', 'teacher', 'student')),
    source    TEXT NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'saml', 'import')),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (course_id, user_id)
);
CREATE INDEX memberships_user ON memberships(user_id);

-- Repositories. kind decides the permission model (see authorization).
CREATE TABLE repos (
    id             INTEGER PRIMARY KEY,
    course_id      INTEGER NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    kind           TEXT NOT NULL CHECK (kind IN ('teacher', 'student', 'team')),
    is_template    INTEGER NOT NULL DEFAULT 0 CHECK (is_template IN (0, 1)),
    default_branch TEXT NOT NULL DEFAULT 'main',
    archived       INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0, 1)),
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE (course_id, name)
);

CREATE TABLE assignments (
    id               INTEGER PRIMARY KEY,
    course_id        INTEGER NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    template_repo_id INTEGER NOT NULL REFERENCES repos(id),
    slug             TEXT NOT NULL,
    title            TEXT NOT NULL,
    mode             TEXT NOT NULL DEFAULT 'individual' CHECK (mode IN ('individual', 'team')),
    deadline         TEXT,
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE (course_id, slug)
);

-- Which repo belongs to which assignment and whom (user or team).
CREATE TABLE assignment_repos (
    assignment_id INTEGER NOT NULL REFERENCES assignments(id) ON DELETE CASCADE,
    repo_id       INTEGER NOT NULL UNIQUE REFERENCES repos(id) ON DELETE CASCADE,
    PRIMARY KEY (assignment_id, repo_id)
);

-- Write/read access of individual users to repos (students, team members).
CREATE TABLE repo_members (
    repo_id INTEGER NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (repo_id, user_id)
);

CREATE TABLE branch_protections (
    repo_id            INTEGER NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    pattern            TEXT NOT NULL,
    require_pr         INTEGER NOT NULL DEFAULT 1 CHECK (require_pr IN (0, 1)),
    required_approvals INTEGER NOT NULL DEFAULT 0 CHECK (required_approvals >= 0),
    allow_force_push   INTEGER NOT NULL DEFAULT 0 CHECK (allow_force_push IN (0, 1)),
    PRIMARY KEY (repo_id, pattern)
);

CREATE TABLE pull_requests (
    id          INTEGER PRIMARY KEY,
    repo_id     INTEGER NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    number      INTEGER NOT NULL,                -- per-repo sequence
    author_id   INTEGER NOT NULL REFERENCES users(id),
    title       TEXT NOT NULL,
    body        TEXT NOT NULL DEFAULT '',
    head_branch TEXT NOT NULL,
    base_branch TEXT NOT NULL,
    state       TEXT NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'closed', 'merged')),
    merge_commit TEXT,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE (repo_id, number)
);

CREATE TABLE reviews (
    id         INTEGER PRIMARY KEY,
    pr_id      INTEGER NOT NULL REFERENCES pull_requests(id) ON DELETE CASCADE,
    reviewer_id INTEGER NOT NULL REFERENCES users(id),
    state      TEXT NOT NULL CHECK (state IN ('comment', 'approve', 'request_changes')),
    body       TEXT NOT NULL DEFAULT '',
    commit_sha TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Inline comments carry path/line; general comments leave them NULL.
CREATE TABLE comments (
    id         INTEGER PRIMARY KEY,
    pr_id      INTEGER NOT NULL REFERENCES pull_requests(id) ON DELETE CASCADE,
    review_id  INTEGER REFERENCES reviews(id) ON DELETE CASCADE,
    author_id  INTEGER NOT NULL REFERENCES users(id),
    body       TEXT NOT NULL,
    path       TEXT,
    line       INTEGER,
    commit_sha TEXT NOT NULL DEFAULT '',
    resolved   INTEGER NOT NULL DEFAULT 0 CHECK (resolved IN (0, 1)),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX comments_pr ON comments(pr_id);

CREATE TABLE issues (
    id         INTEGER PRIMARY KEY,
    repo_id    INTEGER NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    number     INTEGER NOT NULL,
    author_id  INTEGER NOT NULL REFERENCES users(id),
    title      TEXT NOT NULL,
    body       TEXT NOT NULL DEFAULT '',
    state      TEXT NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'closed')),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE (repo_id, number)
);

-- Personal access tokens for git over HTTP. Only a hash is stored.
CREATE TABLE tokens (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    expires_at TEXT,
    last_used_at TEXT
);

CREATE TABLE audit_log (
    id         INTEGER PRIMARY KEY,
    at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    actor_id   INTEGER REFERENCES users(id) ON DELETE SET NULL,
    action     TEXT NOT NULL,
    target     TEXT NOT NULL DEFAULT '',
    detail     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX audit_log_at ON audit_log(at);
