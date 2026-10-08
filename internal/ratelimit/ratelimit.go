// Package ratelimit throttles repeated failures, such as bad access tokens,
// per client address.
package ratelimit

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Failures blocks a key once it has failed Max times within Window. The block
// lasts until the oldest counted failure leaves the window.
type Failures struct {
	Max    int
	Window time.Duration

	// Now is time.Now unless a test replaces it.
	Now func() time.Time

	mu   sync.Mutex
	seen map[string][]time.Time
}

func (f *Failures) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

// prune drops expired failures of key and returns the rest. f.mu must be held.
func (f *Failures) prune(key string) []time.Time {
	cut := f.now().Add(-f.Window)
	ts := f.seen[key]
	i := 0
	for i < len(ts) && !ts[i].After(cut) {
		i++
	}
	ts = ts[i:]
	if len(ts) == 0 {
		delete(f.seen, key)
	} else {
		f.seen[key] = ts
	}
	return ts
}

// Blocked reports whether key has used up its failures.
func (f *Failures) Blocked(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.prune(key)) >= f.Max
}

// Fail records a failure of key.
func (f *Failures) Fail(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.seen == nil {
		f.seen = map[string][]time.Time{}
	}
	ts := f.prune(key)
	if len(ts) >= f.Max {
		return // already blocked; do not extend the block or grow memory
	}
	f.seen[key] = append(ts, f.now())
	if len(f.seen) > 10000 { // bound memory under a wide address sweep
		for k := range f.seen {
			if f.prune(k); len(f.seen) <= 5000 {
				break
			}
		}
	}
}

// ClientIP returns the address of the peer. With trustProxy it uses the last
// X-Forwarded-For entry, the one the nearest (trusted) proxy appended.
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
