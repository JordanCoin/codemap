package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnsureClaudeMCPPreservesLargeIntegersAndUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")
	original := `{
  "bigInt": 9007199254740993,
  "nested": {
    "anotherBig": 1234567890123456789
  },
  "unknownKey": "keep me"
}
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := ensureClaudeMCPWithExecutable(path, filepath.Join(dir, "codemap"))
	if err != nil {
		t.Fatalf("ensureClaudeMCPWithExecutable: %v", err)
	}
	if !changed {
		t.Fatal("expected write to add the codemap server")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"9007199254740993", "1234567890123456789", "keep me", "mcpServers"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("round-trip lost %q:\n%s", want, data)
		}
	}
	if strings.Contains(string(data), "9007199254740992") {
		t.Fatalf("large integer was float64-mangled:\n%s", data)
	}
}

func TestEnsureHooksPreservesLargeIntegers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"bigInt": 9007199254740993}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := ensureClaudeHooksWithExecutable(path, false, filepath.Join(dir, "codemap")); err != nil {
		t.Fatalf("ensureClaudeHooksWithExecutable: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "9007199254740993") {
		t.Fatalf("large integer was float64-mangled:\n%s", data)
	}
}

func TestWriteFileAtomicReplacesContentWithoutTempResidue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := writeFileAtomic(path, []byte("new contents"), 0o644); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new contents" {
		t.Fatalf("content = %q, want %q", data, "new contents")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Fatalf("temp file left behind: %s", entry.Name())
		}
	}
}

func TestReplaceCodexMCPCommandPreservesCommentsAndUnrelatedTables(t *testing.T) {
	input := `# top-level comment
model = "gpt-5" # inline comment

[unrelated.table]
key = "value # not a comment"

[mcp_servers.codemap]
command = "old-codemap" # keep this note
args = ["mcp"]

[mcp_servers.other]
command = "other-server"
`
	updated, err := replaceCodexMCPCommand([]byte(input), "/abs/codemap", []string{"mcp", "--configured-version", "1.2.3", "--integration", "codex-setup"})
	if err != nil {
		t.Fatalf("replaceCodexMCPCommand: %v", err)
	}
	out := string(updated)
	for _, want := range []string{
		"# top-level comment",
		`model = "gpt-5" # inline comment`,
		"[unrelated.table]",
		`key = "value # not a comment"`,
		`command = "/abs/codemap" # keep this note`,
		"[mcp_servers.other]",
		`command = "other-server"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("surgery lost %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "old-codemap") {
		t.Fatalf("stale command survived:\n%s", out)
	}
	if !strings.Contains(out, "--configured-version") {
		t.Fatalf("args not replaced:\n%s", out)
	}
}

func TestReplaceCodexMCPCommandRejectsUnsafeShapes(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "missing table", input: "[mcp_servers.other]\ncommand = \"x\"\n"},
		{name: "multi-line args", input: "[mcp_servers.codemap]\ncommand = \"x\"\nargs = [\n  \"mcp\",\n]\n"},
		{name: "missing args", input: "[mcp_servers.codemap]\ncommand = \"x\"\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := replaceCodexMCPCommand([]byte(tt.input), "/abs/codemap", []string{"mcp"}); err == nil {
				t.Fatal("expected surgery to refuse this shape")
			}
		})
	}
}

func TestTomlCommentIndex(t *testing.T) {
	tests := []struct {
		value string
		want  int
	}{
		{`"a#b" # real`, 6},
		{`'lit#eral'`, -1},
		{`"esc\"#" # after`, 9},
		{`plain`, -1},
		{`# immediate`, 0},
	}
	for _, tt := range tests {
		if got := tomlCommentIndex(tt.value); got != tt.want {
			t.Fatalf("tomlCommentIndex(%q) = %d, want %d", tt.value, got, tt.want)
		}
	}
}

func TestMigrateOwnedHookCommand(t *testing.T) {
	specs := generatedCodexHooks("/abs/path/codemap")
	target := specs[1].Command // pre-edit

	tests := []struct {
		name     string
		existing string
		want     bool
	}{
		{name: "exact target", existing: target, want: true},
		{name: "legacy bare codemap", existing: "codemap hook pre-edit --agent=codex", want: true},
		{name: "legacy absolute path", existing: "'/somewhere/else/codemap' hook pre-edit --agent=codex", want: true},
		{name: "absolute path with integration suffix", existing: "'/other/install/codemap-next' hook pre-edit --agent=codex --integration=codex-setup", want: true},
		{name: "foreign command", existing: "rm -rf / hook pre-edit", want: false},
		{name: "unrelated hook", existing: "codemap hook session-start --agent=codex", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			migrated, ok := migrateOwnedHookCommand(tt.existing, target)
			if ok != tt.want {
				t.Fatalf("migrateOwnedHookCommand(%q) ok = %v, want %v", tt.existing, ok, tt.want)
			}
			if ok && migrated != target {
				t.Fatalf("migrated = %q, want %q", migrated, target)
			}
		})
	}
}

func TestUpdateSessionLeaseTracksActiveSessionsAndPrunesStale(t *testing.T) {
	root := t.TempDir()
	now := time.Now()

	var count int
	if err := updateSessionLease(root, "session-a", true, now, func(active int) { count = active }); err != nil {
		t.Fatalf("updateSessionLease(start a): %v", err)
	}
	if count != 1 {
		t.Fatalf("active count after first start = %d, want 1", count)
	}

	if err := updateSessionLease(root, "session-b", true, now, func(active int) { count = active }); err != nil {
		t.Fatalf("updateSessionLease(start b): %v", err)
	}
	if count != 2 {
		t.Fatalf("active count after second start = %d, want 2", count)
	}

	// Age session-b's lease past the TTL; the next update must reclaim it.
	leaseDir := filepath.Join(root, ".codemap", "sessions")
	entries, err := os.ReadDir(leaseDir)
	if err != nil {
		t.Fatal(err)
	}
	stale := now.Add(-sessionLeaseTTL - time.Hour)
	for _, entry := range entries {
		if err := os.Chtimes(filepath.Join(leaseDir, entry.Name()), stale, stale); err != nil {
			t.Fatal(err)
		}
	}
	if err := updateSessionLease(root, "session-a", true, now, func(active int) { count = active }); err != nil {
		t.Fatalf("updateSessionLease(refresh a): %v", err)
	}
	if count != 1 {
		t.Fatalf("active count after stale prune = %d, want 1 (only refreshed lease)", count)
	}

	if err := updateSessionLease(root, "session-a", false, now, func(active int) { count = active }); err != nil {
		t.Fatalf("updateSessionLease(stop a): %v", err)
	}
	if count != 0 {
		t.Fatalf("active count after stop = %d, want 0", count)
	}
}

func TestUpdateSessionLeaseReclaimsStaleLock(t *testing.T) {
	root := t.TempDir()
	lockPath := filepath.Join(root, ".codemap", "sessions.lock")
	if err := os.MkdirAll(lockPath, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-sessionLockStaleAfter - time.Minute)
	if err := os.Chtimes(lockPath, stale, stale); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := updateSessionLease(root, "session-a", true, time.Now(), func(active int) { count = active }); err != nil {
		t.Fatalf("updateSessionLease should reclaim a stale lock: %v", err)
	}
	if count != 1 {
		t.Fatalf("active count = %d, want 1", count)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatal("lock directory should be released after the update")
	}
}

func TestValidateCodexHookTrust(t *testing.T) {
	trustedEntry := func() codexHooksListEntry {
		entry := codexHooksListEntry{}
		for _, spec := range recommendedCodexHooks {
			entry.Hooks = append(entry.Hooks, codexHookMetadata{
				Command:     spec.Command,
				Enabled:     true,
				HandlerType: "command",
				TrustStatus: "trusted",
			})
		}
		return entry
	}

	tests := []struct {
		name    string
		mutate  func(*codexHooksListEntry)
		wantErr string
	}{
		{name: "all trusted", mutate: func(*codexHooksListEntry) {}, wantErr: ""},
		{
			name:    "disabled hook",
			mutate:  func(entry *codexHooksListEntry) { entry.Hooks[0].Enabled = false },
			wantErr: "disabled",
		},
		{
			name:    "untrusted hook",
			mutate:  func(entry *codexHooksListEntry) { entry.Hooks[1].TrustStatus = "untrusted" },
			wantErr: "untrusted",
		},
		{
			name:    "missing hook",
			mutate:  func(entry *codexHooksListEntry) { entry.Hooks = entry.Hooks[1:] },
			wantErr: "missing",
		},
		{
			name: "codex-reported error",
			mutate: func(entry *codexHooksListEntry) {
				entry.Errors = append(entry.Errors, codexHookError{Message: "hooks file invalid", Path: "/tmp/hooks.json"})
			},
			wantErr: "hooks file invalid",
		},
	}

	original := doctorCodexHooksProbe
	t.Cleanup(func() { doctorCodexHooksProbe = original })

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := trustedEntry()
			tt.mutate(&entry)
			doctorCodexHooksProbe = func(string) (codexHooksListEntry, error) {
				return entry, nil
			}
			err := validateCodexHookTrust(t.TempDir())
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected trust validation to pass, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestRunDoctorReportsUnconfiguredProject(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	originalLook := doctorLookPath
	doctorLookPath = func(string) (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { doctorLookPath = originalLook })

	var code int
	out := captureOutput(func() { code = RunDoctor([]string{root}, ".") })

	if code != 1 {
		t.Fatalf("RunDoctor exit code = %d, want 1 for unconfigured project\n%s", code, out)
	}
	for _, want := range []string{"MISS project config", "no supported coding agent", "need attention"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in doctor output:\n%s", want, out)
		}
	}
}

func TestDetectInstalledAgentsUsesHomeMarkers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", t.TempDir())

	claude, codex := detectInstalledAgents()
	if claude || codex {
		t.Fatalf("detection = (%v, %v), want none on empty home", claude, codex)
	}

	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	claude, codex = detectInstalledAgents()
	if !claude || codex {
		t.Fatalf("detection = (%v, %v), want claude only", claude, codex)
	}

	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	claude, codex = detectInstalledAgents()
	if !claude || !codex {
		t.Fatalf("detection = (%v, %v), want both", claude, codex)
	}
}
