package gitserver

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestExport(t *testing.T) {
	ctx := context.Background()
	r, _ := pullFixture(t)
	by := Identity{Name: "Ada", Email: "ada@example.com"}
	tip, _ := r.Resolve(ctx, "c1", "demo", "main")
	if _, err := r.CommitFiles(ctx, "c1", "demo", "main", "", tip, []FileChange{
		{Path: "public/index.html", Content: []byte("hi")},
		{Path: "public/css/a.css", Content: []byte("b{}")},
	}, by, "site"); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if err := r.Export(ctx, "c1", "demo", "main", "public", dest); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]string{"index.html": "hi", "css/a.css": "b{}"} {
		if got, err := os.ReadFile(filepath.Join(dest, p)); err != nil || string(got) != want {
			t.Errorf("%s = %q, %v", p, got, err)
		}
	}
	if err := r.Export(ctx, "c1", "demo", "nope", "", t.TempDir()); err == nil {
		t.Error("missing branch should fail")
	}
	if err := r.Export(ctx, "c1", "demo", "main", "../x", t.TempDir()); err == nil {
		t.Error("bad dir should fail")
	}
}

func TestExtractTarSafety(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
	_ = tw.WriteHeader(&tar.Header{Name: "ok.txt", Typeflag: tar.TypeReg, Size: 1, Mode: 0o600})
	_, _ = tw.Write([]byte("x"))
	_ = tw.Close()
	dest := t.TempDir()
	if err := extractTar(&buf, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dest, "link")); err == nil {
		t.Error("symlink was extracted")
	}
	if _, err := os.Stat(filepath.Join(dest, "ok.txt")); err != nil {
		t.Error(err)
	}

	buf.Reset()
	tw = tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "../evil", Typeflag: tar.TypeReg, Size: 1})
	_, _ = tw.Write([]byte("x"))
	_ = tw.Close()
	if err := extractTar(&buf, t.TempDir()); err == nil {
		t.Error("traversal accepted")
	}
}
