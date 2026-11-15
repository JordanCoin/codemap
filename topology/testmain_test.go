package topology

import (
	"os"
	"testing"
)

// TestMain keeps state of un-set-up test repos out of the real ~/.codemap.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "codemap-topology-cache-*")
	if err != nil {
		os.Exit(1)
	}
	os.Setenv("HOME", dir)
	os.Setenv("USERPROFILE", dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
