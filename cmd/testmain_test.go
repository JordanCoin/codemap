package cmd

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestMain keeps daemon registrations made by tests out of the user's
// ~/.codemap/daemons. Helper subprocesses inherit the variable and skip this.
func TestMain(m *testing.M) {
	// Hooks exec os.Executable() as codemap, which here is this test binary. A
	// positional first argument such as `watch start` ends flag parsing, so
	// without this exit the child reruns the whole suite, spawning its own child.
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-test.") {
		os.Exit(0)
	}
	if os.Getenv("CODEMAP_DAEMON_REGISTRY") != "" {
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "codemap-cmd-daemons-*")
	if err != nil {
		os.Exit(1)
	}
	os.Setenv("CODEMAP_DAEMON_REGISTRY", dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func TestSelfExecDoesNotRerunTheSuite(t *testing.T) {
	const probeEnv = "CODEMAP_SELF_EXEC_PROBE"
	if os.Getenv(probeEnv) != "" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(exe, "watch", "start", t.TempDir())
	child.Env = append(os.Environ(), probeEnv+"=1")
	if out, err := child.CombinedOutput(); err != nil || len(out) != 0 {
		t.Fatalf("self-exec ran test code: err=%v output:\n%s", err, out)
	}
}
