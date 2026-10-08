// Package sites publishes a course's static site from a teacher repo. Each
// publish exports the branch into a fresh directory and atomically repoints
// a "current" symlink, so readers never see a half-written site.
package sites

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Exporter writes a branch's tree into an existing directory.
type Exporter interface {
	Export(ctx context.Context, course, repo, branch, dir, dest string) error
}

// Publisher owns the published sites under Root/<course>/.
type Publisher struct {
	Root string
	Git  Exporter

	mu sync.Mutex // serializes publishes; sites are small and pushes rare
}

func (p *Publisher) base(course string) (string, error) {
	if course == "" || course != filepath.Base(course) || strings.HasPrefix(course, ".") {
		return "", fmt.Errorf("invalid course %q", course)
	}
	return filepath.Join(p.Root, course), nil
}

// Publish exports dir of branch in repo and makes it the course's live site.
func (p *Publisher) Publish(ctx context.Context, course, repo, branch, dir string) error {
	base, err := p.base(course)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := os.MkdirAll(base, 0o755); err != nil {
		return err
	}
	build, err := os.MkdirTemp(base, "build-")
	if err != nil {
		return err
	}
	if err := p.Git.Export(ctx, course, repo, branch, dir, build); err != nil {
		_ = os.RemoveAll(build)
		return err
	}
	var raw [6]byte
	if _, err := rand.Read(raw[:]); err != nil {
		_ = os.RemoveAll(build)
		return err
	}
	link := filepath.Join(base, "current")
	tmp := filepath.Join(base, ".link-"+hex.EncodeToString(raw[:]))
	old, _ := os.Readlink(link)
	if err := os.Symlink(filepath.Base(build), tmp); err != nil {
		_ = os.RemoveAll(build)
		return err
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		_ = os.RemoveAll(build)
		return err
	}
	if old != "" && old != filepath.Base(build) && strings.HasPrefix(old, "build-") {
		_ = os.RemoveAll(filepath.Join(base, old))
	}
	return nil
}

// Remove unpublishes the course's site.
func (p *Publisher) Remove(course string) error {
	base, err := p.base(course)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return os.RemoveAll(base)
}

// ErrNoSite means nothing has been published for the course.
var ErrNoSite = errors.New("no published site")

// Handler serves the course's live site; prefix is the URL path prefix to
// strip (e.g. "/sites/oop"). Directory listings are disabled.
func (p *Publisher) Handler(course, prefix string) (http.Handler, error) {
	base, err := p.base(course)
	if err != nil {
		return nil, err
	}
	root := filepath.Join(base, "current")
	if _, err := os.Stat(root); err != nil {
		return nil, ErrNoSite
	}
	return http.StripPrefix(prefix, http.FileServer(noListing{http.Dir(root)})), nil
}

// noListing serves index.html for directories and 404s directories without
// one, instead of listing them.
type noListing struct{ fs http.FileSystem }

func (n noListing) Open(name string) (http.File, error) {
	f, err := n.fs.Open(name)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if st.IsDir() {
		idx, err := n.fs.Open(strings.TrimSuffix(name, "/") + "/index.html")
		if err != nil {
			_ = f.Close()
			return nil, os.ErrNotExist
		}
		_ = idx.Close()
	}
	return f, nil
}
