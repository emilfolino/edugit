package sites

import (
	"context"
	"errors"
	"strings"

	"github.com/emilfolino/edugit/internal/hooks"
	"github.com/emilfolino/edugit/internal/store"
)

// Config looks up a course's site configuration.
type Config interface {
	SiteByCourse(ctx context.Context, slug string) (store.Site, error)
}

// Sink republishes the site when its source branch is pushed to. It
// implements hooks.Sink.
type Sink struct {
	DB  Config
	Pub *Publisher
}

// Pushed implements hooks.Sink.
func (s Sink) Pushed(ctx context.Context, req hooks.Request) error {
	site, err := s.DB.SiteByCourse(ctx, req.Course)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if site.Repo != req.Repo {
		return nil
	}
	for _, u := range req.Updates {
		if b, ok := strings.CutPrefix(u.Ref, "refs/heads/"); ok && b == site.Branch && !u.IsDelete() {
			return s.Pub.Publish(ctx, req.Course, site.Repo, site.Branch, site.Dir)
		}
	}
	return nil
}

// Multi fans a push out to several sinks, returning the first error after
// running all of them.
type Multi []hooks.Sink

// Pushed implements hooks.Sink.
func (m Multi) Pushed(ctx context.Context, req hooks.Request) error {
	var first error
	for _, s := range m {
		if err := s.Pushed(ctx, req); err != nil && first == nil {
			first = err
		}
	}
	return first
}
