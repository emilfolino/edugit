package ratelimit

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestFailures(t *testing.T) {
	now := time.Unix(1000, 0)
	f := &Failures{Max: 3, Window: time.Minute, Now: func() time.Time { return now }}
	for i := 0; i < 3; i++ {
		if f.Blocked("a") {
			t.Fatalf("blocked after %d failures", i)
		}
		f.Fail("a")
	}
	if !f.Blocked("a") {
		t.Error("not blocked after Max failures")
	}
	if f.Blocked("b") {
		t.Error("other key blocked")
	}
	now = now.Add(time.Minute + time.Second)
	if f.Blocked("a") {
		t.Error("still blocked after the window")
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:5555"
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.9")
	if got := ClientIP(r, false); got != "10.0.0.1" {
		t.Errorf("direct = %q", got)
	}
	if got := ClientIP(r, true); got != "203.0.113.9" {
		t.Errorf("proxied = %q", got)
	}
}
