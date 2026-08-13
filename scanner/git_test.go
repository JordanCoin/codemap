package scanner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupGitRepo creates a temporary git repository for testing
func setupGitRepo(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()

	// Initialize git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Skipf("git not available: %v", err)
	}

	// Configure git user for commits
	cmd = exec.Command("git", "config", "user.email", "test@test.com")
	cmd.Dir = tmpDir
	cmd.Run()

	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = tmpDir
	cmd.Run()

	return tmpDir
}

func TestDiffStat(t *testing.T) {
	// Test DiffStat struct
	stat := DiffStat{Added: 10, Removed: 5}
	if stat.Added != 10 {
		t.Errorf("Expected Added=10, got %d", stat.Added)
	}
	if stat.Removed != 5 {
		t.Errorf("Expected Removed=5, got %d", stat.Removed)
	}
}

func TestDiffInfo(t *testing.T) {
	// Test DiffInfo struct initialization
	info := &DiffInfo{
		Changed:   make(map[string]bool),
		Untracked: make(map[string]bool),
		Stats:     make(map[string]DiffStat),
	}

	info.Changed["test.go"] = true
	info.Untracked["new.go"] = true
	info.Stats["test.go"] = DiffStat{Added: 5, Removed: 2}

	if !info.Changed["test.go"] {
		t.Error("Expected test.go in Changed")
	}
	if !info.Untracked["new.go"] {
		t.Error("Expected new.go in Untracked")
	}
	if info.Stats["test.go"].Added != 5 {
		t.Error("Expected Added=5 for test.go")
	}
}

func TestGitDiffFilesInRepo(t *testing.T) {
	tmpDir := setupGitRepo(t)

	// Create initial file and commit
	initialFile := filepath.Join(tmpDir, "initial.go")
	if err := os.WriteFile(initialFile, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("git", "add", ".")
	cmd.Dir = tmpDir
	cmd.Run()

	cmd = exec.Command("git", "commit", "-m", "initial")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Skipf("Could not create initial commit: %v", err)
	}

	// Create main branch reference
	cmd = exec.Command("git", "branch", "-M", "main")
	cmd.Dir = tmpDir
	cmd.Run()

	// Modify the file
	if err := os.WriteFile(initialFile, []byte("package main\n\nfunc main() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a new untracked file
	newFile := filepath.Join(tmpDir, "new.go")
	if err := os.WriteFile(newFile, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Get diff info
	info, err := GitDiffInfo(context.Background(), tmpDir, "main")
	if err != nil {
		t.Fatalf("GitDiffInfo failed: %v", err)
	}

	// Should have both files as changed
	if !info.Changed["initial.go"] {
		t.Error("Expected initial.go in changed files")
	}
	if !info.Changed["new.go"] {
		t.Error("Expected new.go in changed files")
	}

	// Only new.go should be untracked
	if info.Untracked["initial.go"] {
		t.Error("initial.go should not be untracked")
	}
	if !info.Untracked["new.go"] {
		t.Error("new.go should be untracked")
	}

	// Check stats for modified file
	if stat, ok := info.Stats["initial.go"]; ok {
		if stat.Added == 0 {
			t.Error("Expected some added lines for initial.go")
		}
	}
}

func TestGitDiffFilesHelper(t *testing.T) {
	tmpDir := setupGitRepo(t)

	// Create and commit a file
	if err := os.WriteFile(filepath.Join(tmpDir, "test.go"), []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("git", "add", ".")
	cmd.Dir = tmpDir
	cmd.Run()

	cmd = exec.Command("git", "commit", "-m", "test")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Skip("Could not create commit")
	}

	cmd = exec.Command("git", "branch", "-M", "main")
	cmd.Dir = tmpDir
	cmd.Run()

	// Use GitDiffInfo helper
	info, err := GitDiffInfo(context.Background(), tmpDir, "main")
	if err != nil {
		t.Fatalf("GitDiffInfo failed: %v", err)
	}

	// No changes since last commit
	if len(info.Changed) != 0 {
		t.Errorf("Expected no changes, got %v", info.Changed)
	}

	// Modify file
	if err := os.WriteFile(filepath.Join(tmpDir, "test.go"), []byte("modified"), 0644); err != nil {
		t.Fatal(err)
	}

	info, err = GitDiffInfo(context.Background(), tmpDir, "main")
	if err != nil {
		t.Fatalf("GitDiffInfo failed: %v", err)
	}

	if !info.Changed["test.go"] {
		t.Error("Expected test.go in changed files")
	}
}

func TestGitDiffStatsHelper(t *testing.T) {
	tmpDir := setupGitRepo(t)

	// Create and commit a file
	if err := os.WriteFile(filepath.Join(tmpDir, "test.go"), []byte("line1\nline2\nline3\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("git", "add", ".")
	cmd.Dir = tmpDir
	cmd.Run()

	cmd = exec.Command("git", "commit", "-m", "initial")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Skip("Could not create commit")
	}

	cmd = exec.Command("git", "branch", "-M", "main")
	cmd.Dir = tmpDir
	cmd.Run()

	// Modify file (add and remove lines)
	if err := os.WriteFile(filepath.Join(tmpDir, "test.go"), []byte("line1\nmodified\nline3\nnew line\n"), 0644); err != nil {
		t.Fatal(err)
	}

	stats, err := GitDiffStats(context.Background(), tmpDir, "main")
	if err != nil {
		t.Fatalf("GitDiffStats failed: %v", err)
	}

	if stat, ok := stats["test.go"]; ok {
		if stat.Added == 0 && stat.Removed == 0 {
			t.Error("Expected some diff stats for test.go")
		}
	} else {
		t.Error("Expected test.go in stats")
	}
}

func TestGitDiffInfoInvalidRef(t *testing.T) {
	tmpDir := setupGitRepo(t)

	const ref = "nonexistent-branch-xyz"
	// Try to diff against nonexistent ref
	_, err := GitDiffInfo(context.Background(), tmpDir, ref)
	if err == nil {
		t.Fatal("expected invalid ref error")
	}
	if !strings.Contains(err.Error(), ref) {
		t.Fatalf("expected Git stderr to identify %q, got %q", ref, err)
	}
}

func TestImpactInfo(t *testing.T) {
	// Test ImpactInfo struct
	impact := ImpactInfo{
		File:   "util.go",
		UsedBy: 5,
	}

	if impact.File != "util.go" {
		t.Errorf("Expected File=util.go, got %s", impact.File)
	}
	if impact.UsedBy != 5 {
		t.Errorf("Expected UsedBy=5, got %d", impact.UsedBy)
	}
}

func TestAnalyzeImpactEmpty(t *testing.T) {
	// Test with empty changed files
	impacts, err := AnalyzeImpact(context.Background(), ".", nil)
	if err != nil {
		t.Fatalf("AnalyzeImpact() error: %v", err)
	}
	if impacts != nil {
		t.Errorf("Expected nil impacts for empty input, got %v", impacts)
	}

	impacts, err = AnalyzeImpact(context.Background(), ".", []FileInfo{})
	if err != nil {
		t.Fatalf("AnalyzeImpact() error: %v", err)
	}
	if impacts != nil {
		t.Errorf("Expected nil impacts for empty slice, got %v", impacts)
	}
}

func TestGitDiffInfoIgnoresStderrWarningsOnSuccess(t *testing.T) {
	// git warns on stderr ("refname 'dup' is ambiguous.") with exit 0 when a
	// branch and a tag share a name; the warning must not become a changed file.
	root := setupGitRepo(t)
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("config", "user.email", "t@t")
	git("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "f.txt")
	git("commit", "-qm", "one")
	git("branch", "dup")
	git("tag", "-m", "t", "dup")
	// An uncommitted change makes `git diff dup` emit a real row alongside the warning.
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("x\ny\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	info, err := GitDiffInfo(context.Background(), root, "dup")
	if err != nil {
		t.Fatalf("GitDiffInfo: %v", err)
	}
	if !info.Changed["f.txt"] {
		t.Fatalf("expected f.txt in changed set, got %v", info.Changed)
	}
	if len(info.Changed) != 1 {
		t.Fatalf("changed set = %v, want exactly {f.txt} (stderr warning leaked into the parser)", info.Changed)
	}
}
