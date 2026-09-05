//go:build windows

package topology

import "testing"

func TestNormalizeRepoPathRejectsVolumePaths(t *testing.T) {
	if _, err := normalizeRepoPath(`C:\repo`, `D:file.go`); err == nil {
		t.Fatal("drive-relative path succeeded")
	}
}
