package pluginbundle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallCodemapPluginCreatesBundleAndMarketplace(t *testing.T) {
	home := t.TempDir()

	result, err := InstallCodemapPlugin(InstallOptions{HomeDir: home})
	if err != nil {
		t.Fatalf("InstallCodemapPlugin returned error: %v", err)
	}

	if result.FilesWritten == 0 {
		t.Fatal("expected plugin installer to write files")
	}
	if !result.CreatedMarketplace {
		t.Fatal("expected plugin installer to create marketplace")
	}

	checks := []string{
		filepath.Join(home, "plugins", "codemap", ".codex-plugin", "plugin.json"),
		filepath.Join(home, "plugins", "codemap", ".mcp.json"),
		filepath.Join(home, "plugins", "codemap", "README.md"),
		filepath.Join(home, "plugins", "codemap", "assets", "icon.png"),
		filepath.Join(home, "plugins", "codemap", "scripts", "run-codemap-mcp.sh"),
		filepath.Join(home, ".agents", "plugins", "marketplace.json"),
	}
	for _, path := range checks {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist: %v", path, err)
		}
	}

	scriptInfo, err := os.Stat(filepath.Join(home, "plugins", "codemap", "scripts", "run-codemap-mcp.sh"))
	if err != nil {
		t.Fatalf("stat plugin script: %v", err)
	}
	if scriptInfo.Mode().Perm() != 0o755 {
		t.Fatalf("expected script mode 0755, got %o", scriptInfo.Mode().Perm())
	}

	pluginData, err := os.ReadFile(filepath.Join(home, "plugins", "codemap", ".codex-plugin", "plugin.json"))
	if err != nil {
		t.Fatalf("read plugin.json: %v", err)
	}
	if !strings.Contains(string(pluginData), `"name": "codemap"`) {
		t.Fatalf("plugin.json missing codemap name:\n%s", string(pluginData))
	}

	var marketplace map[string]any
	data, err := os.ReadFile(filepath.Join(home, ".agents", "plugins", "marketplace.json"))
	if err != nil {
		t.Fatalf("read marketplace.json: %v", err)
	}
	if err := json.Unmarshal(data, &marketplace); err != nil {
		t.Fatalf("unmarshal marketplace.json: %v", err)
	}
	plugins, ok := marketplace["plugins"].([]any)
	if !ok || len(plugins) != 1 {
		t.Fatalf("expected one marketplace plugin entry, got %#v", marketplace["plugins"])
	}
	entry := plugins[0].(map[string]any)
	if entry["name"] != "codemap" {
		t.Fatalf("marketplace entry name = %#v, want codemap", entry["name"])
	}
}

func TestInstallCodemapPluginIsIdempotentAndPreservesMarketplaceMetadata(t *testing.T) {
	home := t.TempDir()
	marketplacePath := filepath.Join(home, ".agents", "plugins", "marketplace.json")
	if err := os.MkdirAll(filepath.Dir(marketplacePath), 0o755); err != nil {
		t.Fatal(err)
	}
	initial := `{
  "name": "custom-marketplace",
  "interface": {
    "displayName": "Custom Plugins"
  },
  "plugins": [
    {
      "name": "other-plugin",
      "source": {
        "source": "local",
        "path": "./plugins/other-plugin"
      },
      "policy": {
        "installation": "AVAILABLE",
        "authentication": "ON_INSTALL"
      },
      "category": "Productivity"
    }
  ]
}
`
	if err := os.WriteFile(marketplacePath, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	first, err := InstallCodemapPlugin(InstallOptions{HomeDir: home})
	if err != nil {
		t.Fatalf("first install error: %v", err)
	}
	if !first.UpdatedMarketplace {
		t.Fatal("expected first install to update marketplace")
	}

	second, err := InstallCodemapPlugin(InstallOptions{HomeDir: home})
	if err != nil {
		t.Fatalf("second install error: %v", err)
	}
	if second.FilesWritten != 0 {
		t.Fatalf("expected second install to avoid rewriting files, wrote %d", second.FilesWritten)
	}
	if second.FilesUnchanged == 0 {
		t.Fatal("expected second install to report unchanged files")
	}
	if second.UpdatedMarketplace {
		t.Fatal("expected second install to leave marketplace unchanged")
	}

	var marketplace map[string]any
	data, err := os.ReadFile(marketplacePath)
	if err != nil {
		t.Fatalf("read marketplace.json: %v", err)
	}
	if err := json.Unmarshal(data, &marketplace); err != nil {
		t.Fatalf("unmarshal marketplace.json: %v", err)
	}
	if marketplace["name"] != "custom-marketplace" {
		t.Fatalf("marketplace name changed unexpectedly: %#v", marketplace["name"])
	}
	if iface, ok := marketplace["interface"].(map[string]any); !ok || iface["displayName"] != "Custom Plugins" {
		t.Fatalf("marketplace display name changed unexpectedly: %#v", marketplace["interface"])
	}
	plugins := marketplace["plugins"].([]any)
	if len(plugins) != 2 {
		t.Fatalf("expected two marketplace entries after install, got %d", len(plugins))
	}
}
