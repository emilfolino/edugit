package hooks

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// Policy decides whether a push may proceed. It returns the reasons to
// reject, or none to accept. Errors fail the push closed.
type Policy interface {
	CheckPush(ctx context.Context, req Request) (reject []string, err error)
}

// Sink receives accepted pushes after they have been applied. It drives PR
// updates and site publishing. Errors are logged, never shown to the pusher.
type Sink interface {
	Pushed(ctx context.Context, req Request) error
}

// Bridge is the server side of the hook protocol. It is safe for concurrent
// use.
type Bridge struct {
	// Secret authenticates hook processes; generate it with crypto/rand at
	// startup and pass it to hooks via EnvToken.
	Secret string
	Policy Policy
	Sink   Sink
	Log    *slog.Logger
}

// maxBody bounds a hook request; a push touching many refs is still small.
const maxBody = 4 << 20

// Handler returns the HTTP handler for the hook endpoints.
func (b *Bridge) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /"+PreReceive, b.handle(true))
	mux.HandleFunc("POST /"+PostReceive, b.handle(false))
	return mux
}

func (b *Bridge) handle(pre bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if b.Secret == "" || subtle.ConstantTimeCompare([]byte(got), []byte(b.Secret)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req Request
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var resp Response
		if pre {
			reject, err := b.Policy.CheckPush(r.Context(), req)
			if err != nil {
				b.Log.Error("hook policy", "course", req.Course, "repo", req.Repo, "err", err)
				resp.Reject = []string{"internal error; push refused"}
			} else {
				resp.Reject = reject
			}
		} else if b.Sink != nil {
			if err := b.Sink.Pushed(r.Context(), req); err != nil {
				b.Log.Error("hook sink", "course", req.Course, "repo", req.Repo, "err", err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// Serve listens on a unix socket at path until ctx is cancelled. The socket is
// mode 0600 so only the server's user can reach it.
func (b *Bridge) Serve(ctx context.Context, path string) error {
	if len(path) >= 100 { // sun_path is ~108 bytes on Linux
		return fmt.Errorf("hook socket path too long (%d bytes): %s", len(path), path)
	}
	_ = os.Remove(path) // stale socket from a previous run
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return err
	}
	srv := &http.Server{Handler: b.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
