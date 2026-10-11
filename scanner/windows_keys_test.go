package scanner

import (
	"path/filepath"
	"testing"
)

// Workspace resolvers compare a file's graph key (forward slashes on every
// OS) against roots they derived with filepath.Dir, which is native. On
// Windows that made pathContains false for every member file, so JS and Dart
// workspace imports resolved to nothing (#214). The probes here are native on
// purpose: identical on Unix, backslashed on Windows.
func TestPathContainsAcceptsNativeProbes(t *testing.T) {
	root := filepath.FromSlash("packages/ui")
	for _, file := range []string{"packages/ui/src/index.ts", filepath.FromSlash("packages/ui/src/index.ts")} {
		if !pathContains(root, file) {
			t.Fatalf("pathContains(%q, %q) = false, want true", root, file)
		}
	}
	if pathContains(root, "packages/ui-kit/src/index.ts") {
		t.Fatalf("pathContains matched a sibling with the same prefix")
	}
	if got := cleanRepoPath(filepath.FromSlash("packages/ui/")); got != "packages/ui" {
		t.Fatalf("cleanRepoPath = %q, want the slash key", got)
	}
}
