package config

import (
	"io"
	"log/slog"
	"slices"
	"testing"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		env     map[string]string
		addr    string
		level   slog.Level
		wantErr bool
	}{
		{name: "defaults", addr: ":8080", level: slog.LevelInfo},
		{name: "env", env: map[string]string{"EDUGIT_ADDR": ":9000"}, addr: ":9000", level: slog.LevelInfo},
		{name: "flag beats env", args: []string{"-addr", ":7000"}, env: map[string]string{"EDUGIT_ADDR": ":9000"}, addr: ":7000", level: slog.LevelInfo},
		{name: "debug level", args: []string{"-log-level", "debug"}, addr: ":8080", level: slog.LevelDebug},
		{name: "bad level", args: []string{"-log-level", "loud"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(k string) string { return tt.env[k] }
			got, err := Load(tt.args, getenv, io.Discard)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got.Addr != tt.addr {
				t.Errorf("Addr: got %q, want %q", got.Addr, tt.addr)
			}
			if got.LogLevel != tt.level {
				t.Errorf("LogLevel: got %v, want %v", got.LogLevel, tt.level)
			}
		})
	}
}

func TestLoad_saml(t *testing.T) {
	env := map[string]string{
		"EDUGIT_PUBLIC_URL":        "https://git.example.edu",
		"EDUGIT_SAML_IDP_METADATA": "https://idp.example/metadata",
		"EDUGIT_ADMIN_EMAILS":      " EFO@bth.se, ,b@bth.se",
	}
	got, err := Load(nil, func(k string) string { return env[k] }, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"efo@bth.se", "b@bth.se"}; !slices.Equal(got.AdminEmails, want) {
		t.Errorf("AdminEmails: got %q, want %q", got.AdminEmails, want)
	}

	delete(env, "EDUGIT_SAML_IDP_METADATA")
	if _, err := Load(nil, func(k string) string { return env[k] }, io.Discard); err == nil {
		t.Error("public url without idp metadata: got nil error, want error")
	}
}
