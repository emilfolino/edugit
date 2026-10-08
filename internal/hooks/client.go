package hooks

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// Run is the "edugit hook <name>" entry point. It returns the hook's exit
// code. pre-receive fails closed when the server cannot be reached;
// post-receive never fails the push, which has already been applied.
func Run(ctx context.Context, name string, stdin io.Reader, stderr io.Writer, getenv func(string) string) int {
	if name != PreReceive && name != PostReceive {
		fmt.Fprintf(stderr, "edugit: unknown hook %q\n", name)
		return 2
	}
	fail := 1
	if name == PostReceive {
		fail = 0
	}
	req := Request{
		Course: getenv(EnvCourse),
		Repo:   getenv(EnvRepo),
		User:   getenv(EnvUser),
	}
	sock, token := getenv(EnvSocket), getenv(EnvToken)
	if sock == "" || token == "" || req.Course == "" || req.Repo == "" {
		fmt.Fprintln(stderr, "edugit: this repository only accepts pushes through the edugit server")
		return fail
	}

	sc := bufio.NewScanner(stdin)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 3 {
			continue
		}
		u := Update{Old: f[0], New: f[1], Ref: f[2]}
		if !u.IsCreate() && !u.IsDelete() {
			u.Forced = !isAncestor(ctx, u.Old, u.New)
		}
		req.Updates = append(req.Updates, u)
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintln(stderr, "edugit: read ref updates:", err)
		return fail
	}

	resp, err := call(ctx, sock, token, name, req)
	if err != nil {
		fmt.Fprintln(stderr, "edugit: hook callback failed:", err)
		return fail
	}
	for _, m := range resp.Messages {
		fmt.Fprintln(stderr, "remote:", m)
	}
	for _, m := range resp.Reject {
		fmt.Fprintln(stderr, "edugit: rejected:", m)
	}
	if len(resp.Reject) > 0 {
		return 1
	}
	return 0
}

// isAncestor reports whether old is an ancestor of new. It runs inside the
// hook, where git's quarantine environment makes the pushed objects visible.
func isAncestor(ctx context.Context, old, new string) bool {
	// Inherit the environment on purpose: GIT_QUARANTINE_PATH and friends.
	return exec.CommandContext(ctx, "git", "merge-base", "--is-ancestor", old, new).Run() == nil
}

func call(ctx context.Context, sock, token, name string, req Request) (Response, error) {
	var resp Response
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	body, err := json.Marshal(req)
	if err != nil {
		return resp, err
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://hook/"+name, bytes.NewReader(body))
	if err != nil {
		return resp, err
	}
	hreq.Header.Set("Authorization", "Bearer "+token)
	hreq.Header.Set("Content-Type", "application/json")
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}}
	hresp, err := client.Do(hreq)
	if err != nil {
		return resp, err
	}
	defer hresp.Body.Close()
	if hresp.StatusCode != http.StatusOK {
		return resp, fmt.Errorf("server returned %s", hresp.Status)
	}
	return resp, json.NewDecoder(io.LimitReader(hresp.Body, maxBody)).Decode(&resp)
}
