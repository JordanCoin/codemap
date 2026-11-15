package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"codemap/internal/gitexclude"
)

// gitIgnoresState reports whether git ignores machine state under .codemap/.
func gitIgnoresState(t *testing.T, root string) bool {
	t.Helper()
	cmd := exec.Command("git", "check-ignore", "-q", ".codemap/state.json")
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

func TestReportEnsureIgnoredWritesStateIgnoreOnlyWhenSetUp(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := makeGitRepo(t)
	var out bytes.Buffer
	reportEnsureIgnored(&out, root)
	if _, err := os.Stat(filepath.Join(root, ".codemap")); !os.IsNotExist(err) {
		t.Fatalf("created .codemap/ in a repo that is not set up: %v", err)
	}

	if err := os.Mkdir(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	reportEnsureIgnored(&out, root)
	data, err := os.ReadFile(filepath.Join(root, ".codemap", ".gitignore"))
	if err != nil || string(data) != gitexclude.StateIgnore {
		t.Fatalf(".codemap/.gitignore = %q, %v", data, err)
	}
	if !strings.Contains(out.String(), "Wrote .codemap/.gitignore") || !gitIgnoresState(t, root) {
		t.Fatalf("state not ignored after report:\n%s", out.String())
	}
	exclude, _ := os.ReadFile(filepath.Join(root, ".git", "info", "exclude"))
	if gitexclude.HasLine(exclude, gitexclude.Entry) {
		t.Fatalf("info/exclude gained %s:\n%s", gitexclude.Entry, exclude)
	}
}

// appendIgnoreEntry must fail cleanly when the ignore file cannot be read
// (here: a directory), not silently succeed or corrupt anything.
func TestAppendIgnoreEntryReadError(t *testing.T) {
	if err := appendIgnoreEntry(t.TempDir(), ignoreEntry); err == nil {
		t.Fatal("expected an error reading a directory as an ignore file")
	}
}
