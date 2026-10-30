package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codemap/internal/buildinfo"

	toml "github.com/pelletier/go-toml/v2"
)

// TestEnsureCodexMCPRepairsLegacyEntry is #157: a managed Codex MCP entry in
// the legacy no-root shape with a stale configured version is rewritten to the
// current executable and managed arguments, and unrelated servers survive
// byte-for-byte.
func TestEnsureCodexMCPRepairsLegacyEntry(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.toml")
	const other = "[mcp_servers.pencil]\ncommand = \"/Applications/Pen.app/mcp\"\nargs = [ \"--app\", \"desktop\", \"--agent\", \"codexCLI\" ]\n"
	legacy := "model = \"gpt-5\"\n\n[mcp_servers.codemap]\ncommand = \"/old/bin/codemap\"\nargs = [ \"mcp\", \"--configured-version\", \"4.3.0\", \"--integration\", \"codex-setup\" ]\n\n" + other
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	projectRoot := filepath.Join(root, "repo")
	changed, err := ensureCodexMCPWithExecutable(path, "/new/bin/codemap", projectRoot)
	if err != nil {
		t.Fatalf("ensureCodexMCPWithExecutable() error: %v", err)
	}
	if !changed {
		t.Fatal("legacy entry must be rewritten")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, other) {
		t.Fatalf("unrelated server must survive byte-for-byte, got:\n%s", got)
	}
	if !strings.Contains(got, "model = \"gpt-5\"") {
		t.Fatalf("unrelated top-level keys must survive, got:\n%s", got)
	}
	for _, want := range []string{`command = "/new/bin/codemap"`, `"--project-root"`, `"--configured-version", "` + buildinfo.Current() + `"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("rewritten entry lacks %s, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, `"4.3.0"`) || strings.Contains(got, "/old/bin/codemap") {
		t.Fatalf("stale version or executable survived, got:\n%s", got)
	}

	// Doctor recognizes the result as managed, and a second run is a no-op.
	if changed, err := ensureCodexMCPWithExecutable(path, "/new/bin/codemap", projectRoot); err != nil || changed {
		t.Fatalf("second run should be a no-op, changed=%v err=%v", changed, err)
	}
	var payload map[string]any
	if err := toml.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	raw := payload["mcp_servers"].(map[string]any)["codemap"]
	launch, err := parseDoctorManagedLaunch(raw, "codex")
	if err != nil {
		t.Fatalf("doctor must recognize the repaired entry: %v", err)
	}
	if launch.integration != "codex-setup" || launch.configuredVersion != buildinfo.Current() {
		t.Fatalf("launch = %+v", launch)
	}
}
