// Command edugit runs the edugit server.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/emilfolino/edugit/internal/auth"
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

	root := http.NewServeMux()
	root.Handle("/", ui.Handler())
	if cfg.PublicURL != "" {
		sp, err := newSAML(ctx, cfg, db, log)
		if err != nil {
			return err
		}
		sp.Routes(root)
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           root,
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

// newSAML builds the SAML service provider from cfg.
func newSAML(ctx context.Context, cfg config.Config, db *store.Store, log *slog.Logger) (*auth.SAML, error) {
	pub, err := url.Parse(cfg.PublicURL)
	if err != nil || (pub.Scheme != "https" && pub.Scheme != "http") || pub.Host == "" {
		return nil, fmt.Errorf("invalid public url %q", cfg.PublicURL)
	}
	meta, err := auth.LoadIDPMetadata(ctx, cfg.IDPMetadata)
	if err != nil {
		return nil, err
	}
	key, crt, err := auth.LoadOrCreateKeypair(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("saml keypair: %w", err)
	}
	return auth.NewSAML(auth.SAMLConfig{
		PublicURL:   pub,
		IDPMetadata: meta,
		Key:         key,
		Cert:        crt,
		Domains:     auth.Domains{Staff: cfg.StaffDomain, Student: cfg.StudentDomain},
		Log:         log,
		OnLogin: func(w http.ResponseWriter, r *http.Request, id auth.Identity, returnTo string) {
			u, err := db.LoginUser(r.Context(), id.Subject, id.Email, id.DisplayName, slices.Contains(cfg.AdminEmails, id.Email))
			if err != nil {
				log.Error("login user", "err", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			log.Info("login", "user", u.Username, "admin", u.IsAdmin)
			// TODO(#6): issue a session cookie and redirect to returnTo.
			http.Error(w, "signed in as "+u.Username+"; sessions are not implemented yet", http.StatusNotImplemented)
		},
	})
}
