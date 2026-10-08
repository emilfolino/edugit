package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler(t *testing.T) {
	s, err := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()

	tests := []struct {
		path     string
		status   int
		contains string
	}{
		{"/", http.StatusOK, "edugit"},
		{"/healthz", http.StatusOK, "ok"},
		{"/static/style.css", http.StatusOK, "--ink"},
		{"/nope", http.StatusNotFound, ""},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("GET", tt.path, nil))
			if rec.Code != tt.status {
				t.Fatalf("status: got %d, want %d", rec.Code, tt.status)
			}
			if !strings.Contains(rec.Body.String(), tt.contains) {
				t.Errorf("body missing %q", tt.contains)
			}
		})
	}
}
