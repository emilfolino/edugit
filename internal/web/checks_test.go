package web

import (
	"testing"

	"github.com/emilfolino/edugit/internal/store"
)

func TestChecksVerdict(t *testing.T) {
	run := func(job, status string) store.CIRun { return store.CIRun{Job: job, Status: status} }
	tests := []struct {
		name string
		runs []store.CIRun
		ok   bool
	}{
		{"no runs fails closed", nil, false},
		{"all success", []store.CIRun{run("test", "success"), run("lint", "success")}, true},
		{"one failure", []store.CIRun{run("test", "success"), run("lint", "failure")}, false},
		{"runner error", []store.CIRun{run("test", "error")}, false},
		{"still running", []store.CIRun{run("test", "success"), run("lint", "running")}, false},
		{"queued", []store.CIRun{run("test", "queued")}, false},
		{"rerun supersedes failure", []store.CIRun{run("test", "failure"), run("test", "success")}, true},
		{"rerun can regress", []store.CIRun{run("test", "success"), run("test", "failure")}, false},
	}
	for _, tt := range tests {
		if got := checksVerdict(tt.runs); (got == "") != tt.ok {
			t.Errorf("%s: verdict %q", tt.name, got)
		}
	}
}
