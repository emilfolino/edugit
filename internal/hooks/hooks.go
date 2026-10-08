// Package hooks connects git's pre-receive and post-receive hooks back to the
// running server.
//
// Each repository gets two tiny shell hooks that exec "edugit hook <name>".
// That client (Run) reads the ref updates from git, works out which are
// non-fast-forward (it must, because only the hook process can see the
// quarantined objects of an in-flight push) and reports them to the server over
// a unix socket. The server (Bridge) answers with a verdict from its Policy and
// forwards accepted pushes to its Sink.
//
// The socket path, a per-process secret and the pushing identity travel to the
// hook through environment variables that the git HTTP handler sets for each
// receive-pack process, so nothing secret is stored in the repository.
package hooks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Environment variables the git HTTP handler sets for hook processes.
const (
	EnvSocket = "EDUGIT_HOOK_SOCKET"
	EnvToken  = "EDUGIT_HOOK_TOKEN"
	EnvCourse = "EDUGIT_COURSE"
	EnvRepo   = "EDUGIT_REPO"
	EnvUser   = "EDUGIT_USER"
)

// Hook names edugit installs.
const (
	PreReceive  = "pre-receive"
	PostReceive = "post-receive"
)

// Update is one ref change in a push.
type Update struct {
	Ref    string `json:"ref"`
	Old    string `json:"old"`
	New    string `json:"new"`
	Forced bool   `json:"forced"` // non-fast-forward update of an existing ref
}

// Zero is the all-zero object ID git uses for "no object".
const zeroSHA1 = "0000000000000000000000000000000000000000"

// IsCreate reports whether the update creates the ref.
func (u Update) IsCreate() bool { return isZero(u.Old) }

// IsDelete reports whether the update deletes the ref.
func (u Update) IsDelete() bool { return isZero(u.New) }

func isZero(s string) bool { return s != "" && strings.Trim(s, "0") == "" }

// Request describes a push as seen by a hook.
type Request struct {
	Course  string   `json:"course"`
	Repo    string   `json:"repo"`
	User    string   `json:"user"`
	Updates []Update `json:"updates"`
}

// Response is the server's verdict. For pre-receive, a non-empty Reject
// refuses the whole push; every reason is shown to the pusher. Messages are
// informational lines shown either way.
type Response struct {
	Reject   []string `json:"reject,omitempty"`
	Messages []string `json:"messages,omitempty"`
}

// Install writes the edugit hooks into repoPath/hooks. binary is the absolute
// path of the edugit executable the hooks should run.
func Install(repoPath, binary string) error {
	if !filepath.IsAbs(binary) || strings.ContainsAny(binary, "'\n\x00") {
		return fmt.Errorf("install hooks: unsafe binary path %q", binary)
	}
	dir := filepath.Join(repoPath, "hooks")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	for _, name := range []string{PreReceive, PostReceive} {
		script := fmt.Sprintf("#!/bin/sh\nexec '%s' hook %s \"$@\"\n", binary, name)
		// Hooks must be executable by the server user only.
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
			return err
		}
	}
	return nil
}
