-- How students may commit: git over HTTP ("cli"), the in-browser editor
-- ("editor") or both. Staff are never restricted.
ALTER TABLE courses ADD COLUMN commit_methods TEXT NOT NULL DEFAULT 'both'
    CHECK (commit_methods IN ('both', 'cli', 'editor'));
