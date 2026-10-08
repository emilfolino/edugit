package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/emilfolino/edugit/internal/auth"
	"github.com/emilfolino/edugit/internal/authz"
	"github.com/emilfolino/edugit/internal/gitserver"
	"github.com/emilfolino/edugit/internal/hooks"
	"github.com/emilfolino/edugit/internal/store"
)

const (
	maxPushBytes = 512 << 20
	maxRepoBytes = 1 << 30
)

// ruleSource adapts the store's branch protection rules to hooks.RuleSource.
type ruleSource struct{ db *store.Store }

func (s ruleSource) Rules(ctx context.Context, course, repo string) ([]hooks.Rule, error) {
	rules, err := s.db.BranchRules(ctx, course, repo)
	if err != nil {
		return nil, err
	}
	out := make([]hooks.Rule, len(rules))
	for i, r := range rules {
		out[i] = hooks.Rule{Pattern: r.Pattern, RequirePR: r.RequirePR, AllowForce: r.AllowForce}
	}
	return out, nil
}

// newRepos opens the bare-repo root; new repositories get the hook shims.
func newRepos(dataDir string) (*gitserver.Repos, error) {
	bin, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("find own binary: %w", err)
	}
	repos, err := gitserver.NewRepos(filepath.Join(dataDir, "repos"))
	if err != nil {
		return nil, fmt.Errorf("repos: %w", err)
	}
	repos.InstallHooks = func(path string) error { return hooks.Install(path, bin) }
	return repos, nil
}

// setupGit starts the hook bridge and the GC
// scheduler, and mounts the git handler on mux. The handler is only mounted
// together with the hooks so pushes are never unprotected.
func setupGit(ctx context.Context, repos *gitserver.Repos, db *store.Store, sessions *auth.Sessions, authorizer *authz.Authorizer, mux *http.ServeMux, log *slog.Logger) error {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Errorf("hook secret: %w", err)
	}
	secret := hex.EncodeToString(raw[:])

	// The socket lives in a private temp dir; the data dir path may be too
	// long for sun_path.
	sockDir, err := os.MkdirTemp("", "edugit-")
	if err != nil {
		return fmt.Errorf("hook socket dir: %w", err)
	}
	sock := filepath.Join(sockDir, "hook.sock")
	bridge := &hooks.Bridge{Secret: secret, Policy: hooks.Protection{Source: ruleSource{db}}, Sink: pullSink{db}, Log: log}
	go func() {
		defer os.RemoveAll(sockDir)
		if err := bridge.Serve(ctx, sock); err != nil && ctx.Err() == nil {
			log.Error("hook bridge stopped", "err", err)
		}
	}()

	authorizer.Authenticate = sessions.GitUser
	h := &gitserver.Handler{
		Repos:        repos,
		Auth:         authorizer.Git,
		Log:          log,
		MaxPushBytes: maxPushBytes,
		MaxRepoBytes: maxRepoBytes,
		HookEnv: func(r *http.Request, course, repo string) []string {
			user := ""
			if u, ok := sessions.GitUser(r); ok {
				user = u.Username
			}
			return []string{
				hooks.EnvSocket + "=" + sock,
				hooks.EnvToken + "=" + secret,
				hooks.EnvCourse + "=" + course,
				hooks.EnvRepo + "=" + repo,
				hooks.EnvUser + "=" + user,
			}
		},
	}
	h.Routes(mux)
	go repos.RunGC(ctx, 24*time.Hour, log)
	return nil
}
