# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Status

Early stage. The scaffold (TODO #1) exists: config, HTTP server with embedded templates/static, health endpoint. `TODO.md` is the numbered implementation backlog; keep it current and reference items by number (e.g. "TODO #7"). 

## Workflow rules

- Commit often, in small logical steps.
- Follow `STYLE.md` for Go, SQL, and frontend code style (based on Effective Go, Go Code Review Comments, and the Google Go Style Guide).
- Only push to `origin` when the user asks.
- On every meaningful completion of a TODO item, update `README.md` (status, stack, layout, getting-started/commands) and tick the item in `TODO.md` in the same commit.

## What this is

**edugit**: a self-hosted Git platform designed for software engineering education, replacing GitHub Campus/Enterprise. Students work in a realistic GitHub Flow (branch, PR, review, merge) on repos generated from teacher-provided material.

## Decisions already made (do not re-litigate)

- **Git layer:** our own app serves git over smart-HTTP by shelling out to the system `git` binary against bare repos on disk. No Gitea/Forgejo embedding.
- **Backend:** Go, standard library first. Every third-party dependency needs a justification; the explicit goal is avoiding dependency problems later. Go is pinned in `.mise.toml` (run via `mise exec -- <cmd>` or an activated mise shell).
- **Frontend:** server-rendered `html/template` + native CSS + small vanilla ES modules. **No npm, no bundler, no build step, no web fonts.** Assets are embedded in the binary with `go:embed`.
- **Storage:** SQLite (`modernc.org/sqlite`, pure Go, chosen over cgo `mattn/go-sqlite3` to keep static builds; keep SQL driver-agnostic so it can be swapped) for metadata (users, courses, PRs, reviews, issues); bare git repos on the filesystem. Single binary + data directory = the whole deployment.
- **Auth:** SAML SSO only (no local passwords). Git over HTTP uses per-user personal access tokens issued after SAML login.
- **Roles:** `admin` is global. `course admin`, `teacher`, `student` are scoped per course, assigned from SAML attributes and/or manual enrollment. Authorization checks must always be course-scoped except for admin.
- **Course model:** teacher repos (multiple per course: material, starter code, etc.) act as templates; assignments generate per-student repos from a template, and also support a shared team repo per assignment. Teachers get access to student repos.
- **Pull requests/reviews/issues:** implemented natively in this app (inline review comments, branch protection, required reviews), with hooks for educational features (rubrics, grading).
- **Static course sites:** published automatically from teacher repos on push, served per course. The user chose a full CI runner as the long-term mechanism; student autograding is a later roadmap item, but design the runner so it can serve both (see TODO).

## Commands

```
make build                       # CGO_ENABLED=0 static binary -> bin/edugit
make test                        # go test ./...
make lint                        # go vet + fail on unformatted files
make run                         # go run ./cmd/edugit
go test ./internal/web -run TestHandler   # single test
```

Config: flags or env (`-addr`/`EDUGIT_ADDR`, `-data-dir`/`EDUGIT_DATA_DIR`, `-log-level`/`EDUGIT_LOG_LEVEL`, plus the SAML settings listed in README.md); flags win.

## Store notes

Migrations live in `internal/store/migrations/NNNN_name.sql`, are embedded, forward-only, and each runs in a transaction; add new files, never edit applied ones. Pragmas (foreign keys, WAL, busy timeout) are set per connection in the DSN. Tests use `:memory:` (single connection) or `t.TempDir()`.

## Architecture (planned; `config`, `web`, `store`, `gitserver`, `hooks` and `auth` exist so far)

- `internal/gitserver`: bare repos at `<root>/<course>/<name>.git`; `Repos.Path` is the only name-to-path resolver (strict name regex). `Handler` serves smart-HTTP at `/git/{course}/{repo}.git/...` via `git upload-pack|receive-pack --stateless-rpc`, with authorization injected as an `Authorizer` func (`Authenticate` is `sessions.GitUser`; mounted in `cmd/edugit/git.go` together with the hooks bridge, never without it). Git subprocesses use a scrubbed env (`gitEnv`). Tests use real `git` through `httptest`.
- `internal/hooks`: pre-/post-receive bridge. Repos get tiny shell hooks that exec `edugit hook <name>` (`hooks.Run`), which computes force-push status inside git's quarantine and calls the server over a 0600 unix socket (`Bridge`, bearer secret from a per-process random). Socket, secret and pusher identity reach hooks via env set by `gitserver.Handler.HookEnv`. `Policy` (branch protection in `Protection`, fed by a `RuleSource`) gates pre-receive and fails closed; `Sink` receives post-receive pushes (PR engine #16, publisher #19). The merge engine updates refs server-side and so bypasses hooks. Mounted by `cmd/edugit/git.go`, whose `ruleSource` adapts `store.BranchRules`; the socket lives in a private temp dir. Default protection rules for student repos come with #12. Tests re-exec the test binary as `edugit` via `TestMain`.
- `internal/auth`: SAML SP on crewjam/saml (the one justified non-stdlib auth dependency). SP-initiated only: `/saml/login` stores the request ID (single use, 10 min, in memory) and sets a binding cookie; `/saml/acs` requires both before `ParseResponse`. Users are keyed on the Entra `objectidentifier` claim; `Domains.Kind` is an exact-match eligibility hint. `OnLogin` is injected from `cmd/edugit` and calls `Store.LoginUser` (JIT provisioning; config admin emails become global admins, never demoted). Sessions (#6): `Sessions` stores only sha256 hashes of 32-byte random secrets (cookie `edugit_session`, tokens prefixed `edg_`); a fresh session per login; the CSRF token is derived from the session cookie (no storage) and `CheckCSRF` also checks `Origin`/`Sec-Fetch-Site`; `GitUser` reads a token from HTTP Basic. `web.Options` enables the account routes only when SAML is configured. Tests use crewjam's `IdentityProvider` in-process as a mock IdP (it needs `Logger` set and an RSA-SHA256 `SignatureMethod`).
- `internal/i18n`: en/sv catalogs (`T(key)` falls back to English, then the key); `web.page.L` comes from `i18n.FromRequest` (cookie, Accept-Language, English), `POST /lang` sets the cookie. Templates use `{{.L.T "key"}}`; add keys to both catalogs (a test enforces parity).
- `internal/authz`: `Can(Principal, Action, Resource)` is pure and the only permission logic; `Principal.Roles` is keyed by course ID so roles never cross courses; global admin bypasses except `RepoWrite` on archived repos. `Authorizer.Git` adapts it to `gitserver.Authorizer` (401 anonymous, 404 for unreadable repos, 403 read-only). Students read teacher repos only if `is_template`.
- `internal/web/assignments.go` + `store/assignments.go`: assignments generate repos via `gitserver.Repos.Generate` (git plumbing from the template HEAD; `fresh` squashes to one commit). Handlers create the DB row first, then the disk repo, and roll the row back (`DeleteRepo`/`DeleteTeam`) if disk fails. Naming: `<assignment>-<username>` / `<assignment>-<team>`. Deadlines are UTC; an extension never shortens the deadline and in team mode the latest member extension applies. Students accept via `AssignmentAccept` (student role only), staff manage via `AssignmentManage`.
- Student repo lifecycle (#12): `store.insertAssignmentRepo` seeds `branch_protections` for `ProtectedBranches` (`main`, `feedback`); `Store.SyncLocks` sets `archived` + `locked_at` past the deadline (extensions counted) and reopens only repos it locked itself, driven by a ticker in `cmd/edugit` and after extensions; `Repos.Replace` swaps a repo aside for reset and restores it on failure.
- `internal/web/courses.go`: course/roster/invite handlers. Every handler goes through `loadCourse` (404 if not viewable, 403 if not allowed); enrolling non-students needs `CourseManage` and `Domains.AllowsStaffRole`. `-dev-login` (loopback only) gives passwordless local sign-in.
- `internal/web/pulls.go`, `diff.go`: PR handlers (every one goes through `loadPullRepo`: 404 if the repo is unreadable, 403 if the action is denied) and the unified-diff parser feeding the templates. `web.PullGit` is satisfied by `gitserver.Repos`; the merge engine bypasses hooks, so merge rules (approvals, #17) must be enforced in `pullMerge`. `cmd/edugit/pulls.go` is the `hooks.Sink` that touches/closes open PRs on branch pushes.
- `internal/web/browse.go` + `gitserver/browse.go` + `web/highlight.go`: read-only repo browser (#14). `ref` is a `?ref=` query param, the path is the URL remainder; every handler goes through `loadPullRepo(RepoRead)`; `web.BrowseGit` is satisfied by `gitserver.Repos`; highlighting is a small lexical per-extension highlighter that escapes everything.
- `internal/web/reviews.go` + `store/reviews.go`: code review (#17). Reviews pin the head SHA; an approval counts only for that SHA, the author's own reviews never count, and each reviewer's latest decisive review wins. `pullMerge` calls `approvalGate` (fails closed) against `RequiredApprovals` for the base branch (staff set it via `/protection`). A requested reviewer gets `Resource.IsReviewer` (read + `RepoReview` on that open PR only), which is how peer review works without widening repo access. Inline comments are listed in the discussion with `path:line`, not anchored in the diff.
- `internal/web/issues.go` + `store/issues.go`: issue tracker (#18). Every handler goes through `loadPullRepo`; `authz.RepoIssue` (read-like, denied on archived repos) opens/comments, the author or `RepoWrite` closes/reopens, labels/assignees/milestones need `RepoWrite`. Numbers are per repo; assignees must be enrolled. `pullMerge` calls `closeReferencedIssues` (`Fixes #n` in PR title/body). Templates are English literals for now.
- `cmd/edugit` entrypoint; `internal/` packages for: git smart-HTTP handlers + hooks, SAML/session/token auth, authorization (role + course scope), course/assignment/repo-template logic, PR/review engine (merge via git plumbing, not a working tree), static-site publisher/CI runner, SQLite store with embedded migrations, web UI (templates + static assets).
- Git push events (pre-/post-receive hooks calling back into the binary) are the central integration point: they drive branch protection, PR updates, and site publishing.
- Repo-level permission is derived from course role + repo type (teacher repo vs student repo vs team repo); never from per-repo ad hoc ACLs alone.

## Design direction

Visual design was derived from a private random seed (intentionally not recorded). Resulting direction: modular 8-column grid with an asymmetric, rhythmic block layout; warm paper-toned background with deep ink text and a single saturated vermilion accent; monospace-forward typography using system font stacks only (`ui-monospace` for code/data/headings accents, `system-ui` for prose); generous whitespace; crisp 1px rules instead of shadows; light and dark themes via `prefers-color-scheme`. It should look polished, not like a GitHub clone. Never display the seed in the UI.
