# Student guide

edugit works like GitHub: you get your own repository for each assignment, work on a branch, open a pull request and merge after review.

## First steps

1. Sign in with your university account.
2. Open your course and accept the assignment. You get a repository of your own.
3. Create a **personal access token** at `/account/tokens`. It is shown once. It is your password for Git over HTTPS; the username can be anything.
4. Depending on the course you work with Git on your computer, in the built-in browser editor, or both.

## Git primer

```
git clone https://<host>/git/<course>/<assignment>-<you>.git
cd <assignment>-<you>
git switch -c my-feature        # work on a branch, never directly on main
# edit files
git add -A
git commit -m "Describe what and why"
git push -u origin my-feature
```

Git asks for username and password: use anything and your token. Store it in a credential helper so you only type it once.

`main` is protected: a direct `git push origin main` is rejected on purpose. The work flow is:

1. **Branch**: one branch per change.
2. **Commit** small steps with clear messages.
3. **Push** the branch.
4. **Open a pull request** from your branch into `main`. Write what you changed and why. `Fixes #2` in the description closes issue 2 when merged.
5. **Review**: teachers or classmates comment on lines and approve or request changes. A new push to the branch means previous approvals no longer count. Answer comments and mark threads resolved.
6. **Merge** once the required approvals are in. Merge keeps all commits, squash makes one, rebase replays them on `main`.

If your branch conflicts with `main`, the pull request says so: `git fetch origin && git merge origin/main`, fix the marked files, commit and push.

## The browser editor

If your course allows it, the Edit link on a file opens an editor that commits for you. Protected branches cannot be edited directly: choose a new branch name and open a pull request afterwards.

## Deadlines

Repositories become read-only at the deadline. Ask your teacher about an extension before it passes. Your grade and feedback appear on the assignment page once graded.

## Team assignments

Create a team or join one on the assignment page, then work in the shared repository the same way, using branches and pull requests so that teammates can review.

## When things go wrong

| Message | Meaning |
|---|---|
| `Authentication failed` | Wrong or expired token; create a new one. Repeated failures temporarily block your address. |
| `rejected ... protected` | You pushed to `main`; push a branch and open a pull request. |
| `403` or "read-only" | The repository is locked after the deadline, or the course only allows the editor. |
| `404` | You do not have access to that repository, or it does not exist. |
