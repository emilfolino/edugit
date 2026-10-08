package web

import "testing"

func TestCSVCell(t *testing.T) {
	for in, want := range map[string]string{"plain": "plain", "=1+1": "'=1+1", "@x": "'@x", "-5": "'-5", "": ""} {
		if got := csvCell(in); got != want {
			t.Errorf("csvCell(%q) = %q, want %q", in, got, want)
		}
	}
}
