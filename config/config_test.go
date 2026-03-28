package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codemap/limits"
)

func TestLoad_MissingFile(t *testing.T) {
	cfg := Load("/nonexistent/path")
	if len(cfg.Only) != 0 || len(cfg.Exclude) != 0 || cfg.Depth != 0 {
		t.Errorf("expected zero-value config for missing file, got %+v", cfg)
	}
}

func TestLoad_ValidConfig(t *testing.T) {
	dir := t.TempDir()
	codemapDir := filepath.Join(dir, ".codemap")
	if err := os.MkdirAll(codemapDir, 0755); err != nil {
		t.Fatal(err)
	}

	data := `{
		"only": ["rs", "sh", "sql"],
		"exclude": ["docs/reference", "vendor"],
		"depth": 3,
		"mode": "structured",
		"budgets": {
			"session_start_bytes": 28000,
			"diff_bytes": 11000,
			"max_hubs": 6
		},
		"routing": {
			"retrieval": {
				"strategy": "keyword",
				"top_k": 4
			},
			"subsystems": [
				{
					"id": "watching",
					"keywords": ["hook", "daemon"],
					"docs": ["docs/HOOKS.md"],
					"agents": ["codemap-hook-triage"]
				}
			]
		},
		"drift": {
			"enabled": true,
			"recent_commits": 9,
			"require_docs_for": ["watching"]
		}
	}`
	if err := os.WriteFile(filepath.Join(codemapDir, "config.json"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := Load(dir)
	if len(cfg.Only) != 3 || cfg.Only[0] != "rs" || cfg.Only[1] != "sh" || cfg.Only[2] != "sql" {
		t.Errorf("unexpected Only: %v", cfg.Only)
	}
	if len(cfg.Exclude) != 2 || cfg.Exclude[0] != "docs/reference" || cfg.Exclude[1] != "vendor" {
		t.Errorf("unexpected Exclude: %v", cfg.Exclude)
	}
	if cfg.Depth != 3 {
		t.Errorf("unexpected Depth: %d", cfg.Depth)
	}
	if cfg.Mode != "structured" {
		t.Errorf("unexpected Mode: %q", cfg.Mode)
	}
	if cfg.Budgets.SessionStartBytes != 28000 {
		t.Errorf("unexpected SessionStartBytes: %d", cfg.Budgets.SessionStartBytes)
	}
	if cfg.Budgets.DiffBytes != 11000 {
		t.Errorf("unexpected DiffBytes: %d", cfg.Budgets.DiffBytes)
	}
	if cfg.Budgets.MaxHubs != 6 {
		t.Errorf("unexpected MaxHubs: %d", cfg.Budgets.MaxHubs)
	}
	if got := cfg.RoutingTopKOrDefault(); got != 4 {
		t.Errorf("unexpected routing top_k: %d", got)
	}
	if len(cfg.Routing.Subsystems) != 1 || cfg.Routing.Subsystems[0].ID != "watching" {
		t.Errorf("unexpected Routing.Subsystems: %+v", cfg.Routing.Subsystems)
	}
	if !cfg.Drift.Enabled {
		t.Errorf("expected drift enabled, got false")
	}
	if cfg.Drift.RecentCommits != 9 {
		t.Errorf("unexpected Drift.RecentCommits: %d", cfg.Drift.RecentCommits)
	}
	if len(cfg.Drift.RequireDocsFor) != 1 || cfg.Drift.RequireDocsFor[0] != "watching" {
		t.Errorf("unexpected Drift.RequireDocsFor: %v", cfg.Drift.RequireDocsFor)
	}
}

func TestLoad_PartialConfig(t *testing.T) {
	dir := t.TempDir()
	codemapDir := filepath.Join(dir, ".codemap")
	if err := os.MkdirAll(codemapDir, 0755); err != nil {
		t.Fatal(err)
	}

	data := `{"only": ["go", "ts"]}`
	if err := os.WriteFile(filepath.Join(codemapDir, "config.json"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := Load(dir)
	if len(cfg.Only) != 2 || cfg.Only[0] != "go" || cfg.Only[1] != "ts" {
		t.Errorf("unexpected Only: %v", cfg.Only)
	}
	if len(cfg.Exclude) != 0 {
		t.Errorf("expected empty Exclude, got %v", cfg.Exclude)
	}
	if cfg.Depth != 0 {
		t.Errorf("expected zero Depth, got %d", cfg.Depth)
	}
}

func TestLoad_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	codemapDir := filepath.Join(dir, ".codemap")
	if err := os.MkdirAll(codemapDir, 0755); err != nil {
		t.Fatal(err)
	}

	data := `{not valid json`
	if err := os.WriteFile(filepath.Join(codemapDir, "config.json"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := Load(dir)
	if len(cfg.Only) != 0 || len(cfg.Exclude) != 0 || cfg.Depth != 0 {
		t.Errorf("expected zero-value config for malformed JSON, got %+v", cfg)
	}
}

func TestLoad_EmptyObject(t *testing.T) {
	dir := t.TempDir()
	codemapDir := filepath.Join(dir, ".codemap")
	if err := os.MkdirAll(codemapDir, 0755); err != nil {
		t.Fatal(err)
	}

	data := `{}`
	if err := os.WriteFile(filepath.Join(codemapDir, "config.json"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := Load(dir)
	if len(cfg.Only) != 0 || len(cfg.Exclude) != 0 || cfg.Depth != 0 {
		t.Errorf("expected zero-value config for empty object, got %+v", cfg)
	}
}

func TestConfigPath(t *testing.T) {
	got := ConfigPath("/my/project")
	want := filepath.Join("/my/project", ".codemap", "config.json")
	if got != want {
		t.Errorf("ConfigPath = %q, want %q", got, want)
	}
}

func TestPolicyDefaultsAndClamps(t *testing.T) {
	t.Run("defaults for empty config", func(t *testing.T) {
		var cfg ProjectConfig
		if got := cfg.ModeOrDefault(); got != "auto" {
			t.Fatalf("ModeOrDefault() = %q, want auto", got)
		}
		if got := cfg.SessionStartOutputBytes(); got != limits.MaxStructureOutputBytes {
			t.Fatalf("SessionStartOutputBytes() = %d, want %d", got, limits.MaxStructureOutputBytes)
		}
		if got := cfg.DiffOutputBytes(); got != limits.MaxDiffOutputBytes {
			t.Fatalf("DiffOutputBytes() = %d, want %d", got, limits.MaxDiffOutputBytes)
		}
		if got := cfg.HubDisplayLimit(); got != 10 {
			t.Fatalf("HubDisplayLimit() = %d, want 10", got)
		}
		if got := cfg.RoutingStrategyOrDefault(); got != "keyword" {
			t.Fatalf("RoutingStrategyOrDefault() = %q, want keyword", got)
		}
		if got := cfg.RoutingTopKOrDefault(); got != 3 {
			t.Fatalf("RoutingTopKOrDefault() = %d, want 3", got)
		}
	})

	t.Run("clamps unsafe values", func(t *testing.T) {
		cfg := ProjectConfig{
			Mode: "invalid",
			Budgets: HookBudgets{
				SessionStartBytes: limits.MaxContextOutputBytes * 5,
				DiffBytes:         limits.MaxContextOutputBytes * 4,
				MaxHubs:           500,
			},
			Routing: RoutingConfig{
				Retrieval: RetrievalConfig{
					Strategy: "semantic",
					TopK:     0,
				},
			},
		}
		if got := cfg.ModeOrDefault(); got != "auto" {
			t.Fatalf("ModeOrDefault() = %q, want auto", got)
		}
		if got := cfg.SessionStartOutputBytes(); got != limits.MaxContextOutputBytes {
			t.Fatalf("SessionStartOutputBytes() = %d, want %d", got, limits.MaxContextOutputBytes)
		}
		if got := cfg.DiffOutputBytes(); got != limits.MaxContextOutputBytes {
			t.Fatalf("DiffOutputBytes() = %d, want %d", got, limits.MaxContextOutputBytes)
		}
		if got := cfg.HubDisplayLimit(); got != 100 {
			t.Fatalf("HubDisplayLimit() = %d, want 100", got)
		}
		if got := cfg.RoutingStrategyOrDefault(); got != "keyword" {
			t.Fatalf("RoutingStrategyOrDefault() = %q, want keyword", got)
		}
		if got := cfg.RoutingTopKOrDefault(); got != 3 {
			t.Fatalf("RoutingTopKOrDefault() = %d, want 3", got)
		}
	})
}

func TestProjectConfigStateHelpers(t *testing.T) {
	t.Run("zero config is zero and not boilerplate", func(t *testing.T) {
		var cfg ProjectConfig
		if !cfg.IsZero() {
			t.Fatal("expected zero-value config to be zero")
		}
		if cfg.LooksBoilerplate() {
			t.Fatal("did not expect zero-value config to look boilerplate")
		}
	})

	t.Run("extension-only config looks boilerplate", func(t *testing.T) {
		cfg := ProjectConfig{
			Only: []string{"go", "ts"},
		}
		if cfg.IsZero() {
			t.Fatal("did not expect extension-only config to be zero")
		}
		if !cfg.LooksBoilerplate() {
			t.Fatal("expected extension-only config to look boilerplate")
		}
	})

	t.Run("project-shaped config does not look boilerplate", func(t *testing.T) {
		cfg := ProjectConfig{
			Only:    []string{"swift"},
			Exclude: []string{".xcassets", "Snapshots"},
			Depth:   4,
		}
		if cfg.LooksBoilerplate() {
			t.Fatal("did not expect tuned config to look boilerplate")
		}
	})
}

func TestAssessSetup(t *testing.T) {
	t.Run("missing config needs setup", func(t *testing.T) {
		assessment := AssessSetup(t.TempDir())
		if assessment.State != SetupStateMissing {
			t.Fatalf("AssessSetup() state = %q, want %q", assessment.State, SetupStateMissing)
		}
		if !assessment.NeedsAttention() {
			t.Fatal("expected missing config to need attention")
		}
	})

	t.Run("malformed config needs setup", func(t *testing.T) {
		root := t.TempDir()
		codemapDir := filepath.Join(root, ".codemap")
		if err := os.MkdirAll(codemapDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(codemapDir, "config.json"), []byte("{bad json"), 0o644); err != nil {
			t.Fatal(err)
		}

		assessment := AssessSetup(root)
		if assessment.State != SetupStateMalformed {
			t.Fatalf("AssessSetup() state = %q, want %q", assessment.State, SetupStateMalformed)
		}
	})

	t.Run("bootstrap config is boilerplate", func(t *testing.T) {
		root := t.TempDir()
		codemapDir := filepath.Join(root, ".codemap")
		if err := os.MkdirAll(codemapDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(codemapDir, "config.json"), []byte(`{"only":["rs","toml"]}`), 0o644); err != nil {
			t.Fatal(err)
		}

		assessment := AssessSetup(root)
		if assessment.State != SetupStateBoilerplate {
			t.Fatalf("AssessSetup() state = %q, want %q", assessment.State, SetupStateBoilerplate)
		}
		if len(assessment.Reasons) == 0 || !strings.Contains(strings.ToLower(assessment.Reasons[0]), "bootstrap") {
			t.Fatalf("expected bootstrap reason, got %v", assessment.Reasons)
		}
	})

	t.Run("tuned config is ready", func(t *testing.T) {
		root := t.TempDir()
		codemapDir := filepath.Join(root, ".codemap")
		if err := os.MkdirAll(codemapDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(codemapDir, "config.json"), []byte(`{"only":["swift"],"exclude":[".xcassets"],"depth":4}`), 0o644); err != nil {
			t.Fatal(err)
		}

		assessment := AssessSetup(root)
		if assessment.State != SetupStateReady {
			t.Fatalf("AssessSetup() state = %q, want %q", assessment.State, SetupStateReady)
		}
		if assessment.NeedsAttention() {
			t.Fatal("did not expect tuned config to need attention")
		}
	})
}
