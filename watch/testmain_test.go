package watch

import (
	"os"
	"testing"
)

// TestMain keeps daemon registrations made by tests out of the user's
// ~/.codemap/daemons. Helper subprocesses inherit the variable and skip this.
func TestMain(m *testing.M) {
	if os.Getenv(daemonRegistryEnv) != "" {
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "codemap-watch-daemons-*")
	if err != nil {
		os.Exit(1)
	}
	os.Setenv(daemonRegistryEnv, dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
