package ci

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emilfolino/edugit/internal/gitserver"
	"github.com/emilfolino/edugit/internal/hooks"
	"github.com/emilfolino/edugit/internal/store"
)

func TestParse(t *testing.T) {
	good := `{"jobs":[{"name":"test","image":"node:22-alpine","run":"npm test","timeout":"2m"}]}`
	c, err := Parse([]byte(good))
	if err != nil || c.Jobs[0].Duration() != 2*time.Minute {
		t.Fatalf("good config: %v %+v", err, c)
	}
	if d := (Job{Timeout: "10h"}).Duration(); d != maxTimeout {
		t.Errorf("uncapped timeout %v", d)
	}
	for name, bad := range map[string]string{
		"empty":    `{"jobs":[]}`,
		"option":   `{"jobs":[{"name":"a","image":"--privileged","run":"x"}]}`,
		"no run":   `{"jobs":[{"name":"a","image":"x"}]}`,
		"dup":      `{"jobs":[{"name":"a","image":"x","run":"y"},{"name":"a","image":"x","run":"y"}]}`,
		"bad time": `{"jobs":[{"name":"a","image":"x","run":"y","timeout":"soon"}]}`,
		"bad name": `{"jobs":[{"name":"-a","image":"x","run":"y"}]}`,
		"not json": `jobs: []`,
		"too many": `{"jobs":[` + strings.Repeat(`{"name":"a","image":"x","run":"y"},`, 6) + `{"name":"b","image":"x","run":"y"}]}`,
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

type fakeStore struct {
	mu   sync.Mutex
	runs map[int64]*store.CIRun
	next int64
}

func (f *fakeStore) CreateCIRun(_ context.Context, repoID int64, sha, branch, job, spec string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	if f.runs == nil {
		f.runs = map[int64]*store.CIRun{}
	}
	f.runs[f.next] = &store.CIRun{ID: f.next, RepoID: repoID, SHA: sha, Branch: branch, Job: job, Spec: spec, Status: store.CIQueued}
	return f.next, nil
}
func (f *fakeStore) StartCIRun(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs[id].Status = store.CIRunning
	return nil
}
func (f *fakeStore) FinishCIRun(_ context.Context, id int64, status, log string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs[id].Status, f.runs[id].Log = status, log
	return nil
}
func (f *fakeStore) FailInterruptedCIRuns(context.Context) (int64, error) { return 0, nil }
func (f *fakeStore) QueuedCIRuns(context.Context) ([]store.CIRun, error)  { return nil, nil }
func (f *fakeStore) RepoByName(_ context.Context, _, name string, _ int64) (store.Repo, bool, error) {
	return store.Repo{ID: 7, Name: name}, false, nil
}
func (f *fakeStore) get(id int64) store.CIRun {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.runs[id]; ok {
		return *r
	}
	return store.CIRun{}
}

type fakeGit struct{ config string }

func (g fakeGit) File(_ context.Context, _, _, _, path string) (gitserver.Blob, error) {
	if g.config == "" || path != ConfigPath {
		return gitserver.Blob{}, gitserver.ErrNoPath
	}
	return gitserver.Blob{Content: g.config}, nil
}
func (g fakeGit) Export(_ context.Context, _, _, _, _, dest string) error {
	return os.WriteFile(filepath.Join(dest, "f.txt"), []byte("x"), 0o600)
}

// fakeRuntime stands in for podman: it prints its arguments and exits 3 or
// sleeps depending on the job's command (the last argument).
func fakeRuntime(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "podman")
	script := `#!/bin/sh
[ "$1" = rm ] && exit 0
echo "ARGS: $*"
for last; do :; done
case "$last" in
  fail) exit 3 ;;
  hang) exec sleep 30 ;;
  broken) exit 125 ;;
esac
`
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func waitDone(t *testing.T, f *fakeStore, id int64) store.CIRun {
	t.Helper()
	for range 200 {
		if r := f.get(id); r.Done() {
			return r
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("run %d did not finish", id)
	return store.CIRun{}
}

func TestRunner(t *testing.T) {
	cfg := `{"jobs":[
		{"name":"ok","image":"img","run":"pass"},
		{"name":"bad","image":"img","run":"fail"},
		{"name":"hang","image":"img","run":"hang","timeout":"1s"},
		{"name":"broken","image":"img","run":"broken"},
		{"name":"net","image":"img","run":"pass","network":true}]}`
	f := &fakeStore{}
	r := &Runner{Store: f, Git: fakeGit{config: cfg}, Runtime: fakeRuntime(t)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	req := hooks.Request{Course: "c", Repo: "demo", Updates: []hooks.Update{
		{Ref: "refs/heads/main", New: "abc123"},
		{Ref: "refs/tags/v1", New: "abc123"},
		{Ref: "refs/heads/gone", New: "0000000000000000000000000000000000000000"},
	}}
	if err := r.Pushed(ctx, req); err != nil {
		t.Fatal(err)
	}
	if len(f.runs) != 5 {
		t.Fatalf("queued %d runs, want 5 (tags and deletes are ignored)", len(f.runs))
	}
	want := map[string]string{"ok": store.CISuccess, "bad": store.CIFailure, "hang": store.CIFailure, "broken": store.CIError, "net": store.CISuccess}
	for id := int64(1); id <= 5; id++ {
		got := waitDone(t, f, id)
		if got.Status != want[got.Job] {
			t.Errorf("%s: status %s, want %s\n%s", got.Job, got.Status, want[got.Job], got.Log)
		}
		if got.Job == "ok" && !strings.Contains(got.Log, "--network=none") {
			t.Errorf("network not disabled: %s", got.Log)
		}
		if got.Job == "net" && !strings.Contains(got.Log, "--network=none") {
			t.Errorf("network granted although AllowNetwork is off: %s", got.Log)
		}
		if got.Job == "hang" && !strings.Contains(got.Log, "timed out") {
			t.Errorf("hang log: %s", got.Log)
		}
	}
}

func TestNoConfigAndBadConfig(t *testing.T) {
	f := &fakeStore{}
	ctx := context.Background()
	req := hooks.Request{Course: "c", Repo: "demo", Updates: []hooks.Update{{Ref: "refs/heads/main", New: "abc"}}}
	if err := (&Runner{Store: f, Git: fakeGit{}}).Pushed(ctx, req); err != nil || len(f.runs) != 0 {
		t.Fatalf("no config: %v, %d runs", err, len(f.runs))
	}
	if err := (&Runner{Store: f, Git: fakeGit{config: "nope"}}).Pushed(ctx, req); err != nil {
		t.Fatal(err)
	}
	if r := f.get(1); r.Status != store.CIError || r.Job != "config" {
		t.Errorf("bad config run = %+v", r)
	}
}

func TestLimitedLog(t *testing.T) {
	l := &limitedLog{max: 5}
	l.Write([]byte("abcdefgh"))
	if s := l.String(); !strings.HasPrefix(s, "abcde") || !strings.Contains(s, "truncated") {
		t.Errorf("log = %q", s)
	}
}
