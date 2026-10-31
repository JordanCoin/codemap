package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"codemap/internal/projectpath"
	"codemap/watch"
)

func writeLease(t *testing.T, root, name string, modTime time.Time) {
	t.Helper()
	dir, err := projectpath.CheckedRuntimeCodemapDir(root)
	if err != nil {
		t.Fatal(err)
	}
	leases := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(leases, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(leases, name)
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
}

// DaemonShouldExit is the reaper for daemons nobody stops (#155): stale
// leases or a superseding daemon mean exit; no leases or a fresh lease mean
// stay.
func TestDaemonShouldExit(t *testing.T) {
	now := time.Now()

	t.Run("no leases: manual daemon stays", func(t *testing.T) {
		if DaemonShouldExit(t.TempDir(), now) {
			t.Fatal("daemon with no leases must not exit")
		}
	})

	t.Run("fresh lease stays", func(t *testing.T) {
		root := t.TempDir()
		writeLease(t, root, "old.json", now.Add(-2*sessionLeaseTTL))
		writeLease(t, root, "fresh.json", now.Add(-time.Hour))
		if DaemonShouldExit(root, now) {
			t.Fatal("one fresh lease must keep the daemon alive")
		}
	})

	t.Run("only stale leases exit", func(t *testing.T) {
		root := t.TempDir()
		writeLease(t, root, "old.json", now.Add(-sessionLeaseTTL-time.Minute))
		if !DaemonShouldExit(root, now) {
			t.Fatal("stale-only leases must exit")
		}
	})

	t.Run("prompt refreshes the lease", func(t *testing.T) {
		root := t.TempDir()
		touchSessionLease(root, "missing") // no lease file: no-op, no panic
		writeLease(t, root, sessionLeaseName("s1"), now.Add(-sessionLeaseTTL-time.Minute))
		if !DaemonShouldExit(root, now) {
			t.Fatal("precondition: lease is stale")
		}
		touchSessionLease(root, "s1")
		if DaemonShouldExit(root, now) {
			t.Fatal("touched lease must keep the daemon alive")
		}
	})

	t.Run("superseded by another pid exits", func(t *testing.T) {
		root := t.TempDir()
		writeLease(t, root, "fresh.json", now)
		if err := watch.WriteProcessPID(root, os.Getpid()+1); err != nil {
			t.Fatal(err)
		}
		if !DaemonShouldExit(root, now) {
			t.Fatal("daemon whose watch.pid names another process must exit")
		}
		if err := watch.WriteProcessPID(root, os.Getpid()); err != nil {
			t.Fatal(err)
		}
		if DaemonShouldExit(root, now) {
			t.Fatal("daemon that owns watch.pid must stay")
		}
	})

	t.Run("removed root exits", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "gone")
		if !DaemonShouldExit(root, now) {
			t.Fatal("missing root must exit")
		}
	})
}
