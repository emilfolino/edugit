# edugit

A self-hosted Git platform built for software engineering education.

edugit lets teachers run courses where students work the way professionals do: branch, open a pull request, get a review, merge. It is intended as a replacement for GitHub Campus/Enterprise, designed around courses, assignments and teaching from the start rather than adapted to them.

> **Status: early development.** The project scaffold runs (config, HTTP server, embedded UI shell, health endpoint) the SQLite metadata store with its initial schema is in place, and the Git smart-HTTP layer (clone, fetch, push, repo lifecycle, GC) and the git hooks bridge (branch protection, push events) are mounted and served at `/git/<course>/<repo>.git`. SAML sign-in (Microsoft Entra ID) works end to end: cookie sessions, CSRF protection and personal access tokens (`/account/tokens`, used as the Git HTTP password) are in place. Course management works: admins create courses, staff enrol students by email list/CSV or invite link, and course admins assign roles (`/courses`). The home page groups your courses into Teaching and Studying. The UI is available in English and Swedish (per-user switch in the nav, cookie `edugit_lang`, English default); strings live in `internal/i18n`, and only the home and shell templates are translated so far. Security-relevant actions are recorded in an audit log that admins can browse at `/admin/audit`. Teachers create repositories on the course page and mark them as templates (students see only templates). Teachers define assignments from template repositories (individual or team, with deadlines and extensions); students accept them to get their own generated repository, with `main` protected, a `feedback` branch, a lock at the deadline and a staff reset. Pull requests work: open from a branch, unified/split diff, commit list, conflict detection, merge/squash/rebase, and staff open the feedback PR; pushes update open PRs. Code review works: general and inline comments with suggestions, approve/request-changes reviews, resolvable comments, reviewer requests (including peer review among students) and required approvals before merging a protected branch. A read-only repository browser (tree, highlighted file view, blame, history, commit diffs, branch switch) is linked from the course page. Issues work per repository (`/courses/<course>/repos/<repo>/issues`): open, comment, close/reopen, labels, assignees (enrolled members), milestones with due dates, and `Fixes #n` in a merged pull request closes the issue; the board is not built. Course admins choose how students commit (CLI only, editor only or both), enforced server-side. The in-browser editor (Monaco, vendored as static files) edits or creates files and commits with git plumbing as the signed-in user, obeying branch protection (use a new branch for protected ones) and firing the same push events as a CLI push. Static course sites work: staff pick a teacher repo, branch and optional directory on the course page, and every push to that branch republishes it atomically at `/sites/<course>/` for anyone who can view the course (builds and per-branch previews wait for the CI runner). CI works when started with `-ci-runtime podman`: a repo declares jobs in `.edugit/ci.json`, each branch push queues them, and they run in rootless containers without network (`-ci-network` lets jobs that ask for it opt in); results show under Checks and on the pull request page, and a branch rule can require them to pass before merging. Grading works: staff define a rubric per assignment, score each generated repo against it with written feedback, students see their own grade on the assignment page, and grades export as CSV. Staff can download a course export (`/courses/<course>/export.zip`: manifest with assignments and rubrics, roster, grades, and git bundles of the teacher repos, or of all repos with `?repos=all`). Small vanilla ES modules add progressive enhancement (auto-submit selects, confirm dialogs). The rest is not implemented yet. Design decisions are settled and the work is tracked in [`TODO.md`](TODO.md).

## Goals

- **Git at the bottom.** A real Git server over HTTP, using the system `git` and bare repositories on disk.
- **Few dependencies.** Go standard library first, server-rendered HTML, native CSS and vanilla JavaScript. No npm, no bundler, no build step (the one exception: prebuilt Monaco files are vendored under `internal/web/static/monaco`, adding about 25 MB to the binary). The whole deployment is one binary plus a data directory.
- **Built for teaching.** Courses, multiple teacher repositories (material, starter code), assignments that generate student repositories from templates, and shared team repositories.
- **Realistic workflow.** Students use GitHub Flow: protected `main`, pull requests, inline code review, issues.
- **Automated course sites.** Material in a teacher repository is published as a static website on push.
- **Institutional login.** SAML SSO only, with course-scoped roles.

## Roles

| Role | Scope | Purpose |
|------|-------|---------|
| Admin | Platform-wide | Operates the platform |
| Course admin | Per course | Manages a course, its staff and enrollment |
| Teacher | Per course | Maintains material, assignments and reviews student work |
| Student | Per course | Works on assignments through pull requests |

Roles live in edugit: a global admin creates a course and names its course admin, who assigns teachers; staff enrol students by email (bound on first login), CSV import or invite link. Email domain is only an eligibility hint (`@student` addresses never hold staff roles).

## Planned stack

| Layer | Choice |
|-------|--------|
| Backend | Go, standard library first |
| Git | System `git` over smart-HTTP, bare repos on disk |
| Metadata | SQLite |
| Frontend | `html/template`, native CSS, vanilla ES modules, embedded with `go:embed` |
| Auth | SAML SSO; personal access tokens for Git over HTTP |
| Deployment | systemd service behind a reverse proxy; optional container image |

Student code execution (CI and autograding) is a separate, sandboxed component and is not part of the server process.

## Repository layout

- `cmd/edugit`: entrypoint (wiring only)
- `internal/config`: flag/env configuration and logger
- `internal/store`: SQLite store (pure-Go driver) with embedded forward-only migrations
- `internal/gitserver`: bare-repo management and Git smart-HTTP handler (shells out to `git`)
- `internal/hooks`: pre-/post-receive hooks calling back into the server (branch protection, push events)
- `internal/auth`: SAML 2.0 service provider (crewjam/saml), sessions, CSRF, personal access tokens
- `internal/authz`: central course-scoped `Can` check and the git authorizer
- `internal/sites`: static course-site publisher (export, atomic swap, serving) and its push sink
- `internal/web`: HTTP handlers, templates and static assets (embedded)
- [`TODO.md`](TODO.md): numbered implementation backlog and open questions
- [`STYLE.md`](STYLE.md): code style, based on the official Go guidance
- [`CLAUDE.md`](CLAUDE.md): guidance for AI coding agents working in this repo, including architecture decisions
- [`LICENSE`](LICENSE): MIT license

## Getting started

Go is pinned in `.mise.toml` (Go 1.27); install with [mise](https://mise.jdx.dev/) or any Go 1.27 toolchain.

```
make build   # static binary at bin/edugit
make test
make run     # listens on :8080
```

To try the UI without an identity provider:

```
mise exec -- go run ./cmd/edugit -dev-login -addr 127.0.0.1:8080 -admin-emails you@bth.se \
  -staff-domain bth.se -student-domain student.bth.se
```

then open <http://127.0.0.1:8080/dev/login> and sign in as the admin email.

Configuration via flags or environment (flags win):

| Flag | Env | Default |
|------|-----|---------|
| `-addr` | `EDUGIT_ADDR` | `:8080` |
| `-data-dir` | `EDUGIT_DATA_DIR` | `./data` |
| `-log-level` | `EDUGIT_LOG_LEVEL` | `info` |
| `-public-url` | `EDUGIT_PUBLIC_URL` | empty (SAML disabled) |
| `-saml-idp-metadata` | `EDUGIT_SAML_IDP_METADATA` | empty; https URL or file |
| `-staff-domain` | `EDUGIT_STAFF_DOMAIN` | empty, e.g. `bth.se` |
| `-student-domain` | `EDUGIT_STUDENT_DOMAIN` | empty, e.g. `student.bth.se` |
| `-admin-emails` | `EDUGIT_ADMIN_EMAILS` | empty; comma-separated global admins |
| `-ci-runtime` | `EDUGIT_CI_RUNTIME` | off; container command (e.g. `podman`) that runs CI jobs |
| `-ci-network` | | off; lets CI jobs that request it use the network |
| `-ci-workers` | | 2; concurrent CI jobs |
| `-trust-proxy` | `EDUGIT_TRUST_PROXY` | off; set to `true` only behind a proxy that overwrites `X-Forwarded-For` |
| `-dev-login` | | off; passwordless sign-in at `/dev/login`, only with a loopback `-addr` and no public URL |

After signing in, create a personal access token at `/account/tokens`; it is shown once and stored only as a hash. Use it as the HTTP Basic password (the username is ignored): `git clone https://<host>/git/<course>/<repo>.git`. Bare repositories live in `<data-dir>/repos`.

SAML is enabled when `-public-url` and `-saml-idp-metadata` are both set. Register `<public-url>/saml/metadata` (entity ID) and `<public-url>/saml/acs` with the identity provider; the service provider keypair is generated in the data directory on first start. When a domain is set, other email domains are refused. Domains only gate eligibility; they never grant a role.

See [`docs/deployment.md`](docs/deployment.md) for the systemd unit, reverse proxy, CI isolation, backups and upgrades (files in `deploy/`).

## Backup and restore

All state is in the data directory: `edugit.db` (SQLite, WAL mode) and the bare repositories.

- **Database:** take a consistent snapshot while the server runs with `sqlite3 data/edugit.db "VACUUM INTO '/backups/edugit-$(date +%F).db'"`. Do not copy `edugit.db` alone while running; WAL files (`-wal`, `-shm`) would be missed.
- **Repositories:** back up the repos directory with a filesystem snapshot or `rsync`; git repos tolerate this well, but a snapshot is safest.
- **Restore:** stop the server, replace `edugit.db` (and remove stale `-wal`/`-shm` files) and the repos directory from the same backup point, then start. Pending migrations are applied automatically on start.

## Testing

`make test` runs everything; no network, containers or external services are needed, only `git`. Unit tests sit next to the code. Git behaviour is tested against real `git` in temp dirs (`internal/gitserver`, `internal/hooks`), the SAML flow against an in-process mock IdP (`internal/auth`), and CI against a fake container runtime script. `cmd/edugit/e2e_test.go` starts the whole application and plays a teacher and a student over HTTP and real `git`: course, template push, published site, assignment, protected `main`, branch push, pull request, merge, grading CSV and export. It signs in with dev-login; the SAML sign-in itself is covered in `internal/auth`. `routes_test.go` checks the read routes (browser, site, CI, grading, export) against teacher, owner, classmate, outsider and anonymous visitors. Running the CI jobs under real podman is not part of the suite.

## Security notes

Reviewed in TODO #22. Git subprocesses never go through a shell, use a scrubbed environment and only receive names validated by strict regexes (`Repos.Path`, branch and path validators). State-changing routes require a session-derived CSRF token plus an `Origin`/`Sec-Fetch-Site` check. Every response carries a same-origin Content-Security-Policy (no inline scripts), `X-Frame-Options: DENY`, `nosniff` and a same-origin referrer policy; published course sites replace the CSP with a sandbox. Highlighted code and diffs are HTML-escaped. Bad personal access tokens are throttled per client address (20 failures per 10 minutes, then that address is refused). Behind a reverse proxy pass `-trust-proxy` so the address comes from `X-Forwarded-For`; never set it when the server is directly reachable. CI jobs run in rootless containers without network (see the CI section). Report vulnerabilities privately to the maintainers.

## Documentation

- [Student guide](docs/student-guide.md), including a git and GitHub Flow primer
- [Teacher guide](docs/teacher-guide.md)
- [Administrator guide](docs/admin-guide.md) and [deployment](docs/deployment.md)
- [Contributor guide](docs/contributing.md)

## Contributing

Pick an item from [`TODO.md`](TODO.md), resolve any open question it depends on, and reference the item number in your commits. Keep dependencies minimal and justify any new one.

## License

[MIT](LICENSE) © 2026 Emil Folino
