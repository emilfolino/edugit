-- Set when the deadline lock archived a generated repo; SyncLocks reopens it
-- if an extension moves the deadline past now.
ALTER TABLE assignment_repos ADD COLUMN locked_at TEXT;
