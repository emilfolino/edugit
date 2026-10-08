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
	"strings"
	"syscall"
	"time"

	"github.com/emilfolino/edugit/internal/auth"
	"github.com/emilfolino/edugit/internal/authz"
	"github.com/emilfolino/edugit/internal/config"
	"github.com/emilfolino/edugit/internal/gitserver"
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

	opts := web.Options{}
	var sessions *auth.Sessions
	var authorizer *authz.Authorizer
	var repos *gitserver.Repos
	if cfg.PublicURL != "" || cfg.DevLogin {
		sessions = &auth.Sessions{Backend: db, Secure: strings.HasPrefix(cfg.PublicURL, "https://")}
		loginURL := "/saml/login"
		if cfg.DevLogin {
			loginURL = "/dev/login"
		}
		authorizer = &authz.Authorizer{Source: db}
		if repos, err = newRepos(cfg.DataDir); err != nil {
			return err
		}
		opts = web.Options{
			Sessions:    sessions,
			Tokens:      db,
			Courses:     db,
			Authz:       authorizer,
			Repos:       db,
			Disk:        repos,
			Assignments: db,
			Pulls:       db,
			PullGit:     repos,
			Issues:      db,
			Browse:      repos,
			Audit:       db,
			Domains:     auth.Domains{Staff: cfg.StaffDomain, Student: cfg.StudentDomain},
			PublicURL:   strings.TrimSuffix(cfg.PublicURL, "/"),
			LoginURL:    loginURL,
		}
		go purgeSessions(ctx, db, log)
		go syncLocks(ctx, db, log)
	}
	ui, err := web.New(log, opts)
	if err != nil {
		return fmt.Errorf("init web: %w", err)
	}

	root := http.NewServeMux()
	root.Handle("/", ui.Handler())
	if sessions != nil {
		if err := setupGit(ctx, repos, db, sessions, authorizer, root, log); err != nil {
			return err
		}
	}
	if cfg.DevLogin {
		log.Warn("dev-login enabled: anyone who can reach this address can sign in as any user")
		root.Handle("/dev/login", devLogin(db, sessions, cfg.AdminEmails, log))
	}
	if cfg.PublicURL != "" {
		sp, err := newSAML(ctx, cfg, db, sessions, log)
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
func newSAML(ctx context.Context, cfg config.Config, db *store.Store, sessions *auth.Sessions, log *slog.Logger) (*auth.SAML, error) {
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
			if err := sessions.Start(w, r, u.ID); err != nil {
				log.Error("start session", "err", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if returnTo == "" {
				returnTo = "/"
			}
			http.Redirect(w, r, returnTo, http.StatusSeeOther)
		},
	})
}

// purgeSessions deletes expired sessions hourly until ctx is done.
func purgeSessions(ctx context.Context, db *store.Store, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := db.PurgeSessions(ctx, time.Now()); err != nil {
				log.Error("purge sessions", "err", err)
			}
		}
	}
}

// syncLocks applies assignment deadlines to student repos every minute until
// ctx is done.
func syncLocks(ctx context.Context, db *store.Store, log *slog.Logger) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		locked, reopened, err := db.SyncLocks(ctx, time.Now())
		switch {
		case err != nil:
			log.Error("sync locks", "err", err)
		case locked+reopened > 0:
			log.Info("sync locks", "locked", locked, "reopened", reopened)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
