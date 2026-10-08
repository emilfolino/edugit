package gitserver

import (
	"context"
	"log/slog"
	"time"
)

// RunGC runs "git gc --auto" over every repository every interval until ctx
// is cancelled. Git decides per repository whether collection is needed, so
// each pass is cheap for quiet repositories.
func (r *Repos) RunGC(ctx context.Context, interval time.Duration, log *slog.Logger) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			repos, err := r.List()
			if err != nil {
				log.Error("gc: list repositories", "err", err)
				continue
			}
			for _, rp := range repos {
				if ctx.Err() != nil {
					return
				}
				if err := r.GC(ctx, rp[0], rp[1]); err != nil {
					log.Warn("gc failed", "course", rp[0], "repo", rp[1], "err", err)
				}
			}
		}
	}
}
