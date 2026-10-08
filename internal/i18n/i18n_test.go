package i18n

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCatalogsMatch(t *testing.T) {
	for k := range en {
		if _, ok := sv[k]; !ok {
			t.Errorf("sv is missing %q", k)
		}
	}
	for k := range sv {
		if _, ok := en[k]; !ok {
			t.Errorf("en is missing %q", k)
		}
	}
}

func TestFromRequest(t *testing.T) {
	tests := []struct {
		accept, cookie string
		want           Lang
	}{
		{"", "", English},
		{"sv-SE,sv;q=0.9,en;q=0.8", "", Swedish},
		{"de,fr;q=0.8", "", English},
		{"sv", "en", English},
		{"en", "sv", Swedish},
		{"", "xx", English},
	}
	for _, tt := range tests {
		r := httptest.NewRequest("GET", "/", nil)
		if tt.accept != "" {
			r.Header.Set("Accept-Language", tt.accept)
		}
		if tt.cookie != "" {
			r.AddCookie(&http.Cookie{Name: Cookie, Value: tt.cookie})
		}
		if got := FromRequest(r); got != tt.want {
			t.Errorf("accept=%q cookie=%q: got %s, want %s", tt.accept, tt.cookie, got, tt.want)
		}
	}
}

func TestT(t *testing.T) {
	if got := Swedish.T("nav.signin"); got != "logga in" {
		t.Errorf("sv = %q", got)
	}
	if got := Swedish.T("missing.key"); got != "missing.key" {
		t.Errorf("fallback = %q", got)
	}
}

func TestValue(t *testing.T) {
	if got := Swedish.Value("role", "teacher"); got != "lärare" {
		t.Errorf("sv = %q", got)
	}
	if got := Swedish.Value("role", "unknown"); got != "unknown" {
		t.Errorf("fallback = %q", got)
	}
}
