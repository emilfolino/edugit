// Package config loads edugit's runtime configuration from flags and
// environment variables. Flags take precedence over the environment.
package config

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
)

// Config holds the runtime configuration of the server.
type Config struct {
	// Addr is the TCP address the HTTP server listens on.
	Addr string
	// DataDir is the root directory for the database and bare repositories.
	DataDir string
	// LogLevel is the minimum slog level.
	LogLevel slog.Level
	// ShutdownTimeout bounds graceful shutdown.
	ShutdownTimeout time.Duration

	// PublicURL is the externally visible base URL (SAML endpoints derive
	// from it). Empty disables SAML.
	PublicURL string
	// IDPMetadata is an https URL or file path of the IdP's SAML metadata.
	IDPMetadata string
	// StaffDomain and StudentDomain are the exact email domains of staff and
	// students; logins from other domains are refused when either is set.
	StaffDomain, StudentDomain string
	// AdminEmails are emails promoted to global admin on login.
	AdminEmails []string
}

// Load parses args (without the program name) and env into a Config.
// The getenv function is typically os.Getenv.
func Load(args []string, getenv func(string) string, errOut io.Writer) (Config, error) {
	c := Config{
		Addr:            envOr(getenv, "EDUGIT_ADDR", ":8080"),
		DataDir:         envOr(getenv, "EDUGIT_DATA_DIR", "./data"),
		ShutdownTimeout: 15 * time.Second,
	}
	level := envOr(getenv, "EDUGIT_LOG_LEVEL", "info")

	fs := flag.NewFlagSet("edugit", flag.ContinueOnError)
	fs.SetOutput(errOut)
	fs.StringVar(&c.Addr, "addr", c.Addr, "HTTP listen address (env EDUGIT_ADDR)")
	fs.StringVar(&c.DataDir, "data-dir", c.DataDir, "data directory (env EDUGIT_DATA_DIR)")
	fs.StringVar(&level, "log-level", level, "log level: debug, info, warn, error (env EDUGIT_LOG_LEVEL)")
	fs.StringVar(&c.PublicURL, "public-url", envOr(getenv, "EDUGIT_PUBLIC_URL", ""), "public base URL, enables SAML (env EDUGIT_PUBLIC_URL)")
	fs.StringVar(&c.IDPMetadata, "saml-idp-metadata", envOr(getenv, "EDUGIT_SAML_IDP_METADATA", ""), "IdP metadata https URL or file (env EDUGIT_SAML_IDP_METADATA)")
	fs.StringVar(&c.StaffDomain, "staff-domain", envOr(getenv, "EDUGIT_STAFF_DOMAIN", ""), "staff email domain (env EDUGIT_STAFF_DOMAIN)")
	fs.StringVar(&c.StudentDomain, "student-domain", envOr(getenv, "EDUGIT_STUDENT_DOMAIN", ""), "student email domain (env EDUGIT_STUDENT_DOMAIN)")
	admins := envOr(getenv, "EDUGIT_ADMIN_EMAILS", "")
	fs.StringVar(&admins, "admin-emails", admins, "comma-separated global admin emails (env EDUGIT_ADMIN_EMAILS)")
	fs.DurationVar(&c.ShutdownTimeout, "shutdown-timeout", c.ShutdownTimeout, "graceful shutdown timeout")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

	for _, e := range strings.Split(admins, ",") {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			c.AdminEmails = append(c.AdminEmails, e)
		}
	}
	if (c.PublicURL == "") != (c.IDPMetadata == "") {
		return Config{}, fmt.Errorf("public-url and saml-idp-metadata must be set together")
	}
	if err := c.LogLevel.UnmarshalText([]byte(level)); err != nil {
		return Config{}, fmt.Errorf("invalid log level %q: %w", level, err)
	}
	return c, nil
}

// NewLogger returns a structured logger writing to w at the configured level.
func (c Config) NewLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: c.LogLevel}))
}

func envOr(getenv func(string) string, key, def string) string {
	if v := getenv(key); v != "" {
		return v
	}
	return def
}

// FromOS is a convenience wrapper that loads configuration from os.Args and
// the process environment.
func FromOS() (Config, error) {
	return Load(os.Args[1:], os.Getenv, os.Stderr)
}
