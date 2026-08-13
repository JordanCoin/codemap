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

	t.Run("writes to info/exclude and not gitignore", func(t *testing.T) {
		root := makeGitRepo(t)

		path, err := ensureCodemapIgnored(root)
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
			t.Fatal("must not create a tracked .gitignore")
		}
		if !gitIgnoresCodemap(t, root) {
			t.Fatal("git does not ignore .codemap after info/exclude write")
		}
	})

	t.Run("no-op when already ignored", func(t *testing.T) {
		root := makeGitRepo(t)
		exclude, err := infoExcludePath(root)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(exclude, []byte("node_modules/\n.codemap/\n"), 0644); err != nil {
			t.Fatal(err)
		}
		before, _ := os.ReadFile(exclude)

		path, err := ensureCodemapIgnored(root)
		if err != nil {
			t.Fatalf("ensureCodemapIgnored: %v", err)
		}
		if path != "" {
			t.Fatalf("path = %q, want empty (nothing written)", path)
		}
		after, _ := os.ReadFile(exclude)
		if string(before) != string(after) {
			t.Fatalf("exclude was modified:\nbefore:\n%s\nafter:\n%s", before, after)
		}
	})

	t.Run("records the local rule even when tracked ignore is effective", func(t *testing.T) {
		root := makeGitRepo(t)
		if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".codemap/\n"), 0644); err != nil {
			t.Fatal(err)
		}

		path, err := ensureCodemapIgnored(root)
		if err != nil {
			t.Fatalf("ensureCodemapIgnored: %v", err)
		}
		if path == "" {
			t.Fatal("must record its own entry, not rely on .gitignore")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read exclude: %v", err)
		}
		if !hasIgnoreLine(data, ignoreEntry) {
			t.Fatalf("info/exclude missing %q:\n%s", ignoreEntry, data)
		}
	})

	t.Run("skips silently when not a git repo", func(t *testing.T) {
		root := t.TempDir()
		path, err := ensureCodemapIgnored(root)
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

// TestEnsureCodemapIgnoredLocalAppends locks the append path: whatever the
// existing exclude content (line endings, missing final newline, oversized
// lines, other rules), .codemap/ lands on its own line exactly once and the
// existing bytes are preserved.
func TestEnsureCodemapIgnoredLocalAppends(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	longLine := strings.Repeat("x", 70*1024) // beyond bufio.Scanner's 64KiB limit
	cases := []struct {
		name    string
		pre     string // pre-existing info/exclude content
		already bool   // pre already lists .codemap/
	}{
		{name: "empty file"},
		{name: "pre-populated entries", pre: "build/\n*.log\n"},
		{name: "no trailing newline", pre: "build/\n*.log"},
		{name: "crlf", pre: "build/\r\n*.log\r\n"},
		{name: "crlf no trailing newline", pre: "build/\r\n*.log"},
		{name: "long first line", pre: longLine + "\n"},
		{name: "already listed after long line", pre: longLine + "\n.codemap/\n", already: true},
		{name: "already listed mid-file", pre: "node_modules/\n.codemap/\n*.log\n", already: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := makeGitRepo(t)
			exclude, err := infoExcludePath(root)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(exclude, []byte(tc.pre), 0644); err != nil {
				t.Fatal(err)
			}

			path, err := ensureCodemapIgnored(root)
			if err != nil {
				t.Fatalf("ensureCodemapIgnored: %v", err)
			}
			if tc.already {
				if path != "" {
					t.Fatalf("path = %q, want empty (nothing written)", path)
				}
				data, err := os.ReadFile(exclude)
				if err != nil {
					t.Fatal(err)
				}
				if string(data) != tc.pre {
					t.Fatalf("exclude changed when entry present:\n%q", data)
				}
			} else {
				if path == "" {
					t.Fatal("expected the ignore entry to be written")
				}
				want := tc.pre
				if want != "" && !strings.HasSuffix(want, "\n") {
					want += "\n"
				}
				want += ".codemap/\n"
				data, err := os.ReadFile(exclude)
				if err != nil {
					t.Fatal(err)
				}
				if string(data) != want {
					t.Fatalf("exclude = %q, want %q", data, want)
				}
			}
			if !gitIgnoresCodemap(t, root) {
				t.Fatal("git does not ignore .codemap after write")
			}
		})
	}
}

func TestReportEnsureIgnored(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	t.Run("prints notice and writes rule on first run", func(t *testing.T) {
		root := makeGitRepo(t)
		var buf bytes.Buffer

		reportEnsureIgnored(&buf, root)

		out := buf.String()
		if !strings.Contains(out, "Added .codemap/ to .git/info/exclude") {
			t.Fatalf("notice missing or malformed:\n%s", out)
		}
		if !gitIgnoresCodemap(t, root) {
			t.Fatal("git does not ignore .codemap after reportEnsureIgnored")
		}
	})

	t.Run("prints nothing when already ignored", func(t *testing.T) {
		root := makeGitRepo(t)
		exclude, err := infoExcludePath(root)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(exclude, []byte(".codemap/\n"), 0644); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer

		reportEnsureIgnored(&buf, root)

		if buf.Len() != 0 {
			t.Fatalf("expected no output, got:\n%s", buf.String())
		}
	})

	t.Run("prints nothing outside a git repo", func(t *testing.T) {
		root := t.TempDir()
		var buf bytes.Buffer

		reportEnsureIgnored(&buf, root)

		if buf.Len() != 0 {
			t.Fatalf("expected no output for non-git dir, got:\n%s", buf.String())
		}
	})

	// In a linked worktree the shared exclude lives in the main repo, outside
	// the worktree root, so the notice must print the absolute path rather
	// than an escaping relative one like ../<main>/.git/info/exclude.
	t.Run("prints absolute path for shared exclude in a linked worktree", func(t *testing.T) {
		root := makeRepoOnBranch(t, "main")
		wt := filepath.Join(t.TempDir(), "wt")
		runGitTestCmd(t, root, "worktree", "add", "-b", "wt-branch", wt)
		// The shared exclude resolves outside the worktree root (into the main
		// repo), so the notice must print that absolute path, not ../<main>/...
		sharedExclude, err := infoExcludePath(wt)
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer

		reportEnsureIgnored(&buf, wt)

		out := buf.String()
		if !strings.Contains(out, "Added .codemap/ to "+sharedExclude) {
			t.Fatalf("expected absolute path in notice, got:\n%s", out)
		}
		if strings.Contains(out, "../") {
			t.Fatalf("notice must not print an escaping relative path:\n%s", out)
		}
		if !gitIgnoresCodemap(t, wt) {
			t.Fatal("git does not ignore .codemap in the worktree after reportEnsureIgnored")
		}
	})

	// A corrupt local ignore (unreadable, or unwritable) must surface as a
	// warning instead of a silent success or a crash.
	t.Run("warns when the exclude file cannot be read", func(t *testing.T) {
		root := makeGitRepo(t)
		exclude, err := infoExcludePath(root)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(exclude); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(exclude, 0o755); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer

		reportEnsureIgnored(&buf, root)

		if !strings.Contains(buf.String(), "Warning: could not update ignore rules") {
			t.Fatalf("expected warning, got:\n%s", buf.String())
		}
	})
}

func TestEnsureCodemapIgnoredLocalModeIsIdempotent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := makeGitRepo(t)
	exclude, err := infoExcludePath(root)
	if err != nil {
		t.Fatal(err)
	}

	first, err := ensureCodemapIgnored(root)
	if err != nil {
		t.Fatalf("first ensureCodemapIgnored: %v", err)
	}
	if first == "" {
		t.Fatal("expected the local ignore entry to be written on the first call")
	}
	second, err := ensureCodemapIgnored(root)
	if err != nil {
		t.Fatalf("second ensureCodemapIgnored: %v", err)
	}
	if second != "" {
		t.Fatalf("second call wrote again (%q); local mode must be idempotent", second)
	}
	data, err := os.ReadFile(exclude)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), ".codemap/\n"); n != 1 {
		t.Fatalf("exclude has %d .codemap/ entries, want exactly 1:\n%s", n, data)
	}
}

// appendIgnoreEntry must fail cleanly when the ignore file cannot be read
// (here: a directory), not silently succeed or corrupt anything.
func TestAppendIgnoreEntryReadError(t *testing.T) {
	if err := appendIgnoreEntry(t.TempDir(), ignoreEntry); err == nil {
		t.Fatal("expected an error reading a directory as an ignore file")
	}
}
