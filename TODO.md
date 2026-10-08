# TODO

Numbered backlog for edugit. Reference items by number. Mark done with `[x]`.

## Open questions (resolve before the related item)

- Q1. Which SAML IdP(s) (Feide, Entra ID, Shibboleth, other)? Which attributes carry identity, email, affiliation/role? (blocks #5)
- Q2. Is SSH git access required, or is HTTPS + tokens enough? (affects #3, #6)
- Q3. Expected scale (users, repos, repo sizes) and hosting/backup environment? (affects #2, #25)
- Q4. Runner isolation: containers (podman/docker), bubblewrap, or VMs? Which languages must student code run in? (blocks #19, #20)
- Q5. Migration: do existing GitHub Classroom repos/courses need importing? (#24)
- Q6. Language of the UI (English, Norwegian, both)? (#13)

## Foundation

1. [ ] **Project scaffold.** Install Go; create module, `cmd/edugit`, `internal/` layout, config loading (flags/env/file, stdlib only), structured logging (`log/slog`), graceful shutdown, `go:embed` for templates/static. Add Makefile or script for build/test/lint. Update CLAUDE.md with the real commands.
2. [ ] **SQLite store + migrations.** Choose a SQLite driver (the one unavoidable dependency decision; prefer pure-Go `modernc.org/sqlite` to keep cgo-free static builds, document the choice). Embedded, ordered SQL migrations; schema for users, courses, memberships(role), repos, assignments, PRs, reviews, comments, issues, tokens, audit log. Backup/restore procedure.
3. [ ] **Git smart-HTTP server.** Serve clone/fetch/push for bare repos by invoking `git upload-pack` / `git receive-pack` (stateless-rpc) or `git http-backend`. Repo layout on disk, creation/deletion/rename, size limits, concurrency safety, garbage collection schedule.
4. [ ] **Git hooks bridge.** Install pre-receive/update/post-receive hooks that call back into the binary (unix socket or local HTTP with a secret). Enforce branch protection, reject force-push to protected branches, emit push events to PR engine and publisher.

## Auth & authorization

5. [ ] **SAML SSO.** SP metadata endpoint, AuthnRequest, ACS with signature/assertion validation, replay protection, clock skew, single logout. Prefer stdlib `encoding/xml` + `crypto`; evaluate a vetted lib (e.g. crewjam/saml) only if hand-rolling XML-DSig is too risky; security-review whatever is chosen. Just-in-time user provisioning from attributes.
6. [ ] **Sessions and personal access tokens.** Secure cookie sessions, CSRF protection, token issue/revoke UI, tokens hashed at rest, used as HTTP Basic password for git. Optional SSH key support per Q2.
7. [ ] **Roles and authorization.** Global `admin`; per-course `course_admin`, `teacher`, `student`. Central `can(user, action, resource)` function; derive repo access from course role and repo type. Role mapping from SAML attributes plus manual enrollment; admin impersonation/"view as" with audit trail. Exhaustive table-driven tests.
8. [ ] **Audit log.** Record auth events, role changes, permission-sensitive actions; admin viewer.

## Courses & repos

9. [ ] **Course management.** Create/archive courses, semesters/terms, enrollment (SAML-driven, CSV import, invite link), roster view, role assignment by course admin.
10. [ ] **Teacher repos and templates.** Multiple teacher repos per course (material, starter code, solutions hidden from students). Mark repos as templates; visibility rules.
11. [ ] **Assignments.** Define assignment from a template repo, deadlines, individual vs team mode. Generate per-student repo on accept (fresh history or copy), naming scheme, teacher access, bulk generation, late/extension handling. Team mode: shared repo per group, group creation/joining, membership rules.
12. [ ] **Student repo lifecycle.** Default branch protection (no direct push to `main`, PR required), archive/lock at deadline, teacher feedback branch/PR, repo reset/recreate.

## Web UI

13. [ ] **Design system.** Implement the design direction from CLAUDE.md as native CSS (custom properties, grid, `prefers-color-scheme`), system fonts only, accessible contrast and keyboard nav, i18n scaffolding per Q6.
14. [ ] **Core pages.** Dashboard per role, course page, repo browser (tree, blob view with syntax highlighting without a JS lib, commits, branches, blame, diff), assignment pages, admin console.
15. [ ] **Web interactivity with vanilla ES modules.** Progressive enhancement; no build step.

## Collaboration (GitHub Flow)

16. [ ] **Pull requests.** Create from branch, diff view (unified/split), commit list, status, merge strategies (merge/squash/rebase) done via git plumbing in a temporary worktree or `git merge-tree`; conflict detection; update on push through hooks.
17. [ ] **Code review.** Inline and general comments, review states (comment/approve/request changes), required approvals for protected branches, suggestions, resolve threads, reviewer assignment (including peer review among students).
18. [ ] **Issues and project basics.** Issues with labels/assignees/milestones linked to PRs (`Fixes #n`); simple board optional.

## Publishing & CI

19. [ ] **Static course site publishing.** On push to a configured branch of a teacher repo, publish to a per-course URL (e.g. `course.<host>/` or `/sites/<course>/`), atomic swap of served directory, optional sandboxed build command, build logs, preview per branch/PR, custom path config file in repo.
20. [ ] **CI runner (long-term).** Generic job runner triggered by push events with a repo-defined config file; sandboxed execution (per Q4), resource/time limits, logs, status checks on PRs. Used first for site builds, later for autograding.
21. [ ] **Autograding and grading (roadmap).** Test results as PR checks, rubric-based grading, grade export (CSV/LMS), plagiarism/similarity hooks.

## Operations & quality

22. [ ] **Security review.** Path traversal in repo handling, command injection when shelling out to git, SAML validation, CSRF, XSS in rendered Markdown/diffs, sandbox escapes. Rate limiting.
23. [ ] **Testing strategy.** Unit tests, integration tests using real `git` against temp dirs, end-to-end flow test (SAML mock IdP -> create course -> assignment -> student PR -> merge -> site publish).
24. [ ] **Import/export.** Course export, optional GitHub Classroom import (per Q5).
25. [ ] **Deployment.** Default: plain systemd service (unprivileged user, hardening options) behind a reverse proxy (Caddy/nginx) for TLS, data in `/var/lib/edugit`; Dockerfile is optional with `/data` as a volume. The CI runner (#20) must be isolated from the server process/host. Single-binary packaging, systemd unit, reverse proxy/TLS guidance, backups of SQLite + repos, upgrade/migration procedure, monitoring/health endpoint, Dockerfile optional.
26. [ ] **Documentation.** Admin guide, teacher guide, student guide (including a git/GitHub Flow primer), contributor guide.
