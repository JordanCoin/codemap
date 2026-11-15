package projectpath

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
