# edugit

A self-hosted Git platform built for software engineering education.

edugit lets teachers run courses where students work the way professionals do: branch, open a pull request, get a review, merge. It is intended as a replacement for GitHub Campus/Enterprise, designed around courses, assignments and teaching from the start rather than adapted to them.

> **Status: early planning.** There is no runnable code yet. The design decisions are settled and the work is tracked in [`TODO.md`](TODO.md).

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

Currently only documentation:

- [`README.md`](README.md): this file
- [`TODO.md`](TODO.md): numbered implementation backlog and open questions
- [`CLAUDE.md`](CLAUDE.md): guidance for AI coding agents working in this repo, including architecture decisions

Source will live under `cmd/edugit` and `internal/` once the scaffold exists (TODO #1).

## Getting started

Not available yet. Build, test and run instructions will be added when the scaffold lands.

## Contributing

Pick an item from [`TODO.md`](TODO.md), resolve any open question it depends on, and reference the item number in your commits. Keep dependencies minimal and justify any new one.
