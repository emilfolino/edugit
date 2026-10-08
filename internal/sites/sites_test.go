package sites

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/emilfolino/edugit/internal/hooks"
	"github.com/emilfolino/edugit/internal/store"
)

type fakeGit struct {
	content string
	calls   int
}

func (f *fakeGit) Export(_ context.Context, _, _, _, _, dest string) error {
	f.calls++
	return os.WriteFile(filepath.Join(dest, "index.html"), []byte(f.content), 0o644)
}

type fakeCfg struct{ site store.Site }

func (f fakeCfg) SiteByCourse(context.Context, string) (store.Site, error) {
	if f.site.Repo == "" {
		return store.Site{}, store.ErrNotFound
	}
	return f.site, nil
}

func get(h http.Handler, path string) (int, string) {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec.Code, rec.Body.String()
}

func TestPublishSwapAndServe(t *testing.T) {
	ctx := context.Background()
	g := &fakeGit{content: "v1"}
	p := &Publisher{Root: t.TempDir(), Git: g}
	if _, err := p.Handler("oop", "/sites/oop"); err != ErrNoSite {
		t.Fatalf("before publish: %v", err)
	}
	if err := p.Publish(ctx, "oop", "material", "main", ""); err != nil {
		t.Fatal(err)
	}
	h, err := p.Handler("oop", "/sites/oop")
	if err != nil {
		t.Fatal(err)
	}
	if code, body := get(h, "/sites/oop/"); code != 200 || body != "v1" {
		t.Errorf("index = %d %q", code, body)
	}
	if code, _ := get(h, "/sites/oop/missing"); code != 404 {
		t.Errorf("missing = %d", code)
	}
	g.content = "v2"
	if err := p.Publish(ctx, "oop", "material", "main", ""); err != nil {
		t.Fatal(err)
	}
	if _, body := get(h, "/sites/oop/"); body != "v2" {
		t.Errorf("after republish = %q", body)
	}
	entries, _ := os.ReadDir(filepath.Join(p.Root, "oop"))
	if len(entries) != 2 { // current + one build dir; the old build is gone
		t.Errorf("leftovers: %v", entries)
	}
	if err := p.Publish(ctx, "../x", "r", "main", ""); err == nil {
		t.Error("bad course accepted")
	}
	if err := p.Remove("oop"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Handler("oop", "/sites/oop"); err != ErrNoSite {
		t.Errorf("after remove: %v", err)
	}
}

func TestSink(t *testing.T) {
	ctx := context.Background()
	g := &fakeGit{content: "x"}
	p := &Publisher{Root: t.TempDir(), Git: g}
	s := Sink{DB: fakeCfg{store.Site{Repo: "material", Branch: "main"}}, Pub: p}
	push := func(repo, ref, newSHA string) {
		t.Helper()
		err := s.Pushed(ctx, hooks.Request{Course: "oop", Repo: repo, Updates: []hooks.Update{{Ref: ref, Old: "a", New: newSHA}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	push("other", "refs/heads/main", "b")
	push("material", "refs/heads/topic", "b")
	if g.calls != 0 {
		t.Fatalf("published for unrelated push: %d", g.calls)
	}
	push("material", "refs/heads/main", "b")
	if g.calls != 1 {
		t.Errorf("calls = %d", g.calls)
	}
	// No configured site: nothing happens.
	s.DB = fakeCfg{}
	push("material", "refs/heads/main", "c")
	if g.calls != 1 {
		t.Errorf("published without a site: %d", g.calls)
	}
}
