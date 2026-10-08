-- One published static site per course, built from a teacher repo.
CREATE TABLE course_sites (
    course_id INTEGER PRIMARY KEY REFERENCES courses(id) ON DELETE CASCADE,
    repo_id   INTEGER NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    branch    TEXT NOT NULL,
    dir       TEXT NOT NULL DEFAULT ''
);
