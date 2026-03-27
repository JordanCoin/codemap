package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunPluginInstall(t *testing.T) {
	home := t.TempDir()

	out := captureOutput(func() {
		RunPlugin([]string{"install", "--home", home})
	})

	checks := []string{
		"codemap plugin install",
		"Plugin:",
		"Marketplace:",
		"Marketplace entry: created",
	}
	for _, check := range checks {
		if !strings.Contains(out, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, out)
		}
	}

	pluginJSON := filepath.Join(home, "plugins", "codemap", ".codex-plugin", "plugin.json")
	if _, err := os.Stat(pluginJSON); err != nil {
		t.Fatalf("expected installed plugin manifest to exist: %v", err)
	}
}
