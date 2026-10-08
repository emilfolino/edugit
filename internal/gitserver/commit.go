package gitserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Errors returned by CommitFiles.
var (
	// ErrNothingToCommit means the changes leave the tree as it was.
	ErrNothingToCommit = errors.New("no changes to commit")
	// ErrBadPath means a changed path is unusable (traversal, .git, empty).
	ErrBadPath = errors.New("invalid file path")
	// ErrTooLarge means a file exceeds the editor limit.
	ErrTooLarge = errors.New("file too large")
)

// MaxEditBytes is the largest file the browser editor may write.
const MaxEditBytes = maxBlobBytes

// FileChange writes Content to Path, or removes Path when Delete is set.
type FileChange struct {
	Path    string
	Content []byte
	Delete  bool
}

func validChangePath(p string) bool {
	if p == "" || strings.HasSuffix(p, "/") || !validPath(p) {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if strings.EqualFold(seg, ".git") {
			return false
		}
	}
	return true
}

// CommitFiles applies changes on top of branch and returns the new commit.
// If branch does not exist it is created from the branch from. A non-empty
// expect must equal the current tip of branch (what the editor loaded), so
// concurrent work yields ErrStale rather than being overwritten. Like Merge it
// uses a temporary index and no working tree, moves the ref with a
// compare-and-swap, and bypasses hooks: the caller applies branch protection
// and notifies the push sink.
func (r *Repos) CommitFiles(ctx context.Context, course, name, branch, from, expect string, changes []FileChange, by Identity, msg string) (string, error) {
	dir, err := r.Path(course, name)
	if err != nil {
		return "", err
	}
	if !validBranch(branch) || (from != "" && !validBranch(from)) {
		return "", fmt.Errorf("%w: branch", ErrInvalidName)
	}
	if len(changes) == 0 {
		return "", ErrNothingToCommit
	}
	for _, c := range changes {
		if !validChangePath(c.Path) {
			return "", fmt.Errorf("%w: %q", ErrBadPath, c.Path)
		}
		if len(c.Content) > MaxEditBytes {
			return "", fmt.Errorf("%w: %s", ErrTooLarge, c.Path)
		}
	}
	oldTip, err := r.Resolve(ctx, course, name, branch)
	creating := err != nil // Resolve fails when the branch does not exist
	parent := oldTip
	if creating {
		if from == "" {
			return "", fmt.Errorf("%w: branch %s", ErrNoPath, branch)
		}
		if parent, err = r.Resolve(ctx, course, name, from); err != nil {
			return "", err
		}
	}
	if !creating && expect != "" && expect != oldTip {
		return "", ErrStale
	}

	tmp, err := os.MkdirTemp("", "edugit-index-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(tmp, "index"), "GIT_WORK_TREE=" + tmp} // update-index insists on a work tree; it is never touched
	if _, err := gitOK(ctx, dir, env, "read-tree", parent); err != nil {
		return "", err
	}
	for _, c := range changes {
		if c.Delete {
			if _, err := gitOK(ctx, dir, env, "update-index", "--force-remove", "--", c.Path); err != nil {
				return "", err
			}
			continue
		}
		mode := "100644"
		if out, err := gitOK(ctx, dir, env, "ls-files", "-s", "--", c.Path); err == nil {
			if f := strings.Fields(out); len(f) >= 3 && (f[0] == "100644" || f[0] == "100755") {
				mode = f[0] // keep the executable bit
			}
		}
		sha, err := hashObject(ctx, dir, c.Content)
		if err != nil {
			return "", err
		}
		if _, err := gitOK(ctx, dir, env, "update-index", "--add", "--cacheinfo", mode+","+sha+","+c.Path); err != nil {
			return "", err
		}
	}
	tree, err := gitOK(ctx, dir, env, "write-tree")
	if err != nil {
		return "", err
	}
	if old, err := gitOK(ctx, dir, nil, "rev-parse", parent+"^{tree}"); err == nil && old == tree {
		return "", ErrNothingToCommit
	}
	who := []string{
		"GIT_COMMITTER_NAME=" + by.Name, "GIT_COMMITTER_EMAIL=" + by.Email,
		"GIT_AUTHOR_NAME=" + by.Name, "GIT_AUTHOR_EMAIL=" + by.Email,
	}
	commit, err := gitOK(ctx, dir, who, "commit-tree", tree, "-p", parent, "-m", msg)
	if err != nil {
		return "", err
	}
	want := oldTip // empty when creating: the ref must not exist yet
	if creating {
		want = ""
	}
	_, errs, code, err := gitRun(ctx, dir, nil, "update-ref", "-m", "editor commit", "refs/heads/"+branch, commit, want)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("%w: %s", ErrStale, strings.TrimSpace(errs))
	}
	return commit, nil
}

// hashObject stores content as a blob and returns its id.
func hashObject(ctx context.Context, dir string, content []byte) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "hash-object", "-w", "--stdin")
	cmd.Dir = dir
	cmd.Env = gitEnv()
	cmd.Stdin = bytes.NewReader(content)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git hash-object: %w: %s", err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}
