package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestUntrackedDirsListsOnlyWhollyUntrackedDirs(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"tracked/a.go", "tracked/new/b.go", "mixed/c.go", "mixed/d.go", "scratch/deep/e.yml"} {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "tracked/a.go", "mixed/c.go"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	got := untrackedDirs(root)
	if !got["scratch"] || !got["tracked/new"] || got["mixed"] || got["tracked"] || len(got) != 2 {
		t.Fatalf("untrackedDirs = %v, want scratch and tracked/new", got)
	}
	if untrackedDirs(t.TempDir()) != nil {
		t.Fatal("non-repo should give nil")
	}
}
