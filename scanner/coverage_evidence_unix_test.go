//go:build !windows

package scanner

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestGraphBuildSurvivesNonRegularSourceNames(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"f.sh", "f.php", "f.rb", "composer.json", "launcher"} {
		if err := syscall.Mkfifo(filepath.Join(dir, name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("f.sh", filepath.Join(dir, "g.sh")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/dev/zero", filepath.Join(dir, "z.php")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.sh"), []byte("source \"$X/y.sh\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := BuildFileGraph(ctx, dir, Filters{})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("graph build hung on a non-regular file")
	}
	if got, whole := readProjectFile(dir, "z.php"); got != nil || !whole {
		t.Fatalf("read %d bytes through a link to /dev/zero, whole = %v", len(got), whole)
	}
}

func TestReadProjectFileBoundsLargeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.sh")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, maxEvidenceFileBytes+1); err != nil {
		t.Fatal(err)
	}
	if got, whole := readProjectFile(dir, "big.sh"); len(got) != maxEvidenceFileBytes || whole {
		t.Fatalf("read %d bytes, whole = %v, want %d and false", len(got), whole, maxEvidenceFileBytes)
	}
}
