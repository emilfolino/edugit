package authz

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/emilfolino/edugit/internal/gitserver"
	"github.com/emilfolino/edugit/internal/store"
)

// Source supplies the facts Can needs. *store.Store implements it.
type Source interface {
	Roles(ctx context.Context, userID int64) (map[int64]string, error)
	RepoByName(ctx context.Context, courseSlug, name string, userID int64) (store.Repo, bool, error)
}

// Authorizer turns store facts into Can decisions.
type Authorizer struct {
	Source Source
	// Authenticate identifies the caller of a git request (token auth). It
	// returns false when there are no valid credentials.
	Authenticate func(r *http.Request) (store.User, bool)
}

// Principal loads u's roles.
func (z *Authorizer) Principal(ctx context.Context, u store.User) (Principal, error) {
	names, err := z.Source.Roles(ctx, u.ID)
	if err != nil {
		return Principal{}, fmt.Errorf("load roles: %w", err)
	}
	p := Principal{UserID: u.ID, IsAdmin: u.IsAdmin, Roles: make(map[int64]Role, len(names))}
	for id, n := range names {
		p.Roles[id] = ParseRole(n)
	}
	return p, nil
}

// Git is a gitserver.Authorizer. Unauthenticated requests get 401 so git
// prompts for a token; repositories the user may not read are reported as
// not found so their existence is not revealed. A user who can read but not
// write gets 403 on push.
func (z *Authorizer) Git(r *http.Request, course, repo string, write bool) error {
	u, ok := z.Authenticate(r)
	if !ok {
		return gitserver.ErrUnauthenticated
	}
	ctx := r.Context()
	rec, member, err := z.Source.RepoByName(ctx, course, repo, u.ID)
	if errors.Is(err, store.ErrNotFound) {
		return gitserver.ErrNotFound
	}
	if err != nil {
		return err
	}
	p, err := z.Principal(ctx, u)
	if err != nil {
		return err
	}
	res := Resource{
		CourseID: rec.CourseID, Repo: true, Kind: RepoKind(rec.Kind),
		IsTemplate: rec.IsTemplate, Archived: rec.Archived, IsMember: member,
	}
	if !Can(p, RepoRead, res) {
		return gitserver.ErrNotFound
	}
	if write && !Can(p, RepoWrite, res) {
		return gitserver.ErrForbidden
	}
	return nil
}
