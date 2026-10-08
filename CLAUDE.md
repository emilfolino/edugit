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

Config: flags or env (`-addr`/`EDUGIT_ADDR`, `-data-dir`/`EDUGIT_DATA_DIR`, `-log-level`/`EDUGIT_LOG_LEVEL`); flags win.

## Store notes

Migrations live in `internal/store/migrations/NNNN_name.sql`, are embedded, forward-only, and each runs in a transaction; add new files, never edit applied ones. Pragmas (foreign keys, WAL, busy timeout) are set per connection in the DSN. Tests use `:memory:` (single connection) or `t.TempDir()`.

## Architecture (planned; `config`, `web` and `store` exist so far)

- `cmd/edugit` entrypoint; `internal/` packages for: git smart-HTTP handlers + hooks, SAML/session/token auth, authorization (role + course scope), course/assignment/repo-template logic, PR/review engine (merge via git plumbing, not a working tree), static-site publisher/CI runner, SQLite store with embedded migrations, web UI (templates + static assets).
- Git push events (pre-/post-receive hooks calling back into the binary) are the central integration point: they drive branch protection, PR updates, and site publishing.
- Repo-level permission is derived from course role + repo type (teacher repo vs student repo vs team repo); never from per-repo ad hoc ACLs alone.

## Design direction

Visual design was derived from a private random seed (intentionally not recorded). Resulting direction: modular 8-column grid with an asymmetric, rhythmic block layout; warm paper-toned background with deep ink text and a single saturated vermilion accent; monospace-forward typography using system font stacks only (`ui-monospace` for code/data/headings accents, `system-ui` for prose); generous whitespace; crisp 1px rules instead of shadows; light and dark themes via `prefers-color-scheme`. It should look polished, not like a GitHub clone. Never display the seed in the UI.
