package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"

	"codemap/handoff"
)

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

	extensions := []string{"go", "tsx", "ts", "jsx", "js", "py", "rs", "rb", "java", "swift", "kt", "c", "cpp", "h"}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var filesMentioned []string
			for _, ext := range extensions {
				pattern := regexp.MustCompile(`[a-zA-Z0-9_/-]+\.` + ext)
				matches := pattern.FindAllString(tt.prompt, 3)
				filesMentioned = append(filesMentioned, matches...)
			}

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
	r, w, _ := os.Pipe()
	os.Stdout = w

	f()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}
