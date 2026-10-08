-- Reviewers asked to look at a pull request (teachers, or peers in the same
-- course). A request gives a peer read access to the PR's repository pages.
CREATE TABLE review_requests (
    pr_id        INTEGER NOT NULL REFERENCES pull_requests(id) ON DELETE CASCADE,
    reviewer_id  INTEGER NOT NULL REFERENCES users(id),
    requested_by INTEGER NOT NULL REFERENCES users(id),
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (pr_id, reviewer_id)
);
