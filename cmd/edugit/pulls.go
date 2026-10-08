package main

import (
	"context"
	"strings"

	"github.com/emilfolino/edugit/internal/hooks"
	"github.com/emilfolino/edugit/internal/store"
)

// pullSync is the subset of the store pullSink needs.
type pullSync interface {
	RepoByName(ctx context.Context, courseSlug, name string, userID int64) (store.Repo, bool, error)
	OpenPullsTouching(ctx context.Context, repoID int64, branch string) ([]store.PullRequest, error)
	TouchPull(ctx context.Context, id int64) error
	SetPullState(ctx context.Context, id int64, state, mergeCommit string) error
}

// pullSink keeps pull requests current on push: pushes to a head or base
// branch bump the pull request, and deleting either branch closes it.
type pullSink struct{ db pullSync }

// Pushed implements hooks.Sink.
func (p pullSink) Pushed(ctx context.Context, req hooks.Request) error {
	var repo *store.Repo
	for _, u := range req.Updates {
		branch, ok := strings.CutPrefix(u.Ref, "refs/heads/")
		if !ok {
			continue
		}
		if repo == nil {
			r, _, err := p.db.RepoByName(ctx, req.Course, req.Repo, 0)
			if err != nil {
				return err
			}
			repo = &r
		}
		pulls, err := p.db.OpenPullsTouching(ctx, repo.ID, branch)
		if err != nil {
			return err
		}
		for _, pr := range pulls {
			if u.IsDelete() {
				err = p.db.SetPullState(ctx, pr.ID, store.PullClosed, "")
			} else {
				err = p.db.TouchPull(ctx, pr.ID)
			}
			if err != nil {
				return err
			}
		}
	}
	return nil
}
