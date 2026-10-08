package gitserver

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ErrNoPath means the ref or path does not exist in the repository.
var ErrNoPath = errors.New("not found in repository")

// Limits for the browsing helpers.
const (
	maxBlobBytes = 1 << 20
	maxLogCount  = 200
)

// TreeEntry is one row of a directory listing.
type TreeEntry struct {
	Name string
	Dir  bool
	Size int64 // blobs only
	Mode string
}

// Blob is the content of one file, cut at maxBlobBytes.
type Blob struct {
	Size      int64
	Binary    bool
	Truncated bool
	Content   string
}

// BlameLine attributes one line of a file to a commit.
type BlameLine struct {
	SHA    string
	Author string
	Line   int
	Text   string
	// First is set on the first line of a run from the same commit.
	First bool
}

// validRef accepts a branch name or a full commit id.
func validRef(ref string) bool { return validBranch(ref) || isSHA(ref) }

// validPath accepts a repository-relative path without traversal, NULs or
// newlines. The empty path is the root.
func validPath(p string) bool {
	if p == "" {
		return true
	}
	if strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\x00\n\r") {
		return false
	}
	for _, seg := range strings.Split(strings.TrimSuffix(p, "/"), "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// revision turns a validated ref into an unambiguous commit-ish.
func revision(ref string) string {
	if isSHA(ref) {
		return ref + "^{commit}"
	}
	return "refs/heads/" + ref + "^{commit}"
}

func (r *Repos) browseArgs(course, name, ref, path string) (dir, rev string, err error) {
	if dir, err = r.Path(course, name); err != nil {
		return "", "", err
	}
	if !validRef(ref) || !validPath(path) {
		return "", "", fmt.Errorf("%w: ref or path", ErrInvalidName)
	}
	return dir, revision(ref), nil
}

// Branches lists branch names, the default branch first.
func (r *Repos) Branches(ctx context.Context, course, name string) ([]string, error) {
	dir, err := r.Path(course, name)
	if err != nil {
		return nil, err
	}
	out, err := gitOK(ctx, dir, nil, "for-each-ref", "--format=%(refname:short)", "refs/heads/")
	if err != nil {
		return nil, err
	}
	def, _ := r.DefaultBranch(ctx, course, name)
	var bs []string
	for _, b := range strings.Fields(out) {
		if b == def {
			bs = append([]string{b}, bs...)
		} else {
			bs = append(bs, b)
		}
	}
	return bs, nil
}

// Tree lists the directory at path in ref. Directories come first.
func (r *Repos) Tree(ctx context.Context, course, name, ref, path string) ([]TreeEntry, error) {
	dir, rev, err := r.browseArgs(course, name, ref, path)
	if err != nil {
		return nil, err
	}
	sha, err := gitOK(ctx, dir, nil, "rev-parse", "--verify", "--quiet", rev)
	if err != nil {
		return nil, ErrNoPath
	}
	spec := sha + "^{tree}"
	if path != "" {
		spec = sha + ":" + strings.TrimSuffix(path, "/")
	}
	if t, err := gitOK(ctx, dir, nil, "cat-file", "-t", spec); err != nil || t != "tree" {
		return nil, ErrNoPath
	}
	out, _, code, err := gitRun(ctx, dir, nil, "ls-tree", "-z", "-l", spec)
	if err != nil || code != 0 {
		return nil, ErrNoPath
	}
	var dirs, files []TreeEntry
	for _, rec := range strings.Split(out, "\x00") {
		meta, nm, ok := strings.Cut(rec, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 4 {
			continue
		}
		e := TreeEntry{Name: nm, Mode: f[0]}
		switch f[1] {
		case "tree":
			e.Dir = true
			dirs = append(dirs, e)
		case "blob":
			e.Size, _ = strconv.ParseInt(f[3], 10, 64)
			files = append(files, e)
		}
	}
	return append(dirs, files...), nil
}

// File returns the blob at path in ref.
func (r *Repos) File(ctx context.Context, course, name, ref, path string) (Blob, error) {
	dir, rev, err := r.browseArgs(course, name, ref, path)
	if err != nil {
		return Blob{}, err
	}
	if path == "" {
		return Blob{}, ErrNoPath
	}
	sha, err := gitOK(ctx, dir, nil, "rev-parse", "--verify", "--quiet", rev)
	if err != nil {
		return Blob{}, ErrNoPath
	}
	spec := sha + ":" + path
	if t, err := gitOK(ctx, dir, nil, "cat-file", "-t", spec); err != nil || t != "blob" {
		return Blob{}, ErrNoPath
	}
	sz, err := gitOK(ctx, dir, nil, "cat-file", "-s", spec)
	if err != nil {
		return Blob{}, err
	}
	b := Blob{}
	b.Size, _ = strconv.ParseInt(sz, 10, 64)
	// Read through "show" with a size cap by using cat-file on the blob and
	// trimming; blobs over the cap are not fully loaded into the response.
	if b.Size > maxBlobBytes {
		b.Truncated = true
	}
	raw, _, code, err := gitRun(ctx, dir, nil, "cat-file", "blob", spec)
	if err != nil || code != 0 {
		return Blob{}, ErrNoPath
	}
	if len(raw) > maxBlobBytes {
		raw = raw[:maxBlobBytes]
	}
	probe := raw
	if len(probe) > 8000 {
		probe = probe[:8000]
	}
	b.Binary = strings.IndexByte(probe, 0) >= 0
	if !b.Binary {
		b.Content = raw
	}
	return b, nil
}

// Log lists up to limit commits reachable from ref, newest first, optionally
// restricted to path.
func (r *Repos) Log(ctx context.Context, course, name, ref, path string, limit int) ([]Commit, error) {
	dir, rev, err := r.browseArgs(course, name, ref, path)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > maxLogCount {
		limit = maxLogCount
	}
	sha, err := gitOK(ctx, dir, nil, "rev-parse", "--verify", "--quiet", rev)
	if err != nil {
		return nil, ErrNoPath
	}
	args := []string{"log", "-n", strconv.Itoa(limit), "--format=%H%x00%an%x00%at%x00%s", sha, "--"}
	if path != "" {
		args = append(args, path)
	}
	out, err := gitOK(ctx, dir, nil, args...)
	if err != nil {
		return nil, err
	}
	return parseLog(out), nil
}

func parseLog(out string) []Commit {
	var cs []Commit
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\x00")
		if len(f) != 4 {
			continue
		}
		at, _ := strconv.ParseInt(f[2], 10, 64)
		cs = append(cs, Commit{SHA: f[0], Author: f[1], When: time.Unix(at, 0).UTC(), Subject: f[3]})
	}
	return cs
}

// CommitDiff returns one commit's metadata and its patch against its first
// parent (or the empty tree).
func (r *Repos) CommitDiff(ctx context.Context, course, name, sha string) (Commit, string, error) {
	dir, err := r.Path(course, name)
	if err != nil {
		return Commit{}, "", err
	}
	if !isSHA(sha) {
		return Commit{}, "", fmt.Errorf("%w: commit", ErrInvalidName)
	}
	out, err := gitOK(ctx, dir, nil, "log", "-n1", "--format=%H%x00%an%x00%at%x00%s", sha+"^{commit}", "--")
	if err != nil {
		return Commit{}, "", ErrNoPath
	}
	cs := parseLog(out)
	if len(cs) != 1 {
		return Commit{}, "", ErrNoPath
	}
	patch, err := gitOK(ctx, dir, nil, "show", "--no-color", "--no-ext-diff", "--no-renames", "--format=", sha, "--")
	if err != nil {
		return Commit{}, "", err
	}
	if len(patch) > maxDiffBytes {
		patch = patch[:maxDiffBytes] + "\n"
	}
	return cs[0], patch, nil
}

// Blame attributes each line of the file at path in ref to a commit.
func (r *Repos) Blame(ctx context.Context, course, name, ref, path string) ([]BlameLine, error) {
	dir, rev, err := r.browseArgs(course, name, ref, path)
	if err != nil || path == "" {
		return nil, ErrNoPath
	}
	sha, err := gitOK(ctx, dir, nil, "rev-parse", "--verify", "--quiet", rev)
	if err != nil {
		return nil, ErrNoPath
	}
	if t, err := gitOK(ctx, dir, nil, "cat-file", "-t", sha+":"+path); err != nil || t != "blob" {
		return nil, ErrNoPath
	}
	out, _, code, err := gitRun(ctx, dir, nil, "blame", "--line-porcelain", sha, "--", path)
	if err != nil || code != 0 {
		return nil, ErrNoPath
	}
	var lines []BlameLine
	var cur BlameLine
	prev := ""
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(l, "\t"):
			cur.Text = l[1:]
			cur.First = cur.SHA != prev
			prev = cur.SHA
			lines = append(lines, cur)
			if len(lines) >= 5000 {
				return lines, nil
			}
		case strings.HasPrefix(l, "author "):
			cur.Author = strings.TrimPrefix(l, "author ")
		default:
			if f := strings.Fields(l); len(f) >= 3 && isSHA(f[0]) {
				cur.SHA = f[0]
				cur.Line, _ = strconv.Atoi(f[2])
			}
		}
	}
	return lines, nil
}
