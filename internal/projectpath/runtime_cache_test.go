package projectpath

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSelectRuntimeRefusesPlantedStateSymlink(t *testing.T) {
	root := makeProjectFixture(t)
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	planted := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cache, "codemap"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(planted, filepath.Join(cache, "codemap", ProjectKey(root))); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := SelectRuntime(root); err == nil {
		t.Fatal("SelectRuntime accepted a symlinked project state directory")
	}
	if _, err := CheckedRuntimeCodemapDir(root); err == nil {
		t.Fatal("CheckedRuntimeCodemapDir accepted a symlinked project state directory")
	}
	if entries, _ := os.ReadDir(planted); len(entries) != 0 {
		t.Fatalf("wrote through the planted link: %v", entries)
	}
}

func TestSelectRuntimeWithoutCacheDirIsUnavailable(t *testing.T) {
	root := makeProjectFixture(t)
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("LOCALAPPDATA", "")
	if _, ok := StateCacheDir(); ok {
		t.Skip("this OS still reports a user cache dir")
	}
	if _, err := SelectRuntime(root); !errors.Is(err, ErrNoStateDir) {
		t.Fatalf("SelectRuntime err = %v, want ErrNoStateDir", err)
	}
}

func TestSelectRuntimeUsesCacheUntilSetUp(t *testing.T) {
	root := makeProjectFixture(t)
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	_ = os.RemoveAll(filepath.Join(root, ".codemap"))

	selection, err := SelectRuntime(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(CanonicalPath(cache), "codemap", ProjectKey(root)); selection.RuntimeDir != want {
		t.Fatalf("RuntimeDir = %q, want %q", selection.RuntimeDir, want)
	}
	for _, dir := range []string{filepath.Dir(selection.RuntimeDir), selection.RuntimeDir} {
		if info, err := os.Stat(dir); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o700) {
			t.Fatalf("%s: mode %v, err %v; want 0700", dir, info.Mode().Perm(), err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".codemap")); !os.IsNotExist(err) {
		t.Fatalf("selection created .codemap/: %v", err)
	}

	if err := os.Mkdir(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	selection, err = SelectRuntime(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(selection.RuntimeDir, string(filepath.Separator)+".codemap") || !IsSetUp(root) {
		t.Fatalf("set-up RuntimeDir = %q, want <root>/.codemap", selection.RuntimeDir)
	}
}
