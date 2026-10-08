# Teacher guide

Teachers and course admins work inside a course. Course admins can also change settings and roles. All teacher actions are limited to their own courses.

## Set up a course

1. **Enrol people.** On the course page, paste email addresses (one per line or comma separated, optionally with a role) or create an invite link students can open after signing in. Staff roles need a staff-domain address and a course admin.
2. **Create repositories.** Teacher repositories hold material and starter code. Mark a repository as a **template** to let students read it and to use it for assignments. Non-template teacher repositories stay hidden from students.
3. **Push content.** Create a personal access token at `/account/tokens` and use it as the password with Git over HTTPS: `git clone https://<host>/git/<course>/<repo>.git`.
4. **Choose how students commit.** Course settings: CLI only, editor only (browser editor), or both. This is enforced on the server.

## Assignments

Create an assignment from a template repository: individual or team, the history of the generated repository (`fresh` squashes the template to one commit, `copy` keeps its history) and an optional deadline in UTC. Students accept it and receive their own repository named `<assignment>-<username>` (or `<assignment>-<team>`). You get access to all of them.

- Each student repository protects `main` (pull request required, no force push) and has a `feedback` branch. Open the **feedback pull request** to review work in the usual PR interface.
- At the deadline repositories lock (read-only). Extensions for single students reopen theirs; in team mode the latest member extension applies.
- **Reset** replaces a student's repository from the template again if it is broken.
- Required approvals before merging can be set per protected branch under protection settings, which makes peer review possible: students can be requested as reviewers on each other's pull requests without gaining wider access.

## Review and issues

Pull requests support inline and general comments, suggestions, approve and request-changes reviews pinned to a commit (a new push invalidates an approval), resolvable threads, and merge/squash/rebase. Each repository has an issue tracker with labels, assignees and milestones; `Fixes #3` in a merged pull request closes the issue.

## Grading

Open an assignment's grading page. Define a rubric (criteria with maximum points), then score each student's repository and write feedback. Students see their own grade on the assignment page. Download grades as CSV from the same page; team grades are expanded to each member. Cells starting with a formula character are neutralised for spreadsheets.

## Course site

Pick a teacher repository, a branch and optionally a directory on the course page. Every push to that branch republishes it at `/sites/<course>/`, visible to everyone in the course. Only static files are served, sandboxed.

## Automatic checks (CI)

If the administrator enabled it, add `.edugit/ci.json` to a repository to run jobs on each branch push in a container without network access. Results appear under Checks and on pull requests. Checks currently inform; they do not block merging.

## Exporting

Course admins can download `/courses/<course>/export.zip` with the roster, the assignments and rubrics, grades per assignment and git bundles of the teacher repositories. Add `?repos=all` to include student repositories.
