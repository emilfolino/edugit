-- Enrollment before first login, and course invite links.

-- A pre-enrolment names an email that has not logged in yet. LoginUser turns
-- it into a membership once the IdP has verified the address.
CREATE TABLE enrollments (
    course_id  INTEGER NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    email      TEXT NOT NULL,
    role       TEXT NOT NULL CHECK (role IN ('course_admin', 'teacher', 'student')),
    source     TEXT NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'saml', 'import')),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (course_id, email)
);

-- An invite link grants the student role. Only the hash of the link secret
-- is stored; a course has at most one live link.
CREATE TABLE invites (
    id         INTEGER PRIMARY KEY,
    course_id  INTEGER NOT NULL UNIQUE REFERENCES courses(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
