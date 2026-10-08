package ci

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/emilfolino/edugit/internal/gitserver"
	"github.com/emilfolino/edugit/internal/hooks"
	"github.com/emilfolino/edugit/internal/store"
)

const maxLog = 256 << 10

// Store is the persistence the runner needs.
type Store interface {
	CreateCIRun(ctx context.Context, repoID int64, sha, branch, job, spec string) (int64, error)
	StartCIRun(ctx context.Context, id int64) error
	FinishCIRun(ctx context.Context, id int64, status, log string) error
	FailInterruptedCIRuns(ctx context.Context) (int64, error)
	QueuedCIRuns(ctx context.Context) ([]store.CIRun, error)
	RepoByName(ctx context.Context, courseSlug, name string, userID int64) (store.Repo, bool, error)
}

// Source reads repository content.
type Source interface {
	File(ctx context.Context, course, name, ref, path string) (gitserver.Blob, error)
	Export(ctx context.Context, course, name, ref, dir, dest string) error
}

// Runner executes queued jobs in containers and implements hooks.Sink.
type Runner struct {
	Store Store
	Git   Source
	// Runtime is the container command, "podman" by default.
	Runtime string
	// AllowNetwork lets jobs that ask for it have network access.
	AllowNetwork bool
	// Workers is the number of concurrent jobs (default 2).
	Workers int
	Log     *slog.Logger

	queue chan store.CIRun
	once  sync.Once
}

func (r *Runner) runtime() string {
	if r.Runtime == "" {
		return "podman"
	}
	return r.Runtime
}

func (r *Runner) logger() *slog.Logger {
	if r.Log == nil {
		return slog.Default()
	}
	return r.Log
}

// Start launches the workers, fails runs a previous process left running and
// requeues the queued ones. Workers stop when ctx is done.
func (r *Runner) Start(ctx context.Context) error {
	r.init()
	if n, err := r.Store.FailInterruptedCIRuns(ctx); err != nil {
		return err
	} else if n > 0 {
		r.logger().Warn("ci runs interrupted by restart", "count", n)
	}
	workers := r.Workers
	if workers <= 0 {
		workers = 2
	}
	for range workers {
		go r.work(ctx)
	}
	queued, err := r.Store.QueuedCIRuns(ctx)
	if err != nil {
		return err
	}
	go func() {
		for _, q := range queued {
			select {
			case r.queue <- q:
			case <-ctx.Done():
				return
			}
		}
	}()
	return nil
}

func (r *Runner) init() {
	r.once.Do(func() { r.queue = make(chan store.CIRun, 256) })
}

func (r *Runner) work(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case run := <-r.queue:
			r.execute(ctx, run)
		}
	}
}

// Pushed implements hooks.Sink: it queues the jobs declared at each pushed
// branch tip.
func (r *Runner) Pushed(ctx context.Context, req hooks.Request) error {
	r.init()
	repo, _, err := r.Store.RepoByName(ctx, req.Course, req.Repo, 0)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var first error
	for _, u := range req.Updates {
		branch, ok := strings.CutPrefix(u.Ref, "refs/heads/")
		if !ok || u.IsDelete() {
			continue
		}
		if err := r.enqueue(ctx, req.Course, repo, branch, u.New); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (r *Runner) enqueue(ctx context.Context, course string, repo store.Repo, branch, sha string) error {
	blob, err := r.Git.File(ctx, course, repo.Name, sha, ConfigPath)
	if errors.Is(err, gitserver.ErrNoPath) {
		return nil
	}
	if err != nil {
		return err
	}
	cfg, err := Parse([]byte(blob.Content))
	if err != nil {
		// Surface the mistake as a failed check instead of silently nothing.
		id, cerr := r.Store.CreateCIRun(ctx, repo.ID, sha, branch, "config", "{}")
		if cerr != nil {
			return cerr
		}
		return r.Store.FinishCIRun(ctx, id, store.CIError, err.Error())
	}
	for _, j := range cfg.Jobs {
		spec, _ := json.Marshal(j)
		id, err := r.Store.CreateCIRun(ctx, repo.ID, sha, branch, j.Name, string(spec))
		if err != nil {
			return err
		}
		run := store.CIRun{ID: id, RepoID: repo.ID, Course: course, Repo: repo.Name, SHA: sha, Branch: branch, Job: j.Name, Spec: string(spec)}
		select {
		case r.queue <- run:
		default:
			// Queue full: the run stays queued in the DB and is picked up
			// after the next restart.
			r.logger().Warn("ci queue full", "run", id)
		}
	}
	return nil
}

func (r *Runner) execute(ctx context.Context, run store.CIRun) {
	if err := r.Store.StartCIRun(ctx, run.ID); err != nil {
		r.logger().Error("ci start", "run", run.ID, "err", err)
		return
	}
	status, log := r.runJob(ctx, run)
	// Record the result even when shutting down.
	if err := r.Store.FinishCIRun(context.WithoutCancel(ctx), run.ID, status, log); err != nil {
		r.logger().Error("ci finish", "run", run.ID, "err", err)
	}
}

func (r *Runner) runJob(ctx context.Context, run store.CIRun) (string, string) {
	var job Job
	if err := json.Unmarshal([]byte(run.Spec), &job); err != nil {
		return store.CIError, "invalid job spec: " + err.Error()
	}
	dir, err := os.MkdirTemp("", "edugit-ci-")
	if err != nil {
		return store.CIError, err.Error()
	}
	defer os.RemoveAll(dir)
	if err := r.Git.Export(ctx, run.Course, run.Repo, run.SHA, "", dir); err != nil {
		return store.CIError, "checkout failed: " + err.Error()
	}
	// The container user must be able to write the checkout.
	if err := os.Chmod(dir, 0o777); err != nil {
		return store.CIError, err.Error()
	}

	name := fmt.Sprintf("edugit-ci-%d", run.ID)
	jctx, cancel := context.WithTimeout(ctx, job.Duration())
	defer cancel()

	args := []string{"run", "--rm", "--name", name,
		"--cap-drop=all", "--security-opt", "no-new-privileges",
		"--pids-limit", "256", "--memory", "512m", "--cpus", "1",
		"--read-only", "--tmpfs", "/tmp:rw,size=256m",
		"-v", dir + ":/work:rw,Z", "-w", "/work"}
	if !(job.Network && r.AllowNetwork) {
		args = append(args, "--network=none")
	}
	args = append(args, job.Image, "sh", "-c", job.Run)

	out := &limitedLog{max: maxLog}
	cmd := exec.CommandContext(jctx, r.runtime(), args...)
	cmd.Env = scrubbedEnv()
	cmd.Stdout, cmd.Stderr = out, out
	err = cmd.Run()

	if jctx.Err() != nil {
		// Timeout or shutdown: make sure the container is gone.
		rm := exec.Command(r.runtime(), "rm", "-f", name)
		rm.Env = scrubbedEnv()
		_ = rm.Run()
		if ctx.Err() != nil {
			return store.CIError, out.String() + "\ninterrupted"
		}
		return store.CIFailure, out.String() + fmt.Sprintf("\ntimed out after %s", job.Duration())
	}
	var ee *exec.ExitError
	switch {
	case err == nil:
		return store.CISuccess, out.String()
	case errors.As(err, &ee):
		// 125-127 come from the runtime itself, not the job.
		if c := ee.ExitCode(); c >= 125 && c <= 127 {
			return store.CIError, out.String() + fmt.Sprintf("\ncontainer runtime failed (exit %d)", c)
		}
		return store.CIFailure, out.String() + fmt.Sprintf("\nexit status %d", ee.ExitCode())
	default:
		return store.CIError, out.String() + "\n" + err.Error()
	}
}

func scrubbedEnv() []string {
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	if x := os.Getenv("XDG_RUNTIME_DIR"); x != "" {
		env = append(env, "XDG_RUNTIME_DIR="+x)
	}
	return env
}

// limitedLog keeps the first max bytes and notes truncation.
type limitedLog struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (l *limitedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if room := l.max - l.buf.Len(); room > 0 {
		l.buf.Write(p[:min(room, len(p))])
	}
	if l.buf.Len() >= l.max {
		l.truncated = true
	}
	return len(p), nil
}

func (l *limitedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := strings.ToValidUTF8(l.buf.String(), "?")
	if l.truncated {
		s += "\n[log truncated]"
	}
	return s
}
