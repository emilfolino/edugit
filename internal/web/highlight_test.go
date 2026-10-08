package web

import (
	"strings"
	"testing"
)

func TestHighlight(t *testing.T) {
	got := highlight("a.go", "func main() { // hi <b>\n\tx := \"s\\\"q\" + 42\n}\n/* open\nstill */ var")
	if len(got) != 5 {
		t.Fatalf("lines = %d", len(got))
	}
	for i, want := range []string{
		`<span class="tok-k">func</span> main() { <span class="tok-c">// hi &lt;b&gt;</span>`,
		`<span class="tok-s">&#34;s\&#34;q&#34;</span> + <span class="tok-n">42</span>`,
		`<span class="tok-c">still */</span> <span class="tok-k">var</span>`,
	} {
		idx := []int{0, 1, 4}[i]
		if !strings.Contains(string(got[idx]), want) {
			t.Errorf("line %d = %s, want %s", idx, got[idx], want)
		}
	}
	if got[3] != `<span class="tok-c">/*</span><span class="tok-c"> open</span>` && !strings.Contains(string(got[3]), "tok-c") {
		t.Errorf("block start = %s", got[3])
	}
	if h := highlight("x.txt", "<script>")[0]; h != "&lt;script&gt;" {
		t.Errorf("plain = %s", h)
	}
}
