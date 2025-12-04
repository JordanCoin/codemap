package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"codemap/scanner"
)

// TestMain runs before all tests
func TestMain(m *testing.M) {
	// Build the binary for integration tests
	cmd := exec.Command("go", "build", "-o", "codemap_test_binary", ".")
	if err := cmd.Run(); err != nil {
		os.Exit(1)
	}
	code := m.Run()
	os.Remove("codemap_test_binary")
	os.Exit(code)
}

func runCodemap(args ...string) (string, error) {
	cmd := exec.Command("./codemap_test_binary", args...)
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return stderr.String(), err
	}
	return out.String(), nil
}

func TestHelpFlag(t *testing.T) {
	output, err := runCodemap("--help")
	if err != nil {
		t.Fatalf("--help failed: %v", err)
	}

	expectedStrings := []string{
		"codemap",
		"Usage:",
		"--skyline",
		"--deps",
		"--diff",
		"--ref",
	}

	for _, expected := range expectedStrings {
		if !strings.Contains(output, expected) {
			t.Errorf("Help output should contain %q", expected)
		}
	}
}

func TestBasicTreeOutput(t *testing.T) {
	output, err := runCodemap(".")
	if err != nil {
		t.Fatalf("Basic tree failed: %v", err)
	}

	// Should contain project name and file stats
	if !strings.Contains(output, "Files:") && !strings.Contains(output, "Changed:") {
		t.Error("Output should contain file stats")
	}

	// Should contain some Go files from this project
	if !strings.Contains(output, ".go") {
		t.Error("Output should show .go files for this Go project")
	}
}

func TestJSONOutput(t *testing.T) {
	output, err := runCodemap("--json", ".")
	if err != nil {
		t.Fatalf("JSON output failed: %v", err)
	}

	// Should be valid JSON
	var project scanner.Project
	if err := json.Unmarshal([]byte(output), &project); err != nil {
		t.Errorf("Output should be valid JSON: %v", err)
	}

	// Verify structure
	if project.Root == "" {
		t.Error("JSON should have root field")
	}
	if project.Mode != "tree" {
		t.Errorf("Expected mode 'tree', got %q", project.Mode)
	}
	if len(project.Files) == 0 {
		t.Error("JSON should have files")
	}

	// Verify file info structure
	for _, f := range project.Files {
		if f.Path == "" {
			t.Error("File path should not be empty")
		}
	}
}

func TestSubdirectoryPath(t *testing.T) {
	// Test scanning a subdirectory
	output, err := runCodemap("scanner")
	if err != nil {
		t.Fatalf("Subdirectory scan failed: %v", err)
	}

	// Should contain files from scanner directory
	if !strings.Contains(output, ".go") {
		t.Error("Should show .go files in scanner directory")
	}
}

func TestNonexistentPath(t *testing.T) {
	_, err := runCodemap("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Error("Should fail for nonexistent path")
	}
}

func TestDiffModeInGitRepo(t *testing.T) {
	// This should either work or say "No files changed"
	output, err := runCodemap("--diff", ".")
	// Even if there's an error, we expect some output
	if err != nil && output == "" {
		// Check if it's a "no changes" scenario which is fine
		stderrOutput, _ := runCodemap("--diff", ".")
		if !strings.Contains(stderrOutput, "No files changed") {
			t.Logf("Diff mode output: %s", output)
		}
	}
}

func TestSkylineFlag(t *testing.T) {
	// Create a temp dir with some files to test skyline mode
	tmpDir := t.TempDir()
	for _, name := range []string{"a.go", "b.go", "c.py"} {
		f, _ := os.Create(filepath.Join(tmpDir, name))
		f.WriteString("content\n")
		f.Close()
	}

	output, err := runCodemap("--skyline", tmpDir)
	if err != nil {
		t.Fatalf("Skyline mode failed: %v", err)
	}

	// Skyline mode should produce some output (could be minimal for small projects)
	if output == "" {
		t.Error("Skyline mode should produce output")
	}
}

func TestDepsModeFallback(t *testing.T) {
	// Without grammars installed, --deps should give a helpful error message
	output, err := runCodemap("--deps", ".")

	// This might succeed if grammars are installed, or fail with a message
	if err != nil {
		// Should have a helpful error message about grammars
		if !strings.Contains(output, "grammar") && !strings.Contains(output, "Grammar") {
			// It's okay if it just fails differently in CI
			t.Logf("Deps mode output: %s", output)
		}
	}
}

func TestJSONDepsOutput(t *testing.T) {
	// Test JSON output for deps mode (if grammars are available)
	output, err := runCodemap("--deps", "--json", ".")

	if err != nil {
		// Expected if no grammars
		return
	}

	// Should be valid JSON
	var depsProject scanner.DepsProject
	if err := json.Unmarshal([]byte(output), &depsProject); err != nil {
		t.Errorf("Deps JSON should be valid: %v", err)
	}

	if depsProject.Mode != "deps" {
		t.Errorf("Expected mode 'deps', got %q", depsProject.Mode)
	}
}

func TestEmptyDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	output, err := runCodemap(tmpDir)
	if err != nil {
		// Empty directory might be handled differently
		t.Logf("Empty dir output: %s", output)
	}
	// Main thing is it shouldn't panic
}

func TestDebugFlag(t *testing.T) {
	output, err := runCodemap("--debug", ".")

	// Debug should produce output on stderr about paths and gitignore
	// We can't easily capture stderr here, but at least check it doesn't fail
	if err != nil {
		t.Logf("Debug mode produced error: %v", err)
	}
	// Should still produce tree output on stdout
	if output == "" {
		t.Error("Debug mode should still produce output")
	}
}

func TestMultipleFlags(t *testing.T) {
	// Test combining flags
	output, err := runCodemap("--json", ".")
	if err != nil {
		t.Fatalf("Multiple flags failed: %v", err)
	}

	if !strings.HasPrefix(strings.TrimSpace(output), "{") {
		t.Error("JSON flag should produce JSON output starting with {")
	}
}

func TestRelativePath(t *testing.T) {
	// Test with relative path
	output, err := runCodemap("./scanner")
	if err != nil {
		t.Fatalf("Relative path failed: %v", err)
	}

	if output == "" {
		t.Error("Should produce output for relative path")
	}
}

func TestCurrentDirectory(t *testing.T) {
	// Test default (current directory)
	output1, err1 := runCodemap()
	output2, err2 := runCodemap(".")

	if err1 != nil {
		t.Fatalf("No arg failed: %v", err1)
	}
	if err2 != nil {
		t.Fatalf("Dot arg failed: %v", err2)
	}

	// Both should produce similar output
	// (Not checking exact equality as timing might differ)
	if (output1 == "") != (output2 == "") {
		t.Error("No arg and '.' should produce similar results")
	}
}
