# Code Style

edugit follows the official Go guidance. Where sources disagree, earlier entries win:

1. [Effective Go](https://go.dev/doc/effective_go)
2. [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments)
3. [Google Go Style Guide](https://google.github.io/styleguide/go/) (decisions and best practices)
4. [Go Proverbs](https://go-proverbs.github.io/) as guiding spirit

This file only records what is specific to this repo or worth emphasizing. It does not restate the guides.

## Tooling (must pass before commit)

- `gofmt` (or `go fmt ./...`): formatting is not negotiable; no manual alignment fights.
- `go vet ./...`
- `go test ./...` (add `-race` for anything touching concurrency)
- Optional but encouraged: `staticcheck ./...`. It is a dev tool, not a build dependency.

## Dependencies

- Standard library first. A new third-party module needs a written justification in the commit message or PR (what it does, why stdlib is insufficient, maintenance status, transitive dependencies).
- No cgo unless unavoidable (we want static, cross-compilable builds).
- Frontend: no npm, no bundler, no web fonts (see CLAUDE.md).

## Naming

- Packages: short, lowercase, single word, no underscores or `mixedCaps`; no `util`, `common`, `helpers`. The package name is part of the call site (`store.Open`, not `store.NewStore`).
- Exported identifiers use `MixedCaps`; unexported use `mixedCaps`. Initialisms keep case: `ID`, `URL`, `HTTP`, `SAML`, `SQL`, `userID`.
- No `Get` prefix on getters (`user.Name()`, not `user.GetName()`).
- Single-method interfaces are named with an `-er` suffix. Define interfaces in the package that *consumes* them, and keep them small.
- Receiver names are one or two letters, consistent across a type, never `this`/`self`.
- Test files: `foo_test.go`; test functions `TestThing_condition`.

## Structure

- `cmd/edugit` contains only wiring (flags/config, construct dependencies, start server). Logic lives in `internal/`.
- Prefer `internal/` for everything; export nothing until needed.
- Avoid package-level mutable state and `init()`. Pass dependencies explicitly (constructors taking a struct or small interfaces).
- Accept interfaces, return concrete types.
- Keep packages acyclic and focused; split by domain (`course`, `pr`, `gitserver`, `auth`), not by layer (`models`, `controllers`).

## Errors

- Return errors; do not panic for expected failures. Panic only for programmer errors at startup.
- Wrap with context using `fmt.Errorf("open repo %q: %w", name, err)`. Messages are lowercase with no trailing punctuation.
- Use `errors.Is` / `errors.As`; define sentinel errors (`ErrNotFound`) or typed errors for conditions callers branch on.
- Handle an error once: either return it or log it, not both.
- Do not ignore errors with `_` unless commented why it is safe.
- Keep the happy path unindented: return early on error.

## Context and concurrency

- `context.Context` is the first parameter, named `ctx`, never stored in structs. Pass `r.Context()` from HTTP handlers into git subprocesses (`exec.CommandContext`) and DB calls.
- Every goroutine has a clear owner and exit condition. Prefer `errgroup`-style patterns built from stdlib (`sync.WaitGroup`, channels) over hand-rolled leaks.
- Document whether a type is safe for concurrent use.

## Security-sensitive conventions (project specific)

- Never build shell strings. Run git with `exec.CommandContext(ctx, "git", args...)` and an explicit argument list; place `--` before user-supplied refs/paths and validate them first.
- Resolve repo paths through a single function that rejects traversal and anything outside the data root.
- Use `html/template` (never `text/template`) for HTML; never mark user input as safe.
- Authorization goes through the central `can(...)` check, not ad hoc role comparisons in handlers.
- Compare secrets/tokens with `crypto/subtle`; store only hashes of tokens.
- Use `crypto/rand`, never `math/rand`, for anything security-related.

## Logging

- Use `log/slog` with structured key/value attributes. No `fmt.Println` or `log.Printf` in non-test code.
- Never log tokens, SAML assertions, session IDs or passwords.

## Comments and docs

- Every exported identifier has a doc comment beginning with its name, in full sentences.
- Every package has a package comment (`// Package store ...`).
- Comment *why*, not *what*. No commented-out code.
- TODO comments reference a backlog number: `// TODO(#16): support rebase merges`.

## Testing

- Table-driven tests with `t.Run` subtests; use `t.Helper()` in helpers and `t.TempDir()` for filesystem state.
- Prefer real `git` against temporary repos over mocking git.
- Compare with the stdlib (`reflect.DeepEqual`, `slices.Equal`, `maps.Equal`); `go-cmp` is third-party and needs justification. Failure messages read `got X, want Y`.
- Authorization logic requires exhaustive table tests across all roles.

## SQL

- Use `database/sql` with parameterized queries only; never concatenate values into SQL.
- Migrations are ordered, embedded, forward-only files; each is idempotent in effect and tested.

## Frontend

- HTML: semantic elements, accessible labels, works without JavaScript where feasible (progressive enhancement).
- CSS: custom properties for the design tokens, no preprocessors, mobile-first, `prefers-color-scheme` for themes.
- JavaScript: ES modules, `const`/`let`, no globals, no transpilation; one small module per behavior. Format consistently (2-space indent, semicolons, double quotes).

## Commits

- Small, logical commits with imperative subject lines ("Add PR merge engine"). Reference TODO numbers in the body.
- Commits that complete a TODO item also tick it in `TODO.md` and update `README.md` (see CLAUDE.md).
