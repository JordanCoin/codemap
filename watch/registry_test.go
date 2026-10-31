package watch

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDaemonRegistryTracksPublishedPIDsAndPrunesDeadOnes is #155 defect 2:
// publishing a pid registers it, removing or stopping it unregisters it, and a
// dead daemon disappears from the list on the next read.
func TestDaemonRegistryTracksPublishedPIDsAndPrunesDeadOnes(t *testing.T) {
	t.Setenv(daemonRegistryEnv, t.TempDir())
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}

	pid, stop := startDaemonLookalike(t, root)
	if err := WriteProcessPID(root, pid); err != nil {
		t.Fatal(err)
	}
	daemons := ListDaemons()
	if len(daemons) != 1 || daemons[0].PID != pid || daemons[0].Root != root || daemons[0].Started.IsZero() {
		t.Fatalf("ListDaemons() = %+v, want the published daemon for %s", daemons, root)
	}
	stop()
	if daemons := ListDaemons(); len(daemons) != 0 {
		t.Fatalf("dead daemon still listed: %+v", daemons)
	}

	pid, _ = startDaemonLookalike(t, root)
	if err := WriteProcessPID(root, pid); err != nil {
		t.Fatal(err)
	}
	if err := RemoveProcessPID(root, pid); err != nil {
		t.Fatal(err)
	}
	if daemons := ListDaemons(); len(daemons) != 0 {
		t.Fatalf("removed pid still registered: %+v", daemons)
	}

	pid, _ = startDaemonLookalike(t, root)
	if err := WriteProcessPID(root, pid); err != nil {
		t.Fatal(err)
	}
	if err := StopDaemon(DaemonInfo{PID: pid, Root: root}); err != nil {
		t.Fatalf("StopDaemon() = %v", err)
	}
	if processAlive(pid) {
		t.Fatal("StopDaemon returned with the daemon alive")
	}
	if daemons := ListDaemons(); len(daemons) != 0 {
		t.Fatalf("stopped daemon still registered: %+v", daemons)
	}
}
