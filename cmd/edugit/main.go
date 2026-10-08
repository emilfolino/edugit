// Command edugit runs the edugit server.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/emilfolino/edugit/internal/config"
	"github.com/emilfolino/edugit/internal/hooks"
	"github.com/emilfolino/edugit/internal/store"
	"github.com/emilfolino/edugit/internal/web"
)

func main() {
	// Git hooks re-enter the binary as "edugit hook <name>".
	if len(os.Args) == 3 && os.Args[1] == "hook" {
		os.Exit(hooks.Run(context.Background(), os.Args[2], os.Stdin, os.Stderr, os.Getenv))
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "edugit:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.FromOS()
	if err != nil {
		return err
	}
	log := cfg.NewLogger(os.Stderr)

	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(ctx, filepath.Join(cfg.DataDir, "edugit.db"))
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer db.Close()

	ui, err := web.New(log)
	if err != nil {
		return fmt.Errorf("init web: %w", err)
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           ui.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Addr, "data_dir", cfg.DataDir)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Info("shutting down")
		shutCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
	}
	return nil
}
