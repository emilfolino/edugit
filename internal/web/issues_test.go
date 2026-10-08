package web

import (
	"slices"
	"testing"
)

func TestReferencedIssues(t *testing.T) {
	got := referencedIssues("Fixes #3 and closes #4.\nresolved #3, see also #9, prefix#5 fixing #6")
	if want := []int{3, 4}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := referencedIssues("nothing here"); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}
