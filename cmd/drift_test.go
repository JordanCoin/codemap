package cmd

import (
	"testing"

	"codemap/config"
)

func TestCheckDrift_Disabled(t *testing.T) {
	cfg := config.DriftConfig{Enabled: false, RequireDocsFor: []string{"watching"}}
	warnings := CheckDrift(".", cfg, config.RoutingConfig{})
	if len(warnings) != 0 {
		t.Errorf("expected no warnings when disabled, got %d", len(warnings))
	}
}

func TestCheckDrift_EmptyRequireDocs(t *testing.T) {
	cfg := config.DriftConfig{Enabled: true, RequireDocsFor: nil}
	warnings := CheckDrift(".", cfg, config.RoutingConfig{})
	if len(warnings) != 0 {
		t.Errorf("expected no warnings with empty require_docs_for, got %d", len(warnings))
	}
}

func TestResolveDocPaths_FromConfig(t *testing.T) {
	subsystemDocs := map[string][]string{
		"watching": {"docs/HOOKS.md"},
	}
	paths := resolveDocPaths("watching", subsystemDocs)
	if len(paths) != 1 || paths[0] != "docs/HOOKS.md" {
		t.Errorf("expected [docs/HOOKS.md], got %v", paths)
	}
}

func TestResolveDocPaths_Fallback(t *testing.T) {
	paths := resolveDocPaths("unknown", map[string][]string{})
	if len(paths) != 2 {
		t.Fatalf("expected 2 fallback paths, got %d", len(paths))
	}
	if paths[0] != "docs/unknown.md" {
		t.Errorf("expected docs/unknown.md, got %s", paths[0])
	}
}

func TestGuessCodePaths(t *testing.T) {
	tests := []struct {
		id      string
		wantAny string
	}{
		{"watching", "watch/"},
		{"scanning", "scanner/"},
		{"hooks", "cmd/hooks.go"},
		{"unknown", "unknown/"},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			paths := guessCodePaths(tt.id)
			if len(paths) == 0 {
				t.Fatal("expected at least one code path")
			}
			found := false
			for _, p := range paths {
				if p == tt.wantAny {
					found = true
				}
			}
			if !found {
				t.Errorf("expected %q in paths %v", tt.wantAny, paths)
			}
		})
	}
}

func TestDriftWarning_Fields(t *testing.T) {
	w := DriftWarning{
		Subsystem:     "watching",
		CodePath:      "watch/daemon.go",
		DocPath:       "docs/HOOKS.md",
		CommitsBehind: 3,
		Reason:        "watch/daemon.go changed 3 commits after docs/HOOKS.md was last updated",
	}

	if w.Subsystem != "watching" {
		t.Error("unexpected subsystem")
	}
	if w.CommitsBehind != 3 {
		t.Error("unexpected commits behind")
	}
}

func TestResolveCodePaths(t *testing.T) {
	tests := []struct {
		name       string
		subsystem  string
		paths      map[string][]string
		wantPrefix string
	}{
		{
			name:      "configured path strips globs",
			subsystem: "watching",
			paths: map[string][]string{
				"watching": {"watch/**", "cmd/hooks.go"},
			},
			wantPrefix: "watch/",
		},
		{
			name:       "fallback to guessed paths",
			subsystem:  "scanning",
			paths:      map[string][]string{},
			wantPrefix: "scanner/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveCodePaths(tt.subsystem, tt.paths)
			if len(got) == 0 {
				t.Fatal("expected at least one path")
			}
			found := false
			for _, p := range got {
				if p == tt.wantPrefix {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected %q in paths %v", tt.wantPrefix, got)
			}
		})
	}
}
