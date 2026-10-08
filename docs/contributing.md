# Contributor guide

## Setup

Go is pinned in `.mise.toml`. Use `mise exec -- <cmd>` or an activated mise shell. You also need `git` for the tests.

```
make build   # static binary, bin/edugit
make test
make lint    # go vet and gofmt check
make run
```

Try the UI locally with `-dev-login` (see the README). Sign in at `/dev/login` as any email; the admin list decides who is admin.

## Ground rules

- Standard library first. Every third-party dependency needs a written justification; today that is the SAML library and the pure-Go SQLite driver.
- Frontend: server-rendered `html/template`, native CSS and small ES modules in `internal/web/static`. No npm, bundler or build step (Monaco is the one vendored exception). The Content-Security-Policy forbids inline scripts and event handlers.
- Follow [`STYLE.md`](../STYLE.md).
- Work comes from [`TODO.md`](../TODO.md). Reference the item number in commits, commit in small logical steps, and when finishing an item tick it in `TODO.md` and update the README (and `CLAUDE.md` if the architecture changed) in the same commit.

## Layout

See the repository layout in the README and the architecture notes in `CLAUDE.md`. In short: `internal/gitserver` (git over HTTP, repo plumbing), `internal/hooks` (push hooks), `internal/auth` (SAML, sessions, tokens), `internal/authz` (the only permission logic), `internal/store` (SQLite, migrations), `internal/web` (handlers, templates), `internal/sites`, `internal/ci`, and `cmd/edugit` (wiring).

## Common tasks

- **Schema change:** add `internal/store/migrations/NNNN_name.sql`. Never edit an applied migration.
- **New page:** handler in `internal/web`, route in the matching `route*` function, template in `internal/web/templates`. Load the course or repository with `loadCourse` / `loadPullRepo` so authorization is course-scoped, and call `requirePost` on every state-changing handler (CSRF).
- **New permission:** add an action to `internal/authz` and test it there; never check roles ad hoc in handlers.
- **Strings:** add keys to both the English and Swedish catalogs in `internal/i18n`; a test enforces parity. Some newer templates still use English literals and can be translated.
- **Shelling out to git:** use the helpers in `internal/gitserver` (explicit args, scrubbed environment) and validate every user-supplied name first.

## Testing

Unit tests next to the code, real `git` in temp dirs for git behaviour, an in-process mock IdP for SAML, a fake runtime script for CI, and `cmd/edugit/e2e_test.go` for the full teacher and student flow. Extend the end-to-end test when you add a user-facing flow.
