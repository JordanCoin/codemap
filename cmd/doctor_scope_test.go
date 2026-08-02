package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func runtimeIsWindows() bool { return runtime.GOOS == "windows" }

// hookSettingsJSON renders a Claude settings file whose hook commands are
// derived from specs via transform. It lets tests express "the same hooks,
// written a different but equivalent way".
func hookSettingsJSON(t *testing.T, specs []claudeHookSpec, transform func(claudeHookSpec) string) string {
	t.Helper()
	hooks := map[string][]claudeHookEntry{}
	for _, spec := range specs {
		hooks[spec.Event] = append(hooks[spec.Event], claudeHookEntry{
			Matcher: spec.Matcher,
			Hooks:   []claudeHookCommand{{Type: "command", Command: transform(spec)}},
		})
	}
	data, err := json.Marshal(map[string]any{"hooks": hooks})
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	return string(data)
}

func writeHookSettings(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// stripIntegrationTag removes the trailing --integration=<value> token that
// codemap adds purely as an ownership marker.
func stripIntegrationTag(command string) string {
	marker := " --integration="
	index := strings.LastIndex(command, marker)
	if index < 0 {
		return command
	}
	rest := command[index+len(marker):]
	if space := strings.Index(rest, " "); space >= 0 {
		return command[:index] + rest[space:]
	}
	return command[:index]
}

// TestValidateHooksAcceptsEquivalentCommands pins the rule that doctor reports
// a hook as present when it invokes the same codemap subcommand, regardless of
// whether the executable is PATH-resolved or absolute and whether the
// ownership tag is present. setup already treats the bare form as codemap-owned
// (migrateOwnedHookCommand), so doctor must not call it missing.
func TestValidateHooksAcceptsEquivalentCommands(t *testing.T) {
	specs := generatedClaudeHooks("/usr/local/bin/codemap")

	for _, tt := range []struct {
		name      string
		transform func(claudeHookSpec) string
		wantErr   bool
	}{
		{
			name:      "canonical quoted absolute with tag",
			transform: func(s claudeHookSpec) string { return s.Command },
		},
		{
			name: "PATH-resolved bare executable without tag",
			transform: func(s claudeHookSpec) string {
				return stripIntegrationTag(strings.Replace(s.Command, "'/usr/local/bin/codemap'", "codemap", 1))
			},
		},
		{
			name: "unquoted absolute path without tag",
			transform: func(s claudeHookSpec) string {
				return stripIntegrationTag(strings.Replace(s.Command, "'/usr/local/bin/codemap'", "/usr/local/bin/codemap", 1))
			},
		},
		{
			name: "windows quoted exe with tag",
			transform: func(s claudeHookSpec) string {
				return strings.Replace(s.Command, "'/usr/local/bin/codemap'", `"C:\Tools\codemap.exe"`, 1)
			},
		},
		{
			name: "different subcommand is not a match",
			transform: func(s claudeHookSpec) string {
				return strings.Replace(s.Command, " hook ", " hook not-a-real-", 1)
			},
			wantErr: true,
		},
		{
			name: "foreign executable is not a match",
			transform: func(s claudeHookSpec) string {
				return stripIntegrationTag(strings.Replace(s.Command, "'/usr/local/bin/codemap'", "othertool", 1))
			},
			wantErr: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			writeHookSettings(t, path, hookSettingsJSON(t, specs, tt.transform))

			err := validateHooks(path, specs)
			if tt.wantErr && err == nil {
				t.Fatalf("validateHooks() = nil, want error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validateHooks() = %v, want nil", err)
			}
		})
	}
}

// TestValidateHooksKeepsAgentDistinct guards against the relaxation being too
// loose: a Claude hook command must not satisfy the Codex spec, because
// --agent=codex changes hook behavior (unlike --integration, which does not).
func TestValidateHooksKeepsAgentDistinct(t *testing.T) {
	claudeSpecs := generatedClaudeHooks("/usr/local/bin/codemap")
	codexSpecs := generatedCodexHooks("/usr/local/bin/codemap")

	path := filepath.Join(t.TempDir(), "settings.json")
	writeHookSettings(t, path, hookSettingsJSON(t, claudeSpecs, func(s claudeHookSpec) string {
		return stripIntegrationTag(s.Command)
	}))

	if err := validateHooks(path, codexSpecs); err == nil {
		t.Fatal("validateHooks() accepted Claude hooks for the Codex spec, want error")
	}
}

// TestHasHookSpecStaysExact protects setup's ownership decision. Rewriting a
// hook entry in place requires byte-identity; only doctor's "is it present"
// question is allowed to be semantic.
func TestHasHookSpecStaysExact(t *testing.T) {
	spec := generatedClaudeHooks("/usr/local/bin/codemap")[0]
	entries := []claudeHookEntry{{
		Hooks: []claudeHookCommand{{Type: "command", Command: stripIntegrationTag(spec.Command)}},
	}}

	if hasHookSpec(entries, spec) {
		t.Fatal("hasHookSpec() matched a non-identical command; setup would skip migrating it")
	}
}

// TestRunDoctorFallsBackToUserScopeHooks covers the scope half: Claude Code
// merges user settings into every project, so hooks configured in
// ~/.claude/settings.json genuinely apply here and must not be reported MISS.
func TestRunDoctorFallsBackToUserScopeHooks(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	originalLook := doctorLookPath
	originalRecommended := recommendedClaudeHooks
	t.Cleanup(func() {
		doctorLookPath = originalLook
		recommendedClaudeHooks = originalRecommended
	})
	doctorLookPath = func(name string) (string, error) {
		if name == "claude" {
			return "/usr/local/bin/claude", nil
		}
		return "", os.ErrNotExist
	}
	recommendedClaudeHooks = generatedClaudeHooks("/usr/local/bin/codemap")

	// Project scope has settings but no hooks; user scope has them.
	writeHookSettings(t, filepath.Join(root, ".claude", "settings.local.json"), `{"permissions":{}}`)
	writeHookSettings(t, filepath.Join(home, ".claude", "settings.json"),
		hookSettingsJSON(t, recommendedClaudeHooks, func(s claudeHookSpec) string { return s.Command }))

	out := captureOutput(func() { RunDoctor([]string{"--agent", "claude", root}, ".") })

	if strings.Contains(out, "MISS Claude hooks") {
		t.Fatalf("doctor reported Claude hooks missing despite user-scope configuration:\n%s", out)
	}
	if !strings.Contains(out, "OK   Claude hooks") {
		t.Fatalf("doctor did not report Claude hooks OK:\n%s", out)
	}
	if !strings.Contains(out, "user scope") {
		t.Fatalf("doctor did not disclose which scope satisfied the check:\n%s", out)
	}
}

// TestRunDoctorFindsLocalScopedClaudeMCP covers Claude Code's third MCP
// location: `claude mcp add` with the default local scope writes the server
// into ~/.claude.json under projects[<root>].mcpServers, not the top-level
// mcpServers object. A server registered that way is live for this project, so
// doctor must not report it missing.
func TestRunDoctorFindsLocalScopedClaudeMCP(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	originalLook := doctorLookPath
	originalVersionProbe := doctorVersionProbe
	originalMCPProbe := doctorMCPProbe
	t.Cleanup(func() {
		doctorLookPath = originalLook
		doctorVersionProbe = originalVersionProbe
		doctorMCPProbe = originalMCPProbe
	})
	doctorLookPath = func(name string) (string, error) {
		if name == "claude" {
			return "/usr/local/bin/claude", nil
		}
		return "", os.ErrNotExist
	}
	// The scope lookup is what is under test, not the launch probes.
	doctorVersionProbe = func(doctorManagedLaunch) error { return nil }
	doctorMCPProbe = func(doctorManagedLaunch) error { return nil }

	binary := filepath.Join(home, "codemap")
	if runtimeIsWindows() {
		binary += ".exe"
	}
	if err := os.WriteFile(binary, []byte("stub"), 0o755); err != nil {
		t.Fatalf("write stub binary: %v", err)
	}
	server := map[string]any{
		"command": binary,
		"args":    managedMCPArgs("1.0.0", "claude-setup"),
	}
	payload := map[string]any{
		"mcpServers": map[string]any{},
		"projects": map[string]any{
			root: map[string]any{"mcpServers": map[string]any{"codemap": server}},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal claude.json: %v", err)
	}
	writeHookSettings(t, filepath.Join(home, ".claude.json"), string(data))

	out := captureOutput(func() { RunDoctor([]string{"--agent", "claude", root}, ".") })

	if strings.Contains(out, "MISS Claude MCP") {
		t.Fatalf("doctor reported Claude MCP missing despite local-scope registration:\n%s", out)
	}
	if !strings.Contains(out, "OK   Claude MCP") || !strings.Contains(out, "local scope") {
		t.Fatalf("doctor did not report Claude MCP OK in local scope:\n%s", out)
	}
}

// TestRunDoctorReportsMisconfiguredScopeOverAbsentOne keeps the failure message
// actionable. When several scopes fail, a scope that exists but is wired up
// wrong is the user's real problem; a scope whose file simply does not exist is
// expected and says nothing. Reporting the absent one buries the diagnosis.
func TestRunDoctorReportsMisconfiguredScopeOverAbsentOne(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	originalLook := doctorLookPath
	t.Cleanup(func() { doctorLookPath = originalLook })
	doctorLookPath = func(name string) (string, error) {
		if name == "claude" {
			return "/usr/local/bin/claude", nil
		}
		return "", os.ErrNotExist
	}

	// No project .mcp.json at all; the local scope exists but is hand-wired
	// rather than codemap-managed.
	payload := map[string]any{
		"projects": map[string]any{
			root: map[string]any{"mcpServers": map[string]any{
				"codemap": map[string]any{"command": "/somewhere/codemap-mcp", "args": []string{}},
			}},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal claude.json: %v", err)
	}
	claudeJSON := filepath.Join(home, ".claude.json")
	writeHookSettings(t, claudeJSON, string(data))

	out := captureOutput(func() { RunDoctor([]string{"--agent", "claude", root}, ".") })

	line := doctorLineFor(t, out, "MISS Claude MCP")
	if !strings.Contains(line, claudeJSON) {
		t.Fatalf("failure did not name the misconfigured scope %s:\n%s", claudeJSON, line)
	}
	if strings.Contains(line, "no such file") {
		t.Fatalf("failure reported an absent scope instead of the misconfigured one:\n%s", line)
	}
}

// doctorLineFor returns the single doctor report line beginning with prefix.
func doctorLineFor(t *testing.T, out, prefix string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			return line
		}
	}
	t.Fatalf("no %q line in doctor output:\n%s", prefix, out)
	return ""
}

// TestRunDoctorGlobalFlagChecksOnlyUserScope keeps --global meaning "check the
// user-scoped configuration", so it must not be satisfied by project files.
func TestRunDoctorGlobalFlagChecksOnlyUserScope(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	originalLook := doctorLookPath
	originalRecommended := recommendedClaudeHooks
	t.Cleanup(func() {
		doctorLookPath = originalLook
		recommendedClaudeHooks = originalRecommended
	})
	doctorLookPath = func(name string) (string, error) {
		if name == "claude" {
			return "/usr/local/bin/claude", nil
		}
		return "", os.ErrNotExist
	}
	recommendedClaudeHooks = generatedClaudeHooks("/usr/local/bin/codemap")

	// Only project scope is configured.
	writeHookSettings(t, filepath.Join(root, ".claude", "settings.local.json"),
		hookSettingsJSON(t, recommendedClaudeHooks, func(s claudeHookSpec) string { return s.Command }))

	out := captureOutput(func() { RunDoctor([]string{"--global", "--agent", "claude", root}, ".") })

	if !strings.Contains(out, "MISS Claude hooks") {
		t.Fatalf("--global was satisfied by project-scope hooks:\n%s", out)
	}
}
