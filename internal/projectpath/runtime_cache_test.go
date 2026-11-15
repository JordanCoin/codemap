package projectpath

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func setHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

func TestSelectRuntimeWithoutHomeIsUnavailable(t *testing.T) {
	root := makeProjectFixture(t)
	setHome(t, "")
	if _, ok := UserDir(); ok {
		t.Skip("this OS still reports a home dir")
	}
	if _, err := SelectRuntime(root); !errors.Is(err, ErrNoStateDir) {
		t.Fatalf("SelectRuntime err = %v, want ErrNoStateDir", err)
	}
}

// State lives in ~/.codemap/state/<key> whether or not the repo is set up,
// whatever XDG_CACHE_HOME says, and every directory codemap creates is 0700.
func TestSelectRuntimeUsesHomeStateWhetherOrNotSetUp(t *testing.T) {
	root := makeProjectFixture(t)
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	want := filepath.Join(CanonicalPath(home), ".codemap", "state", ProjectKey(root))

	selection, err := SelectRuntime(root)
	if err != nil {
		t.Fatal(err)
	}
	if selection.RuntimeDir != want || selection.ProjectDir != want {
		t.Fatalf("RuntimeDir = %q, ProjectDir = %q, want %q", selection.RuntimeDir, selection.ProjectDir, want)
	}
	for dir := want; dir != CanonicalPath(home); dir = filepath.Dir(dir) {
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
	t.Setenv("XDG_CACHE_HOME", "")
	if selection, err = SelectRuntime(root); err != nil || selection.RuntimeDir != want || !IsSetUp(root) {
		t.Fatalf("set-up RuntimeDir = %q, %v; want %q", selection.RuntimeDir, err, want)
	}
	if entries, _ := os.ReadDir(filepath.Join(root, ".codemap")); len(entries) != 0 {
		t.Fatalf("selection wrote into the set-up repo's .codemap/: %v", entries)
	}
}

// A link at .codemap/state, or at any level below it, is refused and nothing
// is written through it. A link at ~/.codemap itself is the user's.
func TestSelectRuntimeRefusesLinksFromTheStateLevelDown(t *testing.T) {
	root := makeProjectFixture(t)
	key := ProjectKey(root)
	for _, rel := range []string{filepath.Join(".codemap", "state"), filepath.Join(".codemap", "state", key)} {
		t.Run(rel, func(t *testing.T) {
			home := t.TempDir()
			setHome(t, home)
			if err := os.MkdirAll(filepath.Dir(filepath.Join(home, rel)), 0o700); err != nil {
				t.Fatal(err)
			}
			planted := t.TempDir()
			if err := os.Symlink(planted, filepath.Join(home, rel)); err != nil {
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

	home, real := t.TempDir(), t.TempDir()
	setHome(t, home)
	if err := os.Symlink(real, filepath.Join(home, ".codemap")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := CheckedRuntimeCodemapDir(root); err != nil {
		t.Fatalf("a symlinked ~/.codemap must work: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(real, "state", key)); err != nil {
		t.Fatalf("state not under the link's target: %v", err)
	}
}

// Every failure to get the state dir is ErrNoStateDir, and codemap never
// creates a missing home: at most ~/.codemap.
func TestSelectRuntimeNeverCreatesAMissingHome(t *testing.T) {
	root := makeProjectFixture(t)
	home := filepath.Join(t.TempDir(), "gone")
	setHome(t, home)
	if _, err := SelectRuntime(root); !errors.Is(err, ErrNoStateDir) {
		t.Fatalf("err = %v, want ErrNoStateDir", err)
	}
	if _, err := os.Lstat(home); !os.IsNotExist(err) {
		t.Fatalf("created the missing home: %v", err)
	}
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		return
	}
	readOnly := t.TempDir()
	if err := os.Chmod(readOnly, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0o755) })
	setHome(t, readOnly)
	if _, err := SelectRuntime(root); !errors.Is(err, ErrNoStateDir) {
		t.Fatalf("read-only home: err = %v, want ErrNoStateDir", err)
	}
}
