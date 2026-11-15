package handoff

import (
	"os"
	"path/filepath"
	"testing"

	"codemap/internal/projectpath"
)

// The first read after an upgrade finds the handoff an older codemap saved in
// the repo, read-only and never through a link; a state-dir copy wins.
func TestReadLatestFallsBackToTheLegacyInRepoHandoffOnce(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path, branch string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"branch":"`+branch+`"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	branch := func() string {
		t.Helper()
		artifact, err := ReadLatest(root)
		if err != nil {
			t.Fatal(err)
		}
		if artifact == nil {
			return ""
		}
		return artifact.Branch
	}

	top := filepath.Join(root, ".codemap", latestFilename)
	write(top, "top")
	if got := branch(); got != "top" {
		t.Fatalf("branch = %q, want the top-level legacy handoff", got)
	}
	nested := filepath.Join(root, ".codemap", "projects", projectpath.ProjectKey(root), latestFilename)
	write(nested, "nested")
	if got := branch(); got != "nested" {
		t.Fatalf("branch = %q, want the per-project legacy handoff first", got)
	}
	write(LatestPath(root), "state")
	if got := branch(); got != "state" {
		t.Fatalf("branch = %q, want the state-dir handoff to win", got)
	}
	if err := os.Remove(LatestPath(root)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, ".codemap")); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "elsewhere")
	write(filepath.Join(outside, latestFilename), "linked")
	if err := os.Symlink(outside, filepath.Join(root, ".codemap")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if artifact, _ := ReadLatest(root); artifact != nil {
		t.Fatalf("read a legacy handoff through a link: %q", artifact.Branch)
	}
}
