# Administrator guide

Installing, proxying, backing up and upgrading edugit is covered in [deployment.md](deployment.md). This guide covers running it day to day.

## Roles

- **Admin** is global: it sees every course and may create courses. Admins are listed in `EDUGIT_ADMIN_EMAILS`; anyone on that list is made admin at their next sign-in (and never demoted automatically).
- **Course admin, teacher, student** are per course. A role in one course gives nothing in another.

## Sign-in

Only SAML single sign-on is supported (tested against Microsoft Entra ID). Users are created on first sign-in and are identified by the identity provider's object id, so a changed email address keeps the same account. `EDUGIT_STAFF_DOMAIN` and `EDUGIT_STUDENT_DOMAIN` restrict which email domains may sign in and which may be given staff roles. They never grant a role by themselves; roles come from enrolment.

## Creating a course

On the home page, an admin enters a short name (lowercase, digits, hyphens), a title, a term and the email of the first course admin. That person can then manage the course themselves.

## Audit log

`/admin/audit` lists security-relevant actions: logins, course and role changes, token creation and revocation, repository creation, grade and export actions.

## Operations

- Health: `GET /healthz`.
- Logs: structured, to stderr (journal under systemd). Raise detail with `EDUGIT_LOG_LEVEL=debug`.
- Sessions and expired data are purged automatically. Past an assignment's deadline the student repositories lock themselves.
- Rate limiting: bad personal access tokens block the client address for git after 20 failures in 10 minutes. Sign-in endpoints allow 60 requests a minute per address and each session 300 writes a minute; beyond that users get `429 too many requests` for a minute. Restarting the server clears all blocks.
- A student who loses access to git can revoke and recreate tokens at `/account/tokens`.

## Troubleshooting

| Symptom | Check |
|---|---|
| Login redirects fail | `EDUGIT_PUBLIC_URL` must match the URL users open and the IdP's registered ACS URL. |
| "not eligible" at enrolment | The email's domain does not match the staff or student domain. |
| Pushes to `main` rejected | Intended: student `main` is protected, open a pull request. |
| Pushes rejected on a course | The course admin may have set commits to "editor only". |
| Git hooks fail after moving the binary | Hooks call the binary by absolute path; move it back or recreate the repos' hooks. |
| CI jobs show "error" | Is `-ci-runtime` set and does rootless podman work for the service user? See deployment.md. |
