package gitexclude

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func freshClone(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	src := t.TempDir()
	gitRun(t, src, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, src, "add", ".")
	gitRun(t, src, "commit", "-q", "-m", "init")
	clone := filepath.Join(t.TempDir(), "clone")
	gitRun(t, src, "clone", "-q", src, clone)
	return clone
}

func TestMkdirAllIgnoresNewStateDirInFreshClone(t *testing.T) {
	root := freshClone(t)
	stateDir := filepath.Join(root, ".codemap", "projects", "abc")

	if err := MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "state.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if status := gitRun(t, root, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Fatalf("git status not clean after state write:\n%s", status)
	}
	if _, err := os.Stat(filepath.Join(root, ".gitignore")); !os.IsNotExist(err) {
		t.Fatal("must never create a tracked .gitignore")
	}
	exclude, err := InfoExcludePath(root)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exclude)
	if err != nil {
		t.Fatal(err)
	}
	if !HasLine(data, Entry) {
		t.Fatalf("info/exclude missing %q:\n%s", Entry, data)
	}
}

func TestMkdirAllAddsNoLocalRuleWhenTrackedIgnoreCovers(t *testing.T) {
	root := freshClone(t)
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".codemap/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, root, "add", ".gitignore")
	gitRun(t, root, "commit", "-q", "-m", "ignore")

	if err := MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	exclude, err := InfoExcludePath(root)
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(exclude); err == nil && HasLine(data, Entry) {
		t.Fatalf("local rule written although .gitignore already ignores .codemap/:\n%s", data)
	}
	if status := gitRun(t, root, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Fatalf("git status not clean:\n%s", status)
	}
}

func TestMkdirAllOutsideGitAndWithoutStateDir(t *testing.T) {
	plain := filepath.Join(t.TempDir(), "a", ".codemap", "b")
	if err := MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(plain); err != nil || !info.IsDir() {
		t.Fatalf("dir not created: %v", err)
	}
	other := filepath.Join(t.TempDir(), "cache", "x")
	if err := MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := stateDirOf(other); ok {
		t.Fatalf("stateDirOf(%q) found a .codemap ancestor", other)
	}
	if got, ok := stateDirOf(plain); !ok || filepath.Base(got) != ".codemap" {
		t.Fatalf("stateDirOf(%q) = %q, %v", plain, got, ok)
	}
}

func TestEnsureIgnoredIfNeededUsesSharedExcludeInLinkedWorktree(t *testing.T) {
	root := freshClone(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitRun(t, root, "worktree", "add", "-q", wt, "HEAD")

	wrote, err := EnsureIgnoredIfNeeded(wt)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, ".git", "info", "exclude")
	if wrote != want {
		t.Fatalf("wrote %q, want the primary's shared exclude %q", wrote, want)
	}
	if err := os.MkdirAll(filepath.Join(wt, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".codemap", "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if status := gitRun(t, wt, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Fatalf("worktree git status not clean:\n%s", status)
	}
	again, err := EnsureIgnoredIfNeeded(wt)
	if err != nil {
		t.Fatal(err)
	}
	if again != "" {
		t.Fatalf("second call wrote %q, want nothing", again)
	}
}

func TestTrackedStateFiles(t *testing.T) {
	root := freshClone(t)
	files, err := TrackedStateFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("tracked = %v, want none", files)
	}
	if err := os.MkdirAll(filepath.Join(root, ".codemap", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"config.json", "skills/x.md"} {
		if err := os.WriteFile(filepath.Join(root, ".codemap", filepath.FromSlash(name)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitRun(t, root, "add", "-f", ".codemap")
	files, err = TrackedStateFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("tracked = %v, want 2 entries", files)
	}
}
