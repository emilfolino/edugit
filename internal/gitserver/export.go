package gitserver

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// Limits for Export, so a pushed tree cannot fill the disk.
const (
	maxExportFiles = 20000
	maxExportBytes = 256 << 20
)

// Export writes the tree at dir (empty for the root) of branch into dest,
// which must exist. Only regular files and directories are written: symlinks
// and special files are skipped so the result cannot point outside dest.
func (r *Repos) Export(ctx context.Context, course, name, branch, dir, dest string) error {
	repo, err := r.Path(course, name)
	if err != nil {
		return err
	}
	if !validBranch(branch) || !validPath(dir) {
		return fmt.Errorf("%w: branch or dir", ErrInvalidName)
	}
	spec := "refs/heads/" + branch
	if dir != "" {
		spec += ":" + dir
	}
	cmd := exec.CommandContext(ctx, "git", "archive", "--format=tar", spec)
	cmd.Dir = repo
	cmd.Env = gitEnv()
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr limitedBuf
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	xerr := extractTar(out, dest)
	if xerr != nil {
		_, _ = io.Copy(io.Discard, out)
	}
	if werr := cmd.Wait(); werr != nil {
		return fmt.Errorf("git archive: %w: %s", werr, stderr.String())
	}
	return xerr
}

func extractTar(src io.Reader, dest string) error {
	tr := tar.NewReader(src)
	var files int
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir {
			continue
		}
		if !filepath.IsLocal(h.Name) {
			return fmt.Errorf("unsafe path %q in archive", h.Name)
		}
		target := filepath.Join(dest, h.Name)
		if h.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if files++; files > maxExportFiles {
			return errors.New("too many files to publish")
		}
		if total += h.Size; total > maxExportBytes {
			return errors.New("site too large to publish")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, tr)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
}

// limitedBuf keeps the first few KiB of stderr for error messages.
type limitedBuf struct{ b []byte }

func (l *limitedBuf) Write(p []byte) (int, error) {
	if room := 4096 - len(l.b); room > 0 {
		l.b = append(l.b, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

func (l *limitedBuf) String() string { return string(l.b) }
