# edugit

A self-hosted Git platform built for software engineering education.

edugit lets teachers run courses where students work the way professionals do: branch, open a pull request, get a review, merge. It is intended as a replacement for GitHub Campus/Enterprise, designed around courses, assignments and teaching from the start rather than adapted to them.

> **Status: early development.** The project scaffold runs (config, HTTP server, embedded UI shell, health endpoint) and the SQLite metadata store with its initial schema is in place. Git hosting, auth and courses are not implemented yet. Design decisions are settled and the work is tracked in [`TODO.md`](TODO.md).

## Goals

- **Git at the bottom.** A real Git server over HTTP, using the system `git` and bare repositories on disk.
- **Few dependencies.** Go standard library first, server-rendered HTML, native CSS and vanilla JavaScript. No npm, no bundler, no build step. The whole deployment is one binary plus a data directory.
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

Roles are assigned from SAML attributes and/or manual enrollment.

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

Configuration via flags or environment (flags win):

| Flag | Env | Default |
|------|-----|---------|
| `-addr` | `EDUGIT_ADDR` | `:8080` |
| `-data-dir` | `EDUGIT_DATA_DIR` | `./data` |
| `-log-level` | `EDUGIT_LOG_LEVEL` | `info` |

## Backup and restore

All state is in the data directory: `edugit.db` (SQLite, WAL mode) and the bare repositories.

- **Database:** take a consistent snapshot while the server runs with `sqlite3 data/edugit.db "VACUUM INTO '/backups/edugit-$(date +%F).db'"`. Do not copy `edugit.db` alone while running; WAL files (`-wal`, `-shm`) would be missed.
- **Repositories:** back up the repos directory with a filesystem snapshot or `rsync`; git repos tolerate this well, but a snapshot is safest.
- **Restore:** stop the server, replace `edugit.db` (and remove stale `-wal`/`-shm` files) and the repos directory from the same backup point, then start. Pending migrations are applied automatically on start.

## Contributing

Pick an item from [`TODO.md`](TODO.md), resolve any open question it depends on, and reference the item number in your commits. Keep dependencies minimal and justify any new one.

## License

[MIT](LICENSE) © 2026 Emil Folino
