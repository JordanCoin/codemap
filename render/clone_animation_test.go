package render

import (
	"bytes"
	"strings"
	"testing"
)

func TestTruncate(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		max      int
		expected string
	}{
		{name: "short string unchanged", input: "repo", max: 10, expected: "repo"},
		{name: "same length unchanged", input: "abcdefghij", max: 10, expected: "abcdefghij"},
		{name: "long string truncated", input: "very-long-repository-name", max: 10, expected: "very-lon.."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.input, tt.max)
			if got != tt.expected {
				t.Fatalf("truncate(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.expected)
			}
		})
	}
}

func TestNewCloneAnimation(t *testing.T) {
	var buf bytes.Buffer
	a := NewCloneAnimation(&buf, "repo")
	if a == nil {
		t.Fatal("expected animation instance")
	}
	if a.w != &buf {
		t.Fatal("expected writer to be set")
	}
	if a.repoName != "repo" {
		t.Fatalf("expected repoName to be %q, got %q", "repo", a.repoName)
	}
}

func TestCloneAnimationRenderClampsProgress(t *testing.T) {
	tests := []struct {
		name             string
		progress         int
		expectedProgress string
	}{
		{name: "negative becomes zero", progress: -10, expectedProgress: "0%"},
		{name: "over hundred becomes hundred", progress: 120, expectedProgress: "100%"},
		{name: "middle value", progress: 45, expectedProgress: "45%"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			a := NewCloneAnimation(&buf, "example/repo")
			a.Render(tt.progress)

			out := buf.String()
			if !strings.Contains(out, "\r\033[K") {
				t.Fatalf("expected cursor clear sequence, got %q", out)
			}
			if !strings.Contains(out, tt.expectedProgress) {
				t.Fatalf("expected output to contain %q, got %q", tt.expectedProgress, out)
			}
		})
	}
}

func TestCloneAnimationBuildFrame(t *testing.T) {
	a := NewCloneAnimation(&bytes.Buffer{}, "very-very-long-repository-name-that-needs-truncation")
	frame := a.buildFrame(50)

	checks := []string{"50%", "..", "🗺️"}
	for _, check := range checks {
		if !strings.Contains(frame, check) {
			t.Fatalf("expected frame to contain %q, got %q", check, frame)
		}
	}
}
