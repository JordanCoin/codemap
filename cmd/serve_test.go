package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteJSON_WritesIndentedJSONAndContentType(t *testing.T) {
	tests := []struct {
		name    string
		payload map[string]interface{}
	}{
		{
			name: "simple map",
			payload: map[string]interface{}{
				"status": "ok",
				"count":  2,
			},
		},
		{
			name: "nested payload",
			payload: map[string]interface{}{
				"outer": map[string]interface{}{"inner": true},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			writeJSON(rr, tt.payload)

			if got := rr.Header().Get("Content-Type"); got != "application/json" {
				t.Fatalf("expected content-type application/json, got %q", got)
			}
			if rr.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", rr.Code)
			}
			if !strings.Contains(rr.Body.String(), "\n  ") {
				t.Fatalf("expected indented JSON output, got %q", rr.Body.String())
			}

			var decoded map[string]interface{}
			if err := json.Unmarshal(rr.Body.Bytes(), &decoded); err != nil {
				t.Fatalf("expected valid JSON body: %v", err)
			}
		})
	}
}

func TestWriteError_WritesStatusJSONAndContentType(t *testing.T) {
	tests := []struct {
		name       string
		code       int
		msg        string
		wantCode   int
		wantSubstr string
	}{
		{name: "bad request", code: http.StatusBadRequest, msg: "bad input", wantCode: http.StatusBadRequest, wantSubstr: "bad input"},
		{name: "internal server error", code: http.StatusInternalServerError, msg: "boom", wantCode: http.StatusInternalServerError, wantSubstr: "boom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			writeError(rr, tt.code, tt.msg)

			if rr.Code != tt.wantCode {
				t.Fatalf("expected status %d, got %d", tt.wantCode, rr.Code)
			}
			if got := rr.Header().Get("Content-Type"); got != "application/json" {
				t.Fatalf("expected content-type application/json, got %q", got)
			}

			var decoded map[string]string
			if err := json.Unmarshal(rr.Body.Bytes(), &decoded); err != nil {
				t.Fatalf("expected valid JSON body: %v", err)
			}
			if decoded["error"] != tt.msg {
				t.Fatalf("expected error message %q, got %q", tt.msg, decoded["error"])
			}
			if !strings.Contains(rr.Body.String(), tt.wantSubstr) {
				t.Fatalf("expected body to contain %q, got %q", tt.wantSubstr, rr.Body.String())
			}
		})
	}
}
