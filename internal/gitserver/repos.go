// Package gitserver stores bare Git repositories on disk and serves them over
// the Git smart-HTTP protocol by running the system git binary.
//
// Repositories live at <root>/<course>/<name>.git. Course slugs and repository
// names are validated by Path, which is the only place that turns names into
// filesystem paths.
package gitserver

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	// ErrInvalidName is returned for course or repository names that are not
	// safe to use as path components.
	ErrInvalidName = errors.New("invalid name")
	// ErrNotFound is returned when a repository does not exist.
	ErrNotFound = errors.New("repository not found")
	// ErrExists is returned when creating or renaming onto an existing repository.
	ErrExists = errors.New("repository already exists")
)

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,99}$`)

// validName reports whether s is a safe path component: lowercase
// alphanumerics, dot, underscore and hyphen, starting with an alphanumeric,
// containing no "..", and not ending in ".git" or ".lock".
func validName(s string) bool {
	return nameRE.MatchString(s) &&
		!strings.Contains(s, "..") &&
		!strings.HasSuffix(s, ".git") &&
		!strings.HasSuffix(s, ".lock")
}

// Repos manages bare repositories under a root directory.
type Repos struct {
	root string
	// InstallHooks, if set, is called with the path of each new repository
	// before it becomes visible.
	InstallHooks func(repoPath string) error
}

// NewRepos returns a Repos rooted at dir, creating it if needed.
func NewRepos(dir string) (*Repos, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, fmt.Errorf("create repos root: %w", err)
	}
	return &Repos{root: abs}, nil
}

// Path returns the on-disk path of a repository, or ErrInvalidName. It does
// not check existence.
func (r *Repos) Path(course, name string) (string, error) {
	if !validName(course) || !validName(name) {
		return "", ErrInvalidName
	}
	p := filepath.Join(r.root, course, name+".git")
	if rel, err := filepath.Rel(r.root, p); err != nil || strings.HasPrefix(rel, "..") {
		return "", ErrInvalidName
	}
	return p, nil
}

// Exists reports whether the repository exists.
func (r *Repos) Exists(course, name string) (bool, error) {
	p, err := r.Path(course, name)
	if err != nil {
		return false, err
	}
	st, err := os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return st.IsDir(), nil
}

// Create initializes an empty bare repository whose HEAD points at
// defaultBranch. The repository is built in a temporary directory and renamed
// into place, so a half-created repository is never visible.
func (r *Repos) Create(ctx context.Context, course, name, defaultBranch string) error {
	dst, err := r.Path(course, name)
	if err != nil {
		return err
	}
	if !validBranch(defaultBranch) {
		return fmt.Errorf("%w: default branch %q", ErrInvalidName, defaultBranch)
	}
	if _, err := os.Stat(dst); err == nil {
		return ErrExists
	}
	courseDir := filepath.Dir(dst)
	if err := os.MkdirAll(courseDir, 0o750); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(courseDir, ".new-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	// --template= avoids copying sample hooks; hooks are installed by edugit.
	if out, err := runGit(ctx, "", "init", "--bare", "--quiet", "--template=", "--initial-branch="+defaultBranch, tmp); err != nil {
		return fmt.Errorf("git init: %w: %s", err, out)
	}
	for _, kv := range [][2]string{
		{"http.receivepack", "true"},
		{"receive.fsckObjects", "true"},
		{"gc.auto", "256"},
	} {
		if out, err := runGit(ctx, tmp, "config", kv[0], kv[1]); err != nil {
			return fmt.Errorf("git config %s: %w: %s", kv[0], err, out)
		}
	}
	if r.InstallHooks != nil {
		if err := r.InstallHooks(tmp); err != nil {
			return fmt.Errorf("install hooks: %w", err)
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		if _, statErr := os.Stat(dst); statErr == nil {
			return ErrExists
		}
		return err
	}
	return nil
}

// Delete removes a repository.
func (r *Repos) Delete(course, name string) error {
	p, err := r.Path(course, name)
	if err != nil {
		return err
	}
	if ok, err := r.Exists(course, name); err != nil {
		return err
	} else if !ok {
		return ErrNotFound
	}
	return os.RemoveAll(p)
}

// Rename moves a repository to a new name within the same course.
func (r *Repos) Rename(course, oldName, newName string) error {
	src, err := r.Path(course, oldName)
	if err != nil {
		return err
	}
	dst, err := r.Path(course, newName)
	if err != nil {
		return err
	}
	if ok, err := r.Exists(course, oldName); err != nil {
		return err
	} else if !ok {
		return ErrNotFound
	}
	if _, err := os.Stat(dst); err == nil {
		return ErrExists
	}
	return os.Rename(src, dst)
}

// DiskUsage returns the total size in bytes of all files in the repository.
func (r *Repos) DiskUsage(course, name string) (int64, error) {
	p, err := r.Path(course, name)
	if err != nil {
		return 0, err
	}
	var total int64
	err = filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return 0, ErrNotFound
	}
	return total, err
}

// GC runs "git gc --auto" on one repository. It only does work when git
// decides the repository needs it.
func (r *Repos) GC(ctx context.Context, course, name string) error {
	p, err := r.Path(course, name)
	if err != nil {
		return err
	}
	if out, err := runGit(ctx, p, "gc", "--auto", "--quiet"); err != nil {
		return fmt.Errorf("git gc: %w: %s", err, out)
	}
	return nil
}

// List returns every repository as {course, name} pairs.
func (r *Repos) List() ([][2]string, error) {
	courses, err := os.ReadDir(r.root)
	if err != nil {
		return nil, err
	}
	var out [][2]string
	for _, c := range courses {
		if !c.IsDir() || !validName(c.Name()) {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(r.root, c.Name()))
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if n, ok := strings.CutSuffix(e.Name(), ".git"); ok && e.IsDir() && validName(n) {
				out = append(out, [2]string{c.Name(), n})
			}
		}
	}
	return out, nil
}

var branchRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,99}$`)

func validBranch(b string) bool {
	return branchRE.MatchString(b) && !strings.Contains(b, "..") &&
		!strings.HasSuffix(b, "/") && !strings.HasSuffix(b, ".lock")
}

// gitEnv is the minimal, deterministic environment for git subprocesses: no
// system or user config, no prompts.
func gitEnv() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=/nonexistent",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C",
	}
}

// runGit runs git with an explicit argument list (never a shell) in dir and
// returns combined output.
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
