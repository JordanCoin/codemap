package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"codemap/config"
	"codemap/handoff"
	"codemap/limits"
	"codemap/watch"
)

func withOwnedDaemonProcess(t *testing.T, fn func(string) bool) {
	t.Helper()
	prev := isOwnedDaemonProcess
	isOwnedDaemonProcess = fn
	t.Cleanup(func() {
		isOwnedDaemonProcess = prev
	})
}

// TestHubInfoIsHub tests the hub detection threshold (3+ importers)
func TestHubInfoIsHub(t *testing.T) {
	tests := []struct {
		name      string
		importers map[string][]string
		file      string
		wantHub   bool
	}{
		{
			name:      "no importers - not a hub",
			importers: map[string][]string{},
			file:      "foo.go",
			wantHub:   false,
		},
		{
			name: "1 importer - not a hub",
			importers: map[string][]string{
				"foo.go": {"bar.go"},
			},
			file:    "foo.go",
			wantHub: false,
		},
		{
			name: "2 importers - not a hub",
			importers: map[string][]string{
				"foo.go": {"bar.go", "baz.go"},
			},
			file:    "foo.go",
			wantHub: false,
		},
		{
			name: "3 importers - is a hub",
			importers: map[string][]string{
				"foo.go": {"a.go", "b.go", "c.go"},
			},
			file:    "foo.go",
			wantHub: true,
		},
		{
			name: "10 importers - is a hub",
			importers: map[string][]string{
				"types.go": {"a.go", "b.go", "c.go", "d.go", "e.go", "f.go", "g.go", "h.go", "i.go", "j.go"},
			},
			file:    "types.go",
			wantHub: true,
		},
		{
			name: "file not in map - not a hub",
			importers: map[string][]string{
				"other.go": {"a.go", "b.go", "c.go"},
			},
			file:    "missing.go",
			wantHub: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &hubInfo{
				Importers: tt.importers,
			}
			got := info.isHub(tt.file)
			if got != tt.wantHub {
				t.Errorf("isHub(%q) = %v, want %v", tt.file, got, tt.wantHub)
			}
		})
	}
}

// TestRunHookRouting tests that RunHook routes to correct handlers
func TestRunHookRouting(t *testing.T) {
	// Test unknown hook returns error
	err := RunHook("unknown-hook", "/tmp")
	if err == nil {
		t.Error("expected error for unknown hook")
	}
	if !strings.Contains(err.Error(), "unknown hook") {
		t.Errorf("error should mention 'unknown hook', got: %v", err)
	}
	if !strings.Contains(err.Error(), "Available:") {
		t.Errorf("error should list available hooks, got: %v", err)
	}

	// Verify all known hooks are listed in error message
	knownHooks := []string{"session-start", "pre-edit", "post-edit", "prompt-submit", "pre-compact", "session-stop"}
	for _, hook := range knownHooks {
		if !strings.Contains(err.Error(), hook) {
			t.Errorf("error should list %q as available hook", hook)
		}
	}
}

func TestRunWithTimeout(t *testing.T) {
	t.Run("returns function result before timeout", func(t *testing.T) {
		errExpected := errors.New("boom")
		err := runWithTimeout("test-hook", 50*time.Millisecond, func() error {
			return errExpected
		})
		if !errors.Is(err, errExpected) {
			t.Fatalf("expected %v, got %v", errExpected, err)
		}
	})

	t.Run("returns timeout error when function blocks too long", func(t *testing.T) {
		err := runWithTimeout("slow-hook", 20*time.Millisecond, func() error {
			time.Sleep(80 * time.Millisecond)
			return nil
		})
		var timeoutErr *HookTimeoutError
		if !errors.As(err, &timeoutErr) {
			t.Fatalf("expected HookTimeoutError, got %v", err)
		}
		if timeoutErr.Hook != "slow-hook" {
			t.Fatalf("expected hook name slow-hook, got %q", timeoutErr.Hook)
		}
	})
}

func TestCappedStringWriter(t *testing.T) {
	t.Run("captures full output under limit", func(t *testing.T) {
		w := newCappedStringWriter(8)
		n, err := w.Write([]byte("hello"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if n != 5 {
			t.Fatalf("expected 5 bytes written, got %d", n)
		}
		if got := w.String(); got != "hello" {
			t.Fatalf("expected %q, got %q", "hello", got)
		}
		if w.Truncated() {
			t.Fatal("expected writer not truncated")
		}
	})

	t.Run("caps output and marks truncated", func(t *testing.T) {
		w := newCappedStringWriter(4)
		n, err := w.Write([]byte("helloworld"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if n != len("helloworld") {
			t.Fatalf("expected write count %d, got %d", len("helloworld"), n)
		}
		if got := w.String(); got != "hell" {
			t.Fatalf("expected %q, got %q", "hell", got)
		}
		if !w.Truncated() {
			t.Fatal("expected writer to be truncated")
		}
	})
}

func TestHookTimeoutFromEnv(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want time.Duration
	}{
		{name: "empty uses default", env: "", want: DefaultHookTimeout},
		{name: "valid duration parses", env: "150ms", want: 150 * time.Millisecond},
		{name: "zero disables timeout", env: "0", want: 0},
		{name: "negative falls back to default", env: "-1s", want: DefaultHookTimeout},
		{name: "invalid falls back to default", env: "not-a-duration", want: DefaultHookTimeout},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HookTimeoutFromEnv(func(key string) string {
				if key != "CODEMAP_HOOK_TIMEOUT" {
					return ""
				}
				return tt.env
			})
			if got != tt.want {
				t.Fatalf("HookTimeoutFromEnv(%q) = %s, want %s", tt.env, got, tt.want)
			}
		})
	}
}

func TestShouldRestartDaemon(t *testing.T) {
	t.Run("not running returns false", func(t *testing.T) {
		withOwnedDaemonProcess(t, func(string) bool { return true })
		if shouldRestartDaemon(t.TempDir(), time.Now()) {
			t.Fatal("expected false when daemon is not running")
		}
	})

	t.Run("running with fresh state returns false", func(t *testing.T) {
		withOwnedDaemonProcess(t, func(string) bool { return true })
		root := t.TempDir()
		writeWatchState(t, root, watch.State{
			UpdatedAt: time.Now().Add(-5 * time.Minute),
			FileCount: 10,
		})
		if shouldRestartDaemon(root, time.Now()) {
			t.Fatal("expected false for fresh daemon state")
		}
	})

	t.Run("running with stale state returns true", func(t *testing.T) {
		withOwnedDaemonProcess(t, func(string) bool { return true })
		root := t.TempDir()
		writeWatchState(t, root, watch.State{
			UpdatedAt: time.Now().Add(-3 * time.Hour),
			FileCount: 10,
		})
		if !shouldRestartDaemon(root, time.Now()) {
			t.Fatal("expected true for stale daemon state")
		}
	})

	t.Run("running without state returns true", func(t *testing.T) {
		withOwnedDaemonProcess(t, func(string) bool { return true })
		root := t.TempDir()
		codemapDir := filepath.Join(root, ".codemap")
		if err := os.MkdirAll(codemapDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := watch.WritePID(root); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { watch.RemovePID(root) })

		if !shouldRestartDaemon(root, time.Now()) {
			t.Fatal("expected true when daemon pid exists but state is missing")
		}
	})

	t.Run("running stale state but daemon not owned returns false", func(t *testing.T) {
		withOwnedDaemonProcess(t, func(string) bool { return false })
		root := t.TempDir()
		writeWatchState(t, root, watch.State{
			UpdatedAt: time.Now().Add(-3 * time.Hour),
			FileCount: 10,
		})
		if shouldRestartDaemon(root, time.Now()) {
			t.Fatal("expected false for unowned daemon process")
		}
	})
}

// TestExtractFilePath tests JSON parsing for file_path extraction
func TestExtractFilePath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantPath string
		wantErr  bool
	}{
		{
			name:     "valid JSON with file_path",
			input:    `{"file_path": "/path/to/file.go", "other": "data"}`,
			wantPath: "/path/to/file.go",
			wantErr:  false,
		},
		{
			name:     "valid JSON without file_path",
			input:    `{"other": "data"}`,
			wantPath: "",
			wantErr:  false,
		},
		{
			name:     "empty JSON object",
			input:    `{}`,
			wantPath: "",
			wantErr:  false,
		},
		{
			name:     "file_path with spaces",
			input:    `{"file_path": "/path/to/my file.go"}`,
			wantPath: "/path/to/my file.go",
			wantErr:  false,
		},
		{
			name:     "nested structure - tool_input",
			input:    `{"tool_name": "Edit", "tool_input": {"file_path": "/src/main.go"}}`,
			wantPath: "", // current impl doesn't handle nested
			wantErr:  false,
		},
		{
			name:     "regex fallback for malformed JSON",
			input:    `not json but has "file_path": "/fallback/path.go" in it`,
			wantPath: "/fallback/path.go",
			wantErr:  false,
		},
		{
			name:     "completely invalid input",
			input:    `random garbage`,
			wantPath: "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseFilePathFromJSON([]byte(tt.input))
			if (err != nil) != tt.wantErr {
				t.Errorf("parseFilePathFromJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.wantPath {
				t.Errorf("parseFilePathFromJSON() = %q, want %q", got, tt.wantPath)
			}
		})
	}
}

// parseFilePathFromJSON is a testable version of the JSON parsing logic
// This mirrors the logic in extractFilePathFromStdin
func parseFilePathFromJSON(input []byte) (string, error) {
	var data map[string]interface{}
	if err := json.Unmarshal(input, &data); err != nil {
		// Try regex fallback for non-JSON or partial JSON
		re := regexp.MustCompile(`"file_path"\s*:\s*"([^"]+)"`)
		matches := re.FindSubmatch(input)
		if len(matches) >= 2 {
			return string(matches[1]), nil
		}
		return "", err
	}

	filePath, ok := data["file_path"].(string)
	if !ok {
		return "", nil
	}

	return filePath, nil
}

// TestCheckFileImportersOutput tests the output format for different scenarios
func TestCheckFileImportersOutput(t *testing.T) {
	tests := []struct {
		name           string
		info           *hubInfo
		filePath       string
		wantContains   []string
		wantNotContain []string
	}{
		{
			name: "hub file with many importers",
			info: &hubInfo{
				Importers: map[string][]string{
					"types.go": {"a.go", "b.go", "c.go", "d.go", "e.go", "f.go"},
				},
				Imports: map[string][]string{},
			},
			filePath: "types.go",
			wantContains: []string{
				"HUB FILE",
				"types.go",
				"Imported by 6 files",
				"wide impact",
				"Dependents:",
			},
		},
		{
			name: "non-hub file with some importers",
			info: &hubInfo{
				Importers: map[string][]string{
					"utils.go": {"main.go", "cmd.go"},
				},
				Imports: map[string][]string{},
			},
			filePath: "utils.go",
			wantContains: []string{
				"File:",
				"utils.go",
				"Imported by 2 file(s)",
			},
			wantNotContain: []string{
				"HUB FILE",
				"wide impact",
			},
		},
		{
			name: "file with no importers",
			info: &hubInfo{
				Importers: map[string][]string{},
				Imports:   map[string][]string{},
			},
			filePath:       "lonely.go",
			wantContains:   []string{}, // should produce no output
			wantNotContain: []string{"HUB FILE", "File:", "Imported by"},
		},
		{
			name: "file that imports hubs",
			info: &hubInfo{
				Importers: map[string][]string{
					"types.go": {"a.go", "b.go", "c.go", "main.go"},
				},
				Imports: map[string][]string{
					"main.go": {"types.go"},
				},
			},
			filePath: "main.go",
			wantContains: []string{
				"Imports 1 hub(s)",
				"types.go",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := captureOutput(func() {
				formatFileImportersOutput(tt.info, tt.filePath)
			})

			for _, want := range tt.wantContains {
				if !strings.Contains(output, want) {
					t.Errorf("output should contain %q, got:\n%s", want, output)
				}
			}

			for _, notWant := range tt.wantNotContain {
				if strings.Contains(output, notWant) {
					t.Errorf("output should NOT contain %q, got:\n%s", notWant, output)
				}
			}
		})
	}
}

// formatFileImportersOutput is the core output logic extracted for testing
// This mirrors checkFileImporters but takes hubInfo directly instead of calling getHubInfo
func formatFileImportersOutput(info *hubInfo, filePath string) {
	if info == nil {
		return
	}

	importers := info.Importers[filePath]
	if len(importers) >= 3 {
		fmt.Println()
		fmt.Printf("⚠️  HUB FILE: %s\n", filePath)
		fmt.Printf("   Imported by %d files - changes have wide impact!\n", len(importers))
		fmt.Println()
		fmt.Println("   Dependents:")
		for i, imp := range importers {
			if i >= 5 {
				fmt.Printf("   ... and %d more\n", len(importers)-5)
				break
			}
			fmt.Printf("   • %s\n", imp)
		}
		fmt.Println()
	} else if len(importers) > 0 {
		fmt.Println()
		fmt.Printf("📍 File: %s\n", filePath)
		fmt.Printf("   Imported by %d file(s): %s\n", len(importers), strings.Join(importers, ", "))
		fmt.Println()
	}

	// Also check if this file imports any hubs
	imports := info.Imports[filePath]
	var hubImports []string
	for _, imp := range imports {
		if info.isHub(imp) {
			hubImports = append(hubImports, imp)
		}
	}
	if len(hubImports) > 0 {
		fmt.Printf("   Imports %d hub(s): %s\n", len(hubImports), strings.Join(hubImports, ", "))
		fmt.Println()
	}
}

// TestPromptFileMentionDetection tests the regex patterns for detecting file mentions
func TestPromptFileMentionDetection(t *testing.T) {
	tests := []struct {
		name       string
		prompt     string
		wantFiles  []string
		wantNoFile bool
	}{
		{
			name:      "go file mention",
			prompt:    "can you check main.go for errors",
			wantFiles: []string{"main.go"},
		},
		{
			name:      "path with directories",
			prompt:    "look at scanner/types.go",
			wantFiles: []string{"scanner/types.go"},
		},
		{
			name:      "multiple file mentions",
			prompt:    "compare main.go with cmd/root.go and utils.py",
			wantFiles: []string{"main.go", "cmd/root.go", "utils.py"},
		},
		{
			name:      "tsx file",
			prompt:    "fix the bug in components/Button.tsx",
			wantFiles: []string{"components/Button.tsx"},
		},
		{
			name:      "jsx file",
			prompt:    "update App.jsx component",
			wantFiles: []string{"App.jsx"},
		},
		{
			name:       "no file mentions",
			prompt:     "how do I run the tests?",
			wantNoFile: true,
		},
		{
			name:      "file with underscores",
			prompt:    "update the my_module.py file",
			wantFiles: []string{"my_module.py"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filesMentioned := extractMentionedFiles(tt.prompt, 10)

			if tt.wantNoFile {
				if len(filesMentioned) > 0 {
					t.Errorf("expected no files, got: %v", filesMentioned)
				}
				return
			}

			for _, want := range tt.wantFiles {
				found := false
				for _, got := range filesMentioned {
					if got == want {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected to find %q in %v", want, filesMentioned)
				}
			}
		})
	}
}

func TestExtractMentionedFilesLimitAndDedup(t *testing.T) {
	prompt := "check main.go then main.go and api/server.go and util/file.py"
	files := extractMentionedFiles(prompt, 10)
	// Should deduplicate main.go and find all 3 unique files
	if len(files) != 3 {
		t.Fatalf("expected 3 unique files, got %d: %v", len(files), files)
	}
	seen := make(map[string]bool)
	for _, f := range files {
		seen[f] = true
	}
	for _, want := range []string{"main.go", "api/server.go", "util/file.py"} {
		if !seen[want] {
			t.Errorf("missing expected file %q in %v", want, files)
		}
	}

	// Limit should cap results
	limited := extractMentionedFiles(prompt, 2)
	if len(limited) != 2 {
		t.Fatalf("expected 2 files with limit=2, got %d: %v", len(limited), limited)
	}
}

func TestMatchSubsystemRoutes(t *testing.T) {
	cfg := config.ProjectConfig{
		Routing: config.RoutingConfig{
			Retrieval: config.RetrievalConfig{Strategy: "keyword", TopK: 2},
			Subsystems: []config.Subsystem{
				{
					ID:       "watching",
					Keywords: []string{"hook", "daemon", "events"},
					Docs:     []string{"docs/HOOKS.md"},
					Agents:   []string{"codemap-hook-triage"},
				},
				{
					ID:       "handoff",
					Keywords: []string{"handoff", "delta"},
					Docs:     []string{"README.md"},
				},
			},
		},
	}

	prompt := "the daemon hook events log is too noisy, handoff delta might also be stale"
	matches := matchSubsystemRoutes(prompt, cfg, cfg.RoutingTopKOrDefault())
	if len(matches) != 2 {
		t.Fatalf("expected top_k=2 matches, got %d: %+v", len(matches), matches)
	}
	if matches[0].ID != "watching" {
		t.Fatalf("expected highest score route to be watching, got %+v", matches[0])
	}
	if len(matches[0].Docs) != 1 || matches[0].Docs[0] != "docs/HOOKS.md" {
		t.Fatalf("unexpected watching docs: %+v", matches[0].Docs)
	}
}

// TestHubInfoWithMultipleHubs tests scenarios with multiple hub files
func TestHubInfoWithMultipleHubs(t *testing.T) {
	info := &hubInfo{
		Hubs: []string{"types.go", "utils.go", "config.go"},
		Importers: map[string][]string{
			"types.go":  {"a.go", "b.go", "c.go", "d.go", "e.go"},
			"utils.go":  {"a.go", "b.go", "c.go"},
			"config.go": {"a.go", "b.go", "c.go", "d.go"},
			"main.go":   {"cmd.go"}, // not a hub
		},
		Imports: map[string][]string{
			"main.go": {"types.go", "utils.go", "config.go"},
		},
	}

	// types.go should be a hub
	if !info.isHub("types.go") {
		t.Error("types.go should be a hub")
	}

	// main.go should not be a hub
	if info.isHub("main.go") {
		t.Error("main.go should not be a hub")
	}

	// Check hub import detection
	var hubImports []string
	for _, imp := range info.Imports["main.go"] {
		if info.isHub(imp) {
			hubImports = append(hubImports, imp)
		}
	}

	if len(hubImports) != 3 {
		t.Errorf("main.go should import 3 hubs, got %d: %v", len(hubImports), hubImports)
	}
}

func TestHandoffHasChangedFiles(t *testing.T) {
	tests := []struct {
		name     string
		artifact *handoff.Artifact
		want     bool
	}{
		{
			name:     "nil artifact",
			artifact: nil,
			want:     false,
		},
		{
			name: "legacy changed files",
			artifact: &handoff.Artifact{
				ChangedFiles: []string{"main.go"},
			},
			want: true,
		},
		{
			name: "delta changed stubs",
			artifact: &handoff.Artifact{
				Delta: handoff.DeltaSnapshot{
					Changed: []handoff.FileStub{{Path: "main.go"}},
				},
			},
			want: true,
		},
		{
			name: "no changed files",
			artifact: &handoff.Artifact{
				Delta: handoff.DeltaSnapshot{
					Changed: []handoff.FileStub{},
				},
				ChangedFiles: []string{},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := handoffHasChangedFiles(tt.artifact)
			if got != tt.want {
				t.Fatalf("handoffHasChangedFiles() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHandoffMatchesBranch(t *testing.T) {
	tests := []struct {
		name          string
		artifact      *handoff.Artifact
		currentBranch string
		branchKnown   bool
		want          bool
	}{
		{
			name:          "nil artifact",
			artifact:      nil,
			currentBranch: "feature/a",
			branchKnown:   true,
			want:          false,
		},
		{
			name: "matching branch",
			artifact: &handoff.Artifact{
				Branch: "feature/a",
			},
			currentBranch: "feature/a",
			branchKnown:   true,
			want:          true,
		},
		{
			name: "different branch",
			artifact: &handoff.Artifact{
				Branch: "feature/old",
			},
			currentBranch: "feature/new",
			branchKnown:   true,
			want:          false,
		},
		{
			name: "unknown current branch",
			artifact: &handoff.Artifact{
				Branch: "feature/a",
			},
			currentBranch: "",
			branchKnown:   false,
			want:          false,
		},
		{
			name: "trimmed whitespace matches",
			artifact: &handoff.Artifact{
				Branch: " feature/a ",
			},
			currentBranch: "feature/a",
			branchKnown:   true,
			want:          true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := handoffMatchesBranch(tt.artifact, tt.currentBranch, tt.branchKnown)
			if got != tt.want {
				t.Fatalf("handoffMatchesBranch() = %v, want %v", got, tt.want)
			}
		})
	}
}

// captureOutput captures stdout during function execution
func captureOutput(f func()) string {
	old := os.Stdout
	outFile, err := os.CreateTemp("", "codemap-cmd-output-*")
	if err != nil {
		panic(err)
	}
	defer os.Remove(outFile.Name())
	func() {
		defer func() {
			_ = outFile.Close()
			os.Stdout = old
		}()
		os.Stdout = outFile
		f()
	}()

	data, err := os.ReadFile(outFile.Name())
	if err != nil {
		panic(err)
	}
	return string(data)
}

// writeWatchState writes a watch.State JSON file to <root>/.codemap/state.json
// and a PID file pointing to the current process so IsRunning returns true.
func writeWatchState(t *testing.T, root string, state watch.State) {
	t.Helper()
	codemapDir := filepath.Join(root, ".codemap")
	if err := os.MkdirAll(codemapDir, 0755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codemapDir, "state.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := watch.WritePID(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { watch.RemovePID(root) })
}

// TestGetLastSessionEvents verifies that the 20-line budget is enforced when
// reading the events log, protecting hook startup output from bloating context.
func TestGetLastSessionEvents(t *testing.T) {
	t.Run("missing file returns nil", func(t *testing.T) {
		if got := getLastSessionEvents(t.TempDir()); got != nil {
			t.Errorf("expected nil for missing file, got %v", got)
		}
	})

	t.Run("empty file returns nil", func(t *testing.T) {
		root := t.TempDir()
		codemapDir := filepath.Join(root, ".codemap")
		if err := os.MkdirAll(codemapDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(codemapDir, "events.log"), []byte(""), 0644); err != nil {
			t.Fatal(err)
		}
		if got := getLastSessionEvents(root); got != nil {
			t.Errorf("expected nil for empty file, got %v", got)
		}
	})

	t.Run("returns all lines when fewer than 20", func(t *testing.T) {
		root := t.TempDir()
		codemapDir := filepath.Join(root, ".codemap")
		os.MkdirAll(codemapDir, 0755)
		lines := "ts|WRITE|a.go\nts|WRITE|b.go\nts|WRITE|c.go"
		os.WriteFile(filepath.Join(codemapDir, "events.log"), []byte(lines), 0644)

		got := getLastSessionEvents(root)
		if len(got) != 3 {
			t.Errorf("expected 3 events, got %d: %v", len(got), got)
		}
	})

	t.Run("caps at 20 lines for large log (context bloat protection)", func(t *testing.T) {
		root := t.TempDir()
		codemapDir := filepath.Join(root, ".codemap")
		os.MkdirAll(codemapDir, 0755)

		var sb strings.Builder
		for i := 1; i <= 35; i++ {
			fmt.Fprintf(&sb, "ts|WRITE|file%02d.go\n", i)
		}
		os.WriteFile(filepath.Join(codemapDir, "events.log"), []byte(sb.String()), 0644)

		got := getLastSessionEvents(root)
		if len(got) != 20 {
			t.Errorf("expected 20 events (cap), got %d", len(got))
		}
		// Should be the *last* 20 (file16.go through file35.go).
		if !strings.Contains(got[0], "file16.go") {
			t.Errorf("expected first retained event to be file16.go, got %q", got[0])
		}
		if !strings.Contains(got[19], "file35.go") {
			t.Errorf("expected last retained event to be file35.go, got %q", got[19])
		}
	})

	t.Run("reads only tail of huge event log and still returns latest 20", func(t *testing.T) {
		root := t.TempDir()
		codemapDir := filepath.Join(root, ".codemap")
		os.MkdirAll(codemapDir, 0755)

		var sb strings.Builder
		for i := 1; i <= 15000; i++ {
			fmt.Fprintf(&sb, "ts|WRITE|file%05d.go\n", i)
		}
		os.WriteFile(filepath.Join(codemapDir, "events.log"), []byte(sb.String()), 0644)

		got := getLastSessionEvents(root)
		if len(got) != 20 {
			t.Fatalf("expected 20 events from huge log tail, got %d", len(got))
		}
		if !strings.Contains(got[0], "file14981.go") {
			t.Errorf("expected first retained event to be file14981.go, got %q", got[0])
		}
		if !strings.Contains(got[19], "file15000.go") {
			t.Errorf("expected last retained event to be file15000.go, got %q", got[19])
		}
	})

	t.Run("skips blank lines when counting", func(t *testing.T) {
		root := t.TempDir()
		codemapDir := filepath.Join(root, ".codemap")
		os.MkdirAll(codemapDir, 0755)
		// 5 real entries surrounded by blank lines
		content := "\nts|WRITE|a.go\n\nts|WRITE|b.go\n\n\nts|WRITE|c.go\n\nts|WRITE|d.go\nts|WRITE|e.go\n"
		os.WriteFile(filepath.Join(codemapDir, "events.log"), []byte(content), 0644)

		got := getLastSessionEvents(root)
		if len(got) != 5 {
			t.Errorf("expected 5 non-blank lines, got %d: %v", len(got), got)
		}
	})
}

// TestShowLastSessionContext verifies the 5-file truncation that prevents
// large working-set context from bloating the session start output.
func TestShowLastSessionContext(t *testing.T) {
	t.Run("empty events list produces no output", func(t *testing.T) {
		out := captureOutput(func() { showLastSessionContext("", []string{}) })
		if out != "" {
			t.Errorf("expected no output for empty events, got %q", out)
		}
	})

	t.Run("malformed lines are skipped gracefully", func(t *testing.T) {
		events := []string{"just one part", "two|parts", ""}
		out := captureOutput(func() { showLastSessionContext("", events) })
		if out != "" {
			t.Errorf("expected no output for malformed events, got %q", out)
		}
	})

	t.Run("shows header and file list for valid events", func(t *testing.T) {
		events := []string{
			"ts|WRITE|alpha.go",
			"ts|WRITE|beta.go",
			"ts|WRITE|gamma.go",
		}
		out := captureOutput(func() { showLastSessionContext("", events) })
		if !strings.Contains(out, "Last session worked on") {
			t.Errorf("expected header, got %q", out)
		}
		if strings.Contains(out, "more files") {
			t.Errorf("should not show truncation indicator for <=5 files, got %q", out)
		}
		if !strings.Contains(out, "alpha.go") {
			t.Errorf("expected alpha.go in output, got %q", out)
		}
	})

	t.Run("truncates at 5 files with 'more files' indicator (context bloat protection)", func(t *testing.T) {
		var events []string
		for i := 1; i <= 8; i++ {
			events = append(events, fmt.Sprintf("ts|WRITE|file%d.go", i))
		}
		out := captureOutput(func() { showLastSessionContext("", events) })
		if !strings.Contains(out, "and 3 more files") {
			t.Errorf("expected '3 more files' truncation, got %q", out)
		}
	})

	t.Run("REMOVE for still-existing file is reported as 'edited'", func(t *testing.T) {
		root := t.TempDir()
		fileName := "survivor.go"
		os.WriteFile(filepath.Join(root, fileName), []byte("package main\n"), 0644)

		events := []string{fmt.Sprintf("ts|REMOVE|%s", fileName)}
		out := captureOutput(func() { showLastSessionContext(root, events) })
		if !strings.Contains(out, "edited") {
			t.Errorf("expected 'edited' for extant file after REMOVE event, got %q", out)
		}
	})
}

// TestShowSessionProgress verifies that in-session hub-edit statistics are
// reported accurately, exposing hub churn as a context-bloat signal.
func TestShowSessionProgress(t *testing.T) {
	t.Run("no daemon state produces no output", func(t *testing.T) {
		out := captureOutput(func() { showSessionProgress(t.TempDir()) })
		if out != "" {
			t.Errorf("expected no output with no state, got %q", out)
		}
	})

	t.Run("state with no events produces no output", func(t *testing.T) {
		root := t.TempDir()
		writeWatchState(t, root, watch.State{
			UpdatedAt: time.Now(),
			FileCount: 5,
		})
		out := captureOutput(func() { showSessionProgress(root) })
		if out != "" {
			t.Errorf("expected no output for state with no events, got %q", out)
		}
	})

	t.Run("shows files-edited count", func(t *testing.T) {
		root := t.TempDir()
		writeWatchState(t, root, watch.State{
			UpdatedAt: time.Now(),
			FileCount: 10,
			RecentEvents: []watch.Event{
				{Path: "main.go", Op: "WRITE"},
				{Path: "utils.go", Op: "WRITE"},
				{Path: "main.go", Op: "WRITE"}, // duplicate — same file
			},
		})
		out := captureOutput(func() { showSessionProgress(root) })
		if !strings.Contains(out, "2 files edited") {
			t.Errorf("expected '2 files edited', got %q", out)
		}
	})

	t.Run("reports hub-edit count to surface risky churn", func(t *testing.T) {
		root := t.TempDir()
		writeWatchState(t, root, watch.State{
			UpdatedAt: time.Now(),
			FileCount: 10,
			RecentEvents: []watch.Event{
				{Path: "types.go", Op: "WRITE", IsHub: true},
				{Path: "utils.go", Op: "WRITE", IsHub: false},
				{Path: "types.go", Op: "WRITE", IsHub: true},
			},
		})
		out := captureOutput(func() { showSessionProgress(root) })
		if !strings.Contains(out, "1 hub edits") {
			t.Errorf("expected '1 hub edits' (unique hub files, not events), got %q", out)
		}
		if !strings.Contains(out, "2 files edited") {
			t.Errorf("expected '2 files edited', got %q", out)
		}
	})

	t.Run("omits hub-edit label when there are none", func(t *testing.T) {
		root := t.TempDir()
		writeWatchState(t, root, watch.State{
			UpdatedAt: time.Now(),
			FileCount: 5,
			RecentEvents: []watch.Event{
				{Path: "main.go", Op: "WRITE", IsHub: false},
			},
		})
		out := captureOutput(func() { showSessionProgress(root) })
		if strings.Contains(out, "hub edits") {
			t.Errorf("should not mention hub edits when count is 0, got %q", out)
		}
		if !strings.Contains(out, "1 files edited") {
			t.Errorf("expected '1 files edited', got %q", out)
		}
	})
}

// TestGetHubInfoNoDeps verifies the guard that prevents expensive fallback scans
// when a daemon state exists but lacks dependency graph data (large repos).
func TestGetHubInfoNoDeps(t *testing.T) {
	t.Run("state with all-empty deps returns nil (avoids expensive scan)", func(t *testing.T) {
		root := t.TempDir()
		writeWatchState(t, root, watch.State{
			UpdatedAt: time.Now(),
			FileCount: 2000,
			// Hubs, Importers, and Imports are all nil/empty
		})
		if info := getHubInfo(root); info != nil {
			t.Errorf("expected nil when state has no dep graph, got %+v", info)
		}
	})

	t.Run("state with hub data returns populated hubInfo", func(t *testing.T) {
		root := t.TempDir()
		writeWatchState(t, root, watch.State{
			UpdatedAt: time.Now(),
			FileCount: 5,
			Hubs:      []string{"types.go"},
			Importers: map[string][]string{
				"types.go": {"a.go", "b.go", "c.go"},
			},
		})
		info := getHubInfo(root)
		if info == nil {
			t.Fatal("expected hubInfo when state has dep graph")
		}
		if len(info.Hubs) != 1 || info.Hubs[0] != "types.go" {
			t.Errorf("expected [types.go] hubs, got %v", info.Hubs)
		}
		if len(info.Importers["types.go"]) != 3 {
			t.Errorf("expected 3 importers for types.go, got %v", info.Importers["types.go"])
		}
	})

	t.Run("state only with imports (no hubs) also returns hubInfo", func(t *testing.T) {
		root := t.TempDir()
		writeWatchState(t, root, watch.State{
			UpdatedAt: time.Now(),
			FileCount: 5,
			Imports: map[string][]string{
				"main.go": {"types.go"},
			},
		})
		if info := getHubInfo(root); info == nil {
			t.Error("expected hubInfo when state has at least one imports entry")
		}
	})

	t.Run("no fallback path skips expensive fresh scan", func(t *testing.T) {
		root := t.TempDir()
		if info := getHubInfoNoFallback(root); info != nil {
			t.Errorf("expected nil when no daemon state and fallback disabled, got %+v", info)
		}
	})
}

// TestHookPreCompact verifies that pre-compact saves hub state and prints the
// correct output, while silently skipping when no hubs exist.
func TestHookPreCompact(t *testing.T) {
	t.Run("no hubs: no output and no hubs.txt created", func(t *testing.T) {
		root := t.TempDir()
		// All-empty state → getHubInfo returns nil → hookPreCompact exits early.
		writeWatchState(t, root, watch.State{
			UpdatedAt: time.Now(),
			FileCount: 5,
		})

		out := captureOutput(func() {
			if err := hookPreCompact(root); err != nil {
				t.Errorf("hookPreCompact returned error: %v", err)
			}
		})

		if out != "" {
			t.Errorf("expected no output when no hubs, got %q", out)
		}
		hubsFile := filepath.Join(root, ".codemap", "hubs.txt")
		if _, err := os.Stat(hubsFile); !os.IsNotExist(err) {
			t.Error("expected no hubs.txt when hub list is empty")
		}
	})

	t.Run("with hubs: creates hubs.txt and prints count", func(t *testing.T) {
		root := t.TempDir()
		writeWatchState(t, root, watch.State{
			UpdatedAt: time.Now(),
			FileCount: 20,
			Hubs:      []string{"types.go", "utils.go", "config.go"},
			Importers: map[string][]string{
				"types.go":  {"a.go", "b.go", "c.go"},
				"utils.go":  {"a.go", "b.go", "c.go"},
				"config.go": {"a.go", "b.go", "c.go"},
			},
		})

		out := captureOutput(func() {
			if err := hookPreCompact(root); err != nil {
				t.Errorf("hookPreCompact returned error: %v", err)
			}
		})

		if !strings.Contains(out, "3 hub files tracked") {
			t.Errorf("expected hub count in output, got %q", out)
		}
		if !strings.Contains(out, "hubs.txt") {
			t.Errorf("expected hubs.txt mention, got %q", out)
		}

		hubsFile := filepath.Join(root, ".codemap", "hubs.txt")
		content, err := os.ReadFile(hubsFile)
		if err != nil {
			t.Fatalf("expected hubs.txt to be created: %v", err)
		}
		for _, hub := range []string{"types.go", "utils.go", "config.go"} {
			if !strings.Contains(string(content), hub) {
				t.Errorf("expected %q in hubs.txt, got %q", hub, content)
			}
		}
	})
}

// TestShowRecentHandoffSummary verifies the handoff output and its
// MaxHandoffCompactBytes guard against context bloat.
func TestShowRecentHandoffSummary(t *testing.T) {
	t.Run("nil artifact produces no output", func(t *testing.T) {
		out := captureOutput(func() { showRecentHandoffSummary(nil) })
		if out != "" {
			t.Errorf("expected no output for nil artifact, got %q", out)
		}
	})

	t.Run("artifact with changed files shows header and file", func(t *testing.T) {
		a := &handoff.Artifact{
			Branch:  "feature/test",
			BaseRef: "main",
			Delta: handoff.DeltaSnapshot{
				Changed: []handoff.FileStub{
					{Path: "cmd/hooks.go", Status: "modified"},
				},
			},
		}
		out := captureOutput(func() { showRecentHandoffSummary(a) })
		if !strings.Contains(out, "Recent handoff") {
			t.Errorf("expected 'Recent handoff' header, got %q", out)
		}
		if !strings.Contains(out, "feature/test") {
			t.Errorf("expected branch name, got %q", out)
		}
	})

	t.Run("large summary is truncated to MaxHandoffCompactBytes (context bloat protection)", func(t *testing.T) {
		// Build a NextSteps list large enough to overflow MaxHandoffCompactBytes (3000 bytes).
		var nextSteps []string
		for i := 0; i < 200; i++ {
			nextSteps = append(nextSteps, fmt.Sprintf("Step %03d: do the thing with the long description that adds bytes %s", i, strings.Repeat("x", 30)))
		}
		a := &handoff.Artifact{
			Branch:  "feature/huge",
			BaseRef: "main",
			Delta: handoff.DeltaSnapshot{
				NextSteps: nextSteps,
			},
		}

		// Confirm the raw compact render is actually over budget before asserting truncation.
		raw := handoff.RenderCompact(a, 200)
		if len(raw) <= limits.MaxHandoffCompactBytes {
			t.Skipf("test precondition not met: raw compact render is only %d bytes (need >%d)", len(raw), limits.MaxHandoffCompactBytes)
		}

		out := captureOutput(func() { showRecentHandoffSummary(a) })
		if !strings.Contains(out, "truncated") {
			t.Errorf("expected truncation indicator in oversized output, got %d bytes", len(out))
		}
	})

	t.Run("artifact with risk files includes them in summary", func(t *testing.T) {
		a := &handoff.Artifact{
			Branch:  "feature/risky",
			BaseRef: "main",
			Delta: handoff.DeltaSnapshot{
				Changed: []handoff.FileStub{{Path: "types.go", Status: "modified"}},
				RiskFiles: []handoff.RiskFile{
					{Path: "types.go", Importers: 10, IsHub: true, Reason: "hub"},
				},
			},
		}
		out := captureOutput(func() { showRecentHandoffSummary(a) })
		if !strings.Contains(out, "types.go") {
			t.Errorf("expected risk file in summary output, got %q", out)
		}
	})
}
