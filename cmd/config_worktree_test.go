package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"codemap/config"
)

// Issue #152: from a linked git worktree, config resolution follows the
// primary checkout's .codemap/ (shared policy is intentional), but `config
// init` must never write into that other checkout, and `config show` must
// say where the config it reports actually lives.
func TestConfigInitRefusesToWriteIntoPrimaryFromWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	primary := makeRepoOnBranch(t, "main")
	// A primary with codemap state but no config.json is the case where
	// init from the worktree used to land in the primary.
	if err := os.MkdirAll(filepath.Join(primary, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(primary, ".codemap", "status"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitTestCmd(t, primary, "worktree", "add", "--detach", worktree)

	_, err := initProjectConfig(worktree)
	if err == nil {
		t.Fatal("config init from a linked worktree succeeded; it must refuse so it never writes into the primary checkout")
	}
	if !strings.Contains(err.Error(), "linked worktree") {
		t.Fatalf("error does not name the worktree situation: %v", err)
	}
	if _, statErr := os.Stat(config.ConfigPath(worktree)); statErr == nil {
		t.Fatalf("config init wrote %s from inside the worktree", config.ConfigPath(worktree))
	}

	out := captureOutput(func() { configShow(worktree) })
	if !strings.Contains(out, "primary checkout") {
		t.Fatalf("config show did not disclose that config resolves to the primary checkout:\n%s", out)
	}
}
