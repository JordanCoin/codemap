package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteJSON(t *testing.T) {
	tests := []struct {
		name       string
		payload    interface{}
		wantHeader string
	}{
		{
			name:       "object payload",
			payload:    map[string]string{"status": "ok"},
			wantHeader: "application/json",
		},
		{
			name:       "array payload",
			payload:    []string{"a", "b"},
			wantHeader: "application/json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeJSON(rec, tt.payload)

			if got := rec.Header().Get("Content-Type"); got != tt.wantHeader {
				t.Fatalf("Content-Type = %q, want %q", got, tt.wantHeader)
			}

			var decoded interface{}
			if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
				t.Fatalf("response body is not valid JSON: %v", err)
			}

			if !strings.Contains(rec.Body.String(), "\n") {
				t.Fatalf("expected indented JSON with newlines, got %q", rec.Body.String())
			}
		})
	}
}

func TestWriteError(t *testing.T) {
	tests := []struct {
		name     string
		code     int
		msg      string
		wantCode int
	}{
		{name: "bad request", code: http.StatusBadRequest, msg: "bad input", wantCode: http.StatusBadRequest},
		{name: "not found", code: http.StatusNotFound, msg: "missing", wantCode: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeError(rec, tt.code, tt.msg)

			if rec.Code != tt.wantCode {
				t.Fatalf("status code = %d, want %d", rec.Code, tt.wantCode)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/json" {
				t.Fatalf("Content-Type = %q, want application/json", got)
			}

			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("response body is not valid JSON: %v", err)
			}
			if body["error"] != tt.msg {
				t.Fatalf("error message = %q, want %q", body["error"], tt.msg)
			}
		})
	}
}
