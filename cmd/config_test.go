package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codemap/config"
)

func TestIsConfigEmpty(t *testing.T) {
	t.Run("zero value is empty", func(t *testing.T) {
		if !isConfigEmpty(config.ProjectConfig{}) {
			t.Fatal("expected zero-value config to be empty")
		}
	})

	t.Run("new policy fields make config non-empty", func(t *testing.T) {
		cfg := config.ProjectConfig{
			Mode: "structured",
		}
		if isConfigEmpty(cfg) {
			t.Fatal("expected config with mode set to be non-empty")
		}
	})
}

func TestConfigShow_PrintsPolicyFields(t *testing.T) {
	root := t.TempDir()
	codemapDir := filepath.Join(root, ".codemap")
	if err := os.MkdirAll(codemapDir, 0755); err != nil {
		t.Fatal(err)
	}

	data := `{
		"only": ["go", "ts"],
		"mode": "structured",
		"budgets": {
			"session_start_bytes": 26000,
			"diff_bytes": 12000,
			"max_hubs": 7
		},
		"routing": {
			"retrieval": {"strategy": "keyword", "top_k": 4},
			"subsystems": [
				{"id": "watching", "keywords": ["hook"], "docs": ["docs/HOOKS.md"], "agents": ["codemap-hook-triage"]}
			]
		},
		"drift": {
			"enabled": true,
			"recent_commits": 12,
			"require_docs_for": ["watching"]
		}
	}`
	if err := os.WriteFile(filepath.Join(codemapDir, "config.json"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	out := captureOutput(func() { configShow(root) })
	want := []string{
		"only:",
		"mode:",
		"budgets:",
		"session_start_bytes",
		"diff_bytes",
		"max_hubs",
		"routing:",
		"retrieval: strategy=keyword top_k=4",
		"subsystems: 1 configured",
		"watching (keywords=1 docs=1 agents=1)",
		"drift:",
		"enabled: true",
		"recent_commits: 12",
		"require_docs_for: watching",
	}
	for _, token := range want {
		if !strings.Contains(out, token) {
			t.Fatalf("expected config show output to contain %q, got:\n%s", token, out)
		}
	}
}
