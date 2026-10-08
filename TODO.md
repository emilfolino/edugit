# TODO

Numbered backlog for edugit. Reference items by number. Mark done with `[x]`.

## Open questions (resolve before the related item)

- Q1. ~~Which SAML IdP?~~ **Resolved:** Microsoft Entra ID, authentication only. Users are keyed on the Entra `objectidentifier` claim. Email domain is an eligibility hint, never a grant: `@bth.se` = staff (may hold teacher/course_admin), `@student.bth.se` = student (never staff roles); match the exact domain after `@`, not a suffix. All roles live in edugit (enrollment by email before first login, bound to the object ID on first login; global admin bootstrapped from config). SAML library: crewjam/saml + goxmldsig (chosen). Still open: the production federation metadata URL and public hostname (set via `EDUGIT_SAML_IDP_METADATA` / `EDUGIT_PUBLIC_URL`; develop against the mock IdP or a personal Entra tenant). Single logout: skipped. Decided: only global admins create courses and name the course admin; staff enroll students directly by roster (invite links also allowed); first global admin is `efo@bth.se` (config bootstrap, matched on verified email at first login). (#5, #9)
- Q2. Is SSH git access required, or is HTTPS + tokens enough? (affects #3, #6)
- Q3. Expected scale (users, repos, repo sizes) and hosting/backup environment? (affects #2, #25)
- Q4. Runner isolation: containers (podman/docker), bubblewrap, or VMs? Which languages must student code run in? (blocks #19, #20)
- Q5. Migration: do existing GitHub Classroom repos/courses need importing? (#24)
- Q6. Language of the UI (English, Norwegian, both)? (#13)

## Foundation

1. [x] **Project scaffold.** *(done: Go 1.27 via mise, `cmd/edugit`, `internal/config`, `internal/web`, Makefile, embedded assets, graceful shutdown.)* Install Go; create module, `cmd/edugit`, `internal/` layout, config loading (flags/env/file, stdlib only), structured logging (`log/slog`), graceful shutdown, `go:embed` for templates/static. Add Makefile or script for build/test/lint. Update CLAUDE.md with the real commands.
2. [x] **SQLite store + migrations.** *(done: `modernc.org/sqlite`, `internal/store`, `0001_initial.sql`, `Store.Backup` via VACUUM INTO, backup/restore documented in README.)* Choose a SQLite driver (the one unavoidable dependency decision; prefer pure-Go `modernc.org/sqlite` to keep cgo-free static builds, document the choice). Embedded, ordered SQL migrations; schema for users, courses, memberships(role), repos, assignments, PRs, reviews, comments, issues, tokens, audit log. Backup/restore procedure.
3. [x] **Git smart-HTTP server.** *(done: `internal/gitserver`; handler is authorizer-injected and mounted in #10; size limits via `MaxPushBytes`/`MaxRepoBytes`, `RunGC` scheduler to be started from main.)* Serve clone/fetch/push for bare repos by invoking `git upload-pack` / `git receive-pack` (stateless-rpc) or `git http-backend`. Repo layout on disk, creation/deletion/rename, size limits, concurrency safety, garbage collection schedule.
4. [x] **Git hooks bridge.** *(done: `internal/hooks`; `Protection` policy + `Sink` interface; store-backed `RuleSource` and mounting landed in #10.)* Install pre-receive/update/post-receive hooks that call back into the binary (unix socket or local HTTP with a secret). Enforce branch protection, reject force-push to protected branches, emit push events to PR engine and publisher.

## Auth & authorization

5. [x] **SAML SSO.** *(done: `internal/auth` on crewjam/saml; SP-initiated only, metadata/login/ACS, single-use request IDs plus browser-binding cookie, exact-domain eligibility, JIT user via `Store.LoginUser`, admin bootstrap from `EDUGIT_ADMIN_EMAILS`; tested with an in-process mock IdP. The ACS ends in a 501 placeholder until sessions land in #6; pre-enrolment by email lands with #9.)* SP metadata endpoint, AuthnRequest, ACS with signature/assertion validation, replay protection, clock skew. Single logout skipped (Q1). Just-in-time user provisioning from attributes.
6. [x] **Sessions and personal access tokens.** *(done: `auth.Sessions` with hashed-at-rest cookie sessions and derived CSRF tokens, token UI at `/account/tokens`, `GitUser` Basic-auth helper. SSH not started, per Q2.)* Secure cookie sessions, CSRF protection, token issue/revoke UI, tokens hashed at rest, used as HTTP Basic password for git. Optional SSH key support per Q2.
7. [x] **Roles and authorization.** *(done: `internal/authz` with pure `Can(Principal, Action, Resource)` and exhaustive table tests, store queries for memberships/repos, `Authorizer.Git` for the git handler. Deferred: admin "view as" moves to #8 (needs the audit log); SAML-attribute role mapping dropped per Q1, enrollment UI/import is #9. The git handler is mounted together with the hooks in #10.)* Global `admin`; per-course `course_admin`, `teacher`, `student`. Central `can(user, action, resource)` function; derive repo access from course role and repo type. Role mapping from SAML attributes plus manual enrollment; admin impersonation/"view as" with audit trail. Exhaustive table-driven tests.
8. [x] **Audit log.** *(done: `store.Audit`/`AuditEntries`, admin-only `AuditView` action, `/admin/audit` viewer with action filter and paging; records login/logout, token create/revoke, course create/archive, enrolment, role changes, removals and invites. Never stores secrets. Admin "view as" is not built; add it when needed.)* Record auth events, role changes, permission-sensitive actions; admin viewer.

## Courses & repos

9. [x] **Course management.** *(done: admin-only creation naming the course admin, archive, pre-enrolment by email bound at first login, paste/CSV import, invite link (hashed, one live per course, confirm-to-join), roster with role changes; staff roles need course admin plus an eligible domain. Audit entries come with #8; `-dev-login` added for local use.)* Create/archive courses, semesters/terms, enrollment (SAML-driven, CSV import, invite link), roster view, role assignment by course admin.
10. [x] **Teacher repos and templates.** *(done: teachers/course admins create teacher repos on the course page (bare repo plus metadata, rolled back on failure) and toggle the template flag; students see and clone only template teacher repos. The git smart-HTTP handler and hooks bridge are now mounted in `cmd/edugit` with a store-backed branch-protection `RuleSource`; clone/push verified end to end with real git and a personal access token.)* Multiple teacher repos per course (material, starter code, solutions hidden from students). Mark repos as templates; visibility rules.
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
