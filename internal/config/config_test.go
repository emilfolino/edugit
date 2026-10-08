package config

import (
	"io"
	"log/slog"
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
