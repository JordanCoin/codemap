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

func TestStateIgnoreHidesLegacyLeftoversAndLeavesExclude(t *testing.T) {
	root := freshClone(t)
	exclude, err := InfoExcludePath(root)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(exclude)
	stateDir := filepath.Join(root, ".codemap", "projects", "abc")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if wrote, err := EnsureStateIgnore(filepath.Join(root, ".codemap")); err != nil || !wrote {
		t.Fatalf("EnsureStateIgnore = %v, %v", wrote, err)
	}
	for path, content := range map[string]string{
		filepath.Join(stateDir, "state.json"):                         "{}",
		filepath.Join(root, ".codemap", "agent_edits.jsonl"):          "{}",
		filepath.Join(root, ".codemap", "config.json"):                "{}",
		filepath.Join(root, ".codemap", "skills", "mine", "SKILL.md"): "x",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	status := gitRun(t, root, "status", "--porcelain", "--untracked-files=all")
	for _, want := range []string{".codemap/.gitignore", ".codemap/config.json", ".codemap/skills/mine/SKILL.md"} {
		if !strings.Contains(status, want) {
			t.Fatalf("git status should offer %s for commit:\n%s", want, status)
		}
	}
	for _, gone := range []string{"state.json", "agent_edits"} {
		if strings.Contains(status, gone) {
			t.Fatalf("machine state %s is not ignored:\n%s", gone, status)
		}
	}
	if after, _ := os.ReadFile(exclude); string(after) != string(before) {
		t.Fatalf("info/exclude changed:\n%s", after)
	}
}

func TestEnsureStateIgnoreKeepsUserCopy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(path, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if wrote, err := EnsureStateIgnore(dir); err != nil || wrote {
		t.Fatalf("EnsureStateIgnore = %v, %v; want no write", wrote, err)
	}
	if data, _ := os.ReadFile(path); string(data) != "mine\n" {
		t.Fatalf("user .gitignore rewritten: %q", data)
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
