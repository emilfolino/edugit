package hooks

import (
	"context"
	"fmt"
	"path"
	"strings"
)

// Rule is a branch protection rule. Pattern is a path.Match glob against the
// branch name without "refs/heads/" ("main", "release/*"); "*" does not match
// across "/".
type Rule struct {
	Pattern    string
	RequirePR  bool
	AllowForce bool
}

// RuleSource loads the protection rules of a repository.
type RuleSource interface {
	Rules(ctx context.Context, course, repo string) ([]Rule, error)
}

// Protection is a Policy enforcing branch protection rules.
//
// Deleting a protected branch is always refused. Force-pushes are refused
// unless the rule allows them. When RequirePR is set every push to the branch
// is refused: changes arrive only through the merge engine (TODO #16), which
// updates refs directly on the server and so does not pass through hooks.
// Required approvals are enforced by that merge engine, not here.
type Protection struct {
	Source RuleSource
}

// CheckPush implements Policy.
func (p Protection) CheckPush(ctx context.Context, req Request) ([]string, error) {
	rules, err := p.Source.Rules(ctx, req.Course, req.Repo)
	if err != nil {
		return nil, fmt.Errorf("load protection rules: %w", err)
	}
	var reject []string
	for _, u := range req.Updates {
		branch, ok := strings.CutPrefix(u.Ref, "refs/heads/")
		if !ok {
			continue
		}
		for _, r := range rules {
			if m, err := path.Match(r.Pattern, branch); err != nil || !m {
				continue
			}
			switch {
			case u.IsDelete():
				reject = append(reject, fmt.Sprintf("branch %q is protected and cannot be deleted", branch))
			case r.RequirePR:
				reject = append(reject, fmt.Sprintf("branch %q is protected: open a pull request instead of pushing directly", branch))
			case u.Forced && !r.AllowForce:
				reject = append(reject, fmt.Sprintf("branch %q is protected: force-push is not allowed", branch))
			}
			break // first matching rule wins
		}
	}
	return reject, nil
}
