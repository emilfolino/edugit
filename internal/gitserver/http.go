package gitserver

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
)

// Errors an Authorizer may return to select the HTTP response.
var (
	// ErrUnauthenticated asks the client for credentials (401).
	ErrUnauthenticated = errors.New("authentication required")
	// ErrForbidden denies access to an authenticated user (403).
	ErrForbidden = errors.New("forbidden")
)

// Authorizer decides whether the request may read (write == false) or push
// (write == true) to the repository. It returns nil to allow,
// ErrUnauthenticated, ErrForbidden, or ErrNotFound (to hide existence).
type Authorizer func(r *http.Request, course, repo string, write bool) error

// Handler serves the Git smart-HTTP protocol (v0/v1/v2) for repositories in
// Repos. Mount it with Routes. The dumb protocol is not supported.
type Handler struct {
	Repos *Repos
	Auth  Authorizer
	Log   *slog.Logger
	// MaxPushBytes bounds a single push request body. Zero means no limit.
	MaxPushBytes int64
	// MaxRepoBytes rejects pushes to repositories already larger than this.
	// Zero means no limit.
	MaxRepoBytes int64
	// HookEnv returns extra environment variables for receive-pack, used to
	// tell hooks who is pushing and how to call back. May be nil.
	HookEnv func(r *http.Request, course, repo string) []string
}

// Routes registers the git endpoints under /git/ on mux.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /git/{course}/{repo}/info/refs", h.infoRefs)
	mux.HandleFunc("POST /git/{course}/{repo}/git-upload-pack", h.service("git-upload-pack"))
	mux.HandleFunc("POST /git/{course}/{repo}/git-receive-pack", h.service("git-receive-pack"))
}

var protocolRE = regexp.MustCompile(`^[A-Za-z0-9=:_.-]{1,64}$`)

// subcommand maps a service name to the git subcommand and whether it writes.
func subcommand(svc string) (cmd string, write, ok bool) {
	switch svc {
	case "git-upload-pack":
		return "upload-pack", false, true
	case "git-receive-pack":
		return "receive-pack", true, true
	}
	return "", false, false
}

// resolve authorizes the request and returns the repository path.
func (h *Handler) resolve(w http.ResponseWriter, r *http.Request, write bool) (course, name, path string, ok bool) {
	course = r.PathValue("course")
	name = strings.TrimSuffix(r.PathValue("repo"), ".git")
	if r.PathValue("repo") == name { // clients must use the .git form
		http.NotFound(w, r)
		return
	}
	p, err := h.Repos.Path(course, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := h.Auth(r, course, name, write); err != nil {
		switch {
		case errors.Is(err, ErrUnauthenticated):
			w.Header().Set("WWW-Authenticate", `Basic realm="edugit", charset="UTF-8"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
		case errors.Is(err, ErrForbidden):
			http.Error(w, "forbidden", http.StatusForbidden)
		case errors.Is(err, ErrNotFound):
			http.NotFound(w, r)
		default:
			h.Log.Error("authorize", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	if exists, err := h.Repos.Exists(course, name); err != nil || !exists {
		http.NotFound(w, r)
		return
	}
	return course, name, p, true
}

func (h *Handler) infoRefs(w http.ResponseWriter, r *http.Request) {
	svc := r.URL.Query().Get("service")
	sub, write, ok := subcommand(svc)
	if !ok {
		http.Error(w, "smart HTTP only", http.StatusForbidden)
		return
	}
	_, _, path, ok := h.resolve(w, r, write)
	if !ok {
		return
	}
	proto := r.Header.Get("Git-Protocol")
	w.Header().Set("Content-Type", "application/x-"+svc+"-advertisement")
	noCache(w)
	if !strings.Contains(proto, "version=2") {
		pktLine(w, "# service="+svc+"\n")
		io.WriteString(w, "0000")
	}
	h.run(r.Context(), w, nil, nil, path, proto, sub, "--stateless-rpc", "--advertise-refs")
}

func (h *Handler) service(svc string) http.HandlerFunc {
	sub, write, _ := subcommand(svc)
	return func(w http.ResponseWriter, r *http.Request) {
		course, name, path, ok := h.resolve(w, r, write)
		if !ok {
			return
		}
		if r.Header.Get("Content-Type") != "application/x-"+svc+"-request" {
			http.Error(w, "unsupported media type", http.StatusUnsupportedMediaType)
			return
		}
		if write && h.MaxRepoBytes > 0 {
			if used, err := h.Repos.DiskUsage(course, name); err == nil && used > h.MaxRepoBytes {
				http.Error(w, "repository size limit exceeded", http.StatusRequestEntityTooLarge)
				return
			}
		}
		var body io.Reader = r.Body
		if write && h.MaxPushBytes > 0 {
			body = http.MaxBytesReader(w, r.Body, h.MaxPushBytes)
		}
		if r.Header.Get("Content-Encoding") == "gzip" {
			zr, err := gzip.NewReader(body)
			if err != nil {
				http.Error(w, "bad gzip body", http.StatusBadRequest)
				return
			}
			defer zr.Close()
			body = zr
		}
		w.Header().Set("Content-Type", "application/x-"+svc+"-result")
		noCache(w)
		var env []string
		if write && h.HookEnv != nil {
			env = h.HookEnv(r, course, name)
		}
		h.run(r.Context(), w, body, env, path, r.Header.Get("Git-Protocol"), sub, "--stateless-rpc")
	}
}

// run executes git <sub> <args...> <path>, streaming stdout to w.
func (h *Handler) run(ctx context.Context, w http.ResponseWriter, stdin io.Reader, extraEnv []string, path, proto, sub string, args ...string) {
	cmd := exec.CommandContext(ctx, "git", append([]string{sub}, append(args, path)...)...)
	cmd.Env = append(gitEnv(), extraEnv...)
	if protocolRE.MatchString(proto) {
		cmd.Env = append(cmd.Env, "GIT_PROTOCOL="+proto)
	}
	cmd.Stdin = stdin
	cmd.Stdout = &flushWriter{w: w, rc: http.NewResponseController(w)}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil && ctx.Err() == nil {
		h.Log.Warn("git service failed", "sub", sub, "err", err, "stderr", strings.TrimSpace(stderr.String()))
	}
}

func noCache(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-cache, max-age=0, must-revalidate")
	w.Header().Set("Expires", "Fri, 01 Jan 1980 00:00:00 GMT")
	w.Header().Set("Pragma", "no-cache")
}

func pktLine(w io.Writer, s string) {
	fmt.Fprintf(w, "%04x%s", len(s)+4, s)
}

// flushWriter flushes after every write so fetch/push progress streams.
type flushWriter struct {
	w  io.Writer
	rc *http.ResponseController
}

func (f *flushWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	_ = f.rc.Flush()
	return n, err
}
