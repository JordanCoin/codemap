package render

import (
	"os"
	"testing"
)

// TestMain points HOME at a temp dir so state never lands in the real
// ~/.codemap.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "codemap-render-home-*")
	if err != nil {
		os.Exit(1)
	}
	os.Setenv("HOME", dir)
	os.Setenv("USERPROFILE", dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
