package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func runGitTestCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}

func makeRepoOnBranch(t *testing.T, branch string) string {
	t.Helper()
	root := t.TempDir()

	runGitTestCmd(t, root, "init")

	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGitTestCmd(t, root, "add", ".")
	runGitTestCmd(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")
	runGitTestCmd(t, root, "branch", "-M", branch)

	return root
}

func TestResolveHandoffBaseRef(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	t.Run("prefers main branch when present", func(t *testing.T) {
		root := makeRepoOnBranch(t, "main")
		got := resolveHandoffBaseRef(root)
		if got != "main" {
			t.Fatalf("expected base ref main, got %q", got)
		}
	})

	t.Run("falls back to master when main is absent", func(t *testing.T) {
		root := makeRepoOnBranch(t, "master")
		got := resolveHandoffBaseRef(root)
		if got != "master" {
			t.Fatalf("expected base ref master, got %q", got)
		}
	})

	t.Run("falls back to HEAD when no known default branch exists", func(t *testing.T) {
		root := makeRepoOnBranch(t, "feature/no-default")
		got := resolveHandoffBaseRef(root)
		if got != "HEAD" {
			t.Fatalf("expected base ref HEAD, got %q", got)
		}
	})
}
