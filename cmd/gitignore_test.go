package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitIgnoresCodemap(t *testing.T, root string) bool {
	t.Helper()
	// Trailing slash so the directory-scoped rule matches even though
	// .codemap does not exist on disk during the test.
	cmd := exec.Command("git", "check-ignore", "-q", ".codemap/")
	cmd.Dir = root
	err := cmd.Run()
	if err == nil {
		return true
	}
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
		return false
	}
	t.Fatalf("git check-ignore failed unexpectedly: %v", err)
	return false
}

func makeGitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	runGitTestCmd(t, root, "init")
	return root
}

func TestEnsureCodemapIgnored(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	t.Run("writes to tracked gitignore when not ignored", func(t *testing.T) {
		root := makeGitRepo(t)

		path, err := ensureCodemapIgnored(root, ignoreTracked)
		if err != nil {
			t.Fatalf("ensureCodemapIgnored: %v", err)
		}
		want := filepath.Join(root, ".gitignore")
		if path != want {
			t.Fatalf("path = %q, want %q", path, want)
		}
		data, err := os.ReadFile(want)
		if err != nil {
			t.Fatalf("read .gitignore: %v", err)
		}
		if !strings.Contains(string(data), ".codemap/") {
			t.Fatalf(".gitignore missing .codemap/ entry:\n%s", data)
		}
		if !gitIgnoresCodemap(t, root) {
			t.Fatal("git still does not ignore .codemap after write")
		}
	})

	t.Run("no-op when already ignored", func(t *testing.T) {
		root := makeGitRepo(t)
		gi := filepath.Join(root, ".gitignore")
		if err := os.WriteFile(gi, []byte("node_modules/\n.codemap/\n"), 0644); err != nil {
			t.Fatal(err)
		}
		before, _ := os.ReadFile(gi)

		path, err := ensureCodemapIgnored(root, ignoreTracked)
		if err != nil {
			t.Fatalf("ensureCodemapIgnored: %v", err)
		}
		if path != "" {
			t.Fatalf("path = %q, want empty (nothing written)", path)
		}
		after, _ := os.ReadFile(gi)
		if string(before) != string(after) {
			t.Fatalf(".gitignore was modified:\nbefore:\n%s\nafter:\n%s", before, after)
		}
	})

	t.Run("creates gitignore when missing", func(t *testing.T) {
		root := makeGitRepo(t)
		if _, err := ensureCodemapIgnored(root, ignoreTracked); err != nil {
			t.Fatalf("ensureCodemapIgnored: %v", err)
		}
		if _, err := os.Stat(filepath.Join(root, ".gitignore")); err != nil {
			t.Fatalf(".gitignore not created: %v", err)
		}
	})

	t.Run("appends preserving existing content and trailing newline", func(t *testing.T) {
		root := makeGitRepo(t)
		gi := filepath.Join(root, ".gitignore")
		// No trailing newline on purpose.
		if err := os.WriteFile(gi, []byte("build/\n*.log"), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := ensureCodemapIgnored(root, ignoreTracked); err != nil {
			t.Fatalf("ensureCodemapIgnored: %v", err)
		}
		data, _ := os.ReadFile(gi)
		got := string(data)
		if !strings.Contains(got, "build/") || !strings.Contains(got, "*.log") {
			t.Fatalf("existing content lost:\n%s", got)
		}
		if !strings.Contains(got, "\n.codemap/\n") {
			t.Fatalf(".codemap/ not on its own line:\n%q", got)
		}
	})

	t.Run("ignoreLocal writes to info/exclude and not gitignore", func(t *testing.T) {
		root := makeGitRepo(t)

		path, err := ensureCodemapIgnored(root, ignoreLocal)
		if err != nil {
			t.Fatalf("ensureCodemapIgnored: %v", err)
		}
		if !strings.HasSuffix(path, filepath.Join("info", "exclude")) {
			t.Fatalf("path = %q, want .git/info/exclude", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read exclude: %v", err)
		}
		if !strings.Contains(string(data), ".codemap/") {
			t.Fatalf("info/exclude missing .codemap/:\n%s", data)
		}
		if _, err := os.Stat(filepath.Join(root, ".gitignore")); !os.IsNotExist(err) {
			t.Fatal("ignoreLocal must not create a tracked .gitignore")
		}
		if !gitIgnoresCodemap(t, root) {
			t.Fatal("git does not ignore .codemap after info/exclude write")
		}
	})

	t.Run("skips silently when not a git repo", func(t *testing.T) {
		root := t.TempDir()
		path, err := ensureCodemapIgnored(root, ignoreTracked)
		if err != nil {
			t.Fatalf("ensureCodemapIgnored on non-git dir: %v", err)
		}
		if path != "" {
			t.Fatalf("path = %q, want empty for non-git dir", path)
		}
		if _, err := os.Stat(filepath.Join(root, ".gitignore")); !os.IsNotExist(err) {
			t.Fatal("must not create .gitignore outside a git repo")
		}
	})
}

func TestReportEnsureIgnored(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	t.Run("prints notice and writes rule on first run", func(t *testing.T) {
		root := makeGitRepo(t)
		var buf bytes.Buffer

		reportEnsureIgnored(&buf, root, ignoreTracked)

		out := buf.String()
		if !strings.Contains(out, "Added .codemap/ to .gitignore") {
			t.Fatalf("notice missing or malformed:\n%s", out)
		}
		if !gitIgnoresCodemap(t, root) {
			t.Fatal("git does not ignore .codemap after reportEnsureIgnored")
		}
	})

	t.Run("prints nothing when already ignored", func(t *testing.T) {
		root := makeGitRepo(t)
		if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".codemap/\n"), 0644); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer

		reportEnsureIgnored(&buf, root, ignoreTracked)

		if buf.Len() != 0 {
			t.Fatalf("expected no output, got:\n%s", buf.String())
		}
	})

	t.Run("prints nothing outside a git repo", func(t *testing.T) {
		root := t.TempDir()
		var buf bytes.Buffer

		reportEnsureIgnored(&buf, root, ignoreTracked)

		if buf.Len() != 0 {
			t.Fatalf("expected no output for non-git dir, got:\n%s", buf.String())
		}
	})
}
