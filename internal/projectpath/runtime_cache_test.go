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

// State lives in the cache whether or not the repo is set up, and every
// directory codemap creates there is 0700.
func TestSelectRuntimeUsesCacheWhetherOrNotSetUp(t *testing.T) {
	root := makeProjectFixture(t)
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	want := filepath.Join(CanonicalPath(cache), "codemap", ProjectKey(root))

	selection, err := SelectRuntime(root)
	if err != nil {
		t.Fatal(err)
	}
	if selection.RuntimeDir != want {
		t.Fatalf("RuntimeDir = %q, want %q", selection.RuntimeDir, want)
	}
	projectDir, err := CheckedRuntimeCodemapDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for dir := projectDir; dir != CanonicalPath(cache); dir = filepath.Dir(dir) {
		if info, err := os.Lstat(dir); err != nil || !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o700) {
			t.Fatalf("%s: %v, err %v; want a 0700 directory", dir, info, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".codemap")); !os.IsNotExist(err) {
		t.Fatalf("selection created .codemap/: %v", err)
	}

	if err := os.Mkdir(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	if selection, err = SelectRuntime(root); err != nil || selection.RuntimeDir != want || !IsSetUp(root) {
		t.Fatalf("set-up RuntimeDir = %q, %v; want %q", selection.RuntimeDir, err, want)
	}
	if entries, _ := os.ReadDir(filepath.Join(root, ".codemap")); len(entries) != 0 {
		t.Fatalf("selection wrote into the set-up repo's .codemap/: %v", entries)
	}
}

// V2: a link at the codemap leaf, or at any level below it, is refused, and
// nothing is written through it. A link above the cache root is the user's.
func TestSelectRuntimeRefusesLinksFromTheCodemapLeafDown(t *testing.T) {
	root := makeProjectFixture(t)
	key := ProjectKey(root)
	for _, rel := range []string{"codemap", filepath.Join("codemap", key, "projects"), filepath.Join("codemap", key, "projects", key)} {
		t.Run(rel, func(t *testing.T) {
			cache := t.TempDir()
			t.Setenv("XDG_CACHE_HOME", cache)
			if err := os.MkdirAll(filepath.Dir(filepath.Join(cache, rel)), 0o700); err != nil {
				t.Fatal(err)
			}
			planted := t.TempDir()
			if err := os.Symlink(planted, filepath.Join(cache, rel)); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			if _, err := CheckedRuntimeCodemapDir(root); !errors.Is(err, ErrNoStateDir) || !strings.Contains(err.Error(), "unsafe runtime directory") {
				t.Fatalf("err = %v, want ErrNoStateDir naming an unsafe runtime directory", err)
			}
			if entries, _ := os.ReadDir(planted); len(entries) != 0 {
				t.Fatalf("wrote through the planted link: %v", entries)
			}
		})
	}

	real := t.TempDir()
	linkedCache := filepath.Join(t.TempDir(), "cache")
	if err := os.Symlink(real, linkedCache); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	t.Setenv("XDG_CACHE_HOME", linkedCache)
	if _, err := CheckedRuntimeCodemapDir(root); err != nil {
		t.Fatalf("a symlinked cache root must work: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(real, "codemap", key)); err != nil {
		t.Fatalf("state not under the link's target: %v", err)
	}
}

// V1: every failure to get the state dir is ErrNoStateDir, and codemap never
// creates a missing home: at most the cache dir's last component.
func TestSelectRuntimeNeverCreatesAMissingHome(t *testing.T) {
	root := makeProjectFixture(t)
	home := filepath.Join(t.TempDir(), "gone")
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	if _, err := SelectRuntime(root); !errors.Is(err, ErrNoStateDir) {
		t.Fatalf("err = %v, want ErrNoStateDir", err)
	}
	if _, err := os.Lstat(home); !os.IsNotExist(err) {
		t.Fatalf("created the missing home: %v", err)
	}

	parent := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(parent, ".cache"))
	if _, err := SelectRuntime(root); err != nil {
		t.Fatalf("the cache dir's own missing leaf should be created: %v", err)
	}
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		return
	}
	readOnly := t.TempDir()
	if err := os.Chmod(readOnly, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0o755) })
	t.Setenv("XDG_CACHE_HOME", filepath.Join(readOnly, ".cache"))
	if _, err := SelectRuntime(root); !errors.Is(err, ErrNoStateDir) {
		t.Fatalf("read-only home: err = %v, want ErrNoStateDir", err)
	}
}
