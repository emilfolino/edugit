package web

import "testing"

const sampleDiff = `diff --git a/a.txt b/a.txt
index 1..2 100644
--- a/a.txt
+++ b/a.txt
@@ -1,3 +1,3 @@
 one
-two
-- three
+TWO
+more
 end
diff --git a/new.txt b/new.txt
new file mode 100644
--- /dev/null
+++ b/new.txt
@@ -0,0 +1 @@
+hi
diff --git a/old.txt b/old.txt
deleted file mode 100644
--- a/old.txt
+++ /dev/null
@@ -1 +0,0 @@
-bye
diff --git a/img.png b/img.png
new file mode 100644
Binary files /dev/null and b/img.png differ
`

func TestParseDiff(t *testing.T) {
	files := parseDiff(sampleDiff)
	if len(files) != 4 {
		t.Fatalf("got %d files: %+v", len(files), files)
	}
	tests := []struct {
		path, status   string
		added, removed int
		hunks          int
		binary         bool
	}{
		{"a.txt", "modified", 2, 2, 1, false},
		{"new.txt", "added", 1, 0, 1, false},
		{"old.txt", "deleted", 0, 1, 1, false},
		{"img.png", "added", 0, 0, 0, true},
	}
	for i, tt := range tests {
		f := files[i]
		if f.Path != tt.path || f.Status != tt.status || f.Added != tt.added || f.Removed != tt.removed ||
			len(f.Hunks) != tt.hunks || f.Binary != tt.binary {
			t.Errorf("file %d: got %+v, want %+v", i, f, tt)
		}
	}
	h := files[0].Hunks[0]
	if h.Lines[2].Text != "- three" || h.Lines[2].Old != 3 || h.Lines[4].New != 3 {
		t.Errorf("line numbering: %+v", h.Lines)
	}
	// context, then 2 removed paired with 2 added, then context.
	if len(h.Rows) != 4 || h.Rows[1].Left.Text != "two" || h.Rows[1].Right.Text != "TWO" || h.Rows[2].Right.Text != "more" {
		t.Errorf("rows: %+v", h.Rows)
	}
}
