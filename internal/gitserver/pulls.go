package gitserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Merge strategies accepted by Repos.Merge.
const (
	StrategyMerge  = "merge"
	StrategySquash = "squash"
	StrategyRebase = "rebase"
)

// Errors returned by the pull request helpers.
var (
	// ErrConflict means the branches cannot be merged without conflicts.
	ErrConflict = errors.New("merge conflict")
	// ErrNothingToMerge means the head has no commits the base lacks.
	ErrNothingToMerge = errors.New("nothing to merge")
	// ErrStale means the base branch moved while the merge was computed.
	ErrStale = errors.New("base branch changed")
)

// maxDiffBytes caps the patch returned by Diff.
const maxDiffBytes = 4 << 20

// Commit is one entry of a pull request's commit list.
type Commit struct {
	SHA     string
	Author  string
	Subject string
	When    time.Time
}

// Identity is the committer recorded on commits the server creates.
type Identity struct {
	Name  string
	Email string
}

// gitRun runs git in dir with extra environment variables and returns stdout
// separately from stderr. A non-zero exit is reported through code.
func gitRun(ctx context.Context, dir string, extraEnv []string, args ...string) (stdout, stderr string, code int, err error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(gitEnv(), extraEnv...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out.String(), errb.String(), ee.ExitCode(), nil
	}
	return out.String(), errb.String(), 0, err
}

// gitOK runs git and fails on a non-zero exit.
func gitOK(ctx context.Context, dir string, extraEnv []string, args ...string) (string, error) {
	out, errs, code, err := gitRun(ctx, dir, extraEnv, args...)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("git %s: exit %d: %s", args[0], code, strings.TrimSpace(errs))
	}
	return strings.TrimSpace(out), nil
}

// Resolve returns the commit a branch points at.
func (r *Repos) Resolve(ctx context.Context, course, name, branch string) (string, error) {
	dir, err := r.Path(course, name)
	if err != nil {
		return "", err
	}
	if !validBranch(branch) {
		return "", fmt.Errorf("%w: branch", ErrInvalidName)
	}
	return gitOK(ctx, dir, nil, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch+"^{commit}")
}

// Commits lists the commits in head that base lacks, oldest first.
func (r *Repos) Commits(ctx context.Context, course, name, base, head string) ([]Commit, error) {
	dir, err := r.Path(course, name)
	if err != nil {
		return nil, err
	}
	if !isSHA(base) || !isSHA(head) {
		return nil, fmt.Errorf("%w: commit", ErrInvalidName)
	}
	out, err := gitOK(ctx, dir, nil, "log", "--reverse", "--format=%H%x00%an%x00%at%x00%s", base+".."+head, "--")
	if err != nil {
		return nil, err
	}
	var cs []Commit
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\x00")
		if len(f) != 4 {
			continue
		}
		var at int64
		fmt.Sscan(f[2], &at)
		cs = append(cs, Commit{SHA: f[0], Author: f[1], When: time.Unix(at, 0).UTC(), Subject: f[3]})
	}
	return cs, nil
}

// Diff returns the unified patch of head against its merge base with base,
// truncated at a few MiB.
func (r *Repos) Diff(ctx context.Context, course, name, base, head string) (string, error) {
	dir, err := r.Path(course, name)
	if err != nil {
		return "", err
	}
	if !isSHA(base) || !isSHA(head) {
		return "", fmt.Errorf("%w: commit", ErrInvalidName)
	}
	out, err := gitOK(ctx, dir, nil, "diff", "--no-color", "--no-ext-diff", "--no-renames", base+"..."+head, "--")
	if err != nil {
		return "", err
	}
	if len(out) > maxDiffBytes {
		out = out[:maxDiffBytes] + "\n"
	}
	return out, nil
}

// Mergeable reports whether head merges into base without conflicts.
func (r *Repos) Mergeable(ctx context.Context, course, name, base, head string) (bool, error) {
	dir, err := r.Path(course, name)
	if err != nil {
		return false, err
	}
	_, err = mergeTree(ctx, dir, "", base, head)
	if errors.Is(err, ErrConflict) {
		return false, nil
	}
	return err == nil, err
}

// mergeTree merges b into a (optionally from an explicit merge base) and
// returns the resulting tree, or ErrConflict.
func mergeTree(ctx context.Context, dir, mergeBase, a, b string) (string, error) {
	args := []string{"merge-tree", "--write-tree", "--no-messages"}
	if mergeBase != "" {
		args = append(args, "--merge-base="+mergeBase)
	}
	args = append(args, a, b)
	out, errs, code, err := gitRun(ctx, dir, nil, args...)
	switch {
	case err != nil:
		return "", err
	case code == 1:
		return "", ErrConflict
	case code != 0:
		return "", fmt.Errorf("merge-tree: exit %d: %s", code, strings.TrimSpace(errs))
	}
	tree, _, _ := strings.Cut(out, "\n")
	return strings.TrimSpace(tree), nil
}

// Merge integrates head into the base branch with the given strategy and
// returns the new base commit. It works on the object store only (no working
// tree) and moves the ref with a compare-and-swap, so a concurrent push makes
// it fail with ErrStale instead of losing commits. by is recorded as the
// committer; squash and merge commits are authored by it too, rebased
// commits keep their original author.
func (r *Repos) Merge(ctx context.Context, course, name, base, head, strategy string, by Identity, msg string) (string, error) {
	dir, err := r.Path(course, name)
	if err != nil {
		return "", err
	}
	if !validBranch(base) || !isSHA(head) {
		return "", fmt.Errorf("%w: branch", ErrInvalidName)
	}
	baseSHA, err := r.Resolve(ctx, course, name, base)
	if err != nil {
		return "", err
	}
	if _, _, code, err := gitRun(ctx, dir, nil, "merge-base", "--is-ancestor", head, baseSHA); err != nil {
		return "", err
	} else if code == 0 {
		return "", ErrNothingToMerge
	}
	who := []string{
		"GIT_COMMITTER_NAME=" + by.Name, "GIT_COMMITTER_EMAIL=" + by.Email,
		"GIT_AUTHOR_NAME=" + by.Name, "GIT_AUTHOR_EMAIL=" + by.Email,
	}
	var result string
	switch strategy {
	case StrategyMerge, StrategySquash:
		tree, err := mergeTree(ctx, dir, "", baseSHA, head)
		if err != nil {
			return "", err
		}
		if baseTree, err := gitOK(ctx, dir, nil, "rev-parse", baseSHA+"^{tree}"); err != nil {
			return "", err
		} else if baseTree == tree {
			return "", ErrNothingToMerge // e.g. already squashed in
		}
		parents := []string{"-p", baseSHA}
		if strategy == StrategyMerge {
			parents = append(parents, "-p", head)
		}
		result, err = gitOK(ctx, dir, who, append([]string{"commit-tree", tree}, append(parents, "-m", msg)...)...)
		if err != nil {
			return "", err
		}
	case StrategyRebase:
		if result, err = rebase(ctx, dir, baseSHA, head, who); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("unknown merge strategy %q", strategy)
	}
	_, errs, code, err := gitRun(ctx, dir, nil, "update-ref", "-m", "merge", "refs/heads/"+base, result, baseSHA)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("%w: %s", ErrStale, strings.TrimSpace(errs))
	}
	return result, nil
}

// rebase replays the commits of head that base lacks on top of base.
func rebase(ctx context.Context, dir, base, head string, committer []string) (string, error) {
	list, err := gitOK(ctx, dir, nil, "rev-list", "--reverse", "--topo-order", "--no-merges", base+".."+head, "--")
	if err != nil {
		return "", err
	}
	cur := base
	for _, c := range strings.Fields(list) {
		tree, err := mergeTree(ctx, dir, c+"^", cur, c)
		if err != nil {
			return "", err
		}
		if curTree, err := gitOK(ctx, dir, nil, "rev-parse", cur+"^{tree}"); err != nil {
			return "", err
		} else if curTree == tree {
			continue // the commit became empty
		}
		meta, err := gitOK(ctx, dir, nil, "log", "-1", "--format=%an%x00%ae%x00%aI%x00%B", c)
		if err != nil {
			return "", err
		}
		f := strings.SplitN(meta, "\x00", 4)
		if len(f) != 4 {
			return "", fmt.Errorf("rebase: unreadable commit %s", c)
		}
		env := append(append([]string{}, committer...),
			"GIT_AUTHOR_NAME="+f[0], "GIT_AUTHOR_EMAIL="+f[1], "GIT_AUTHOR_DATE="+f[2])
		if cur, err = gitOK(ctx, dir, env, "commit-tree", tree, "-p", cur, "-m", f[3]); err != nil {
			return "", err
		}
	}
	if cur == base {
		return "", ErrNothingToMerge
	}
	return cur, nil
}

// isSHA reports whether s looks like a full object name.
func isSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
