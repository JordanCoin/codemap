package watch

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"codemap/scanner"
)

func waitForWatchCondition(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for watch condition")
}

func runGitWatchTestCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}

func TestGetGraphWriteInitialStateAndFindRelatedHot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}

	d := &Daemon{
		root: root,
		graph: &Graph{
			Files: map[string]*scanner.FileInfo{
				"pkg/types.go": {Path: "pkg/types.go"},
				"main.go":      {Path: "main.go"},
				"other.go":     {Path: "other.go"},
			},
			Events: []Event{
				{Time: time.Now().Add(-10 * time.Minute), Op: "WRITE", Path: "stale.go"},
				{Time: time.Now().Add(-30 * time.Second), Op: "WRITE", Path: "main.go"},
				{Time: time.Now().Add(-15 * time.Second), Op: "CREATE", Path: "other.go"},
			},
			FileGraph: &scanner.FileGraph{
				Imports: map[string][]string{
					"main.go": {"pkg/types.go"},
				},
				Importers: map[string][]string{
					"pkg/types.go": {"main.go", "other.go"},
				},
			},
		},
	}

	if got := d.GetGraph(); got != d.graph {
		t.Fatal("expected GetGraph to return the active graph pointer")
	}

	hot := d.findRelatedHot("pkg/types.go", time.Minute)
	slices.Sort(hot)
	if len(hot) != 2 || hot[0] != "main.go" || hot[1] != "other.go" {
		t.Fatalf("findRelatedHot() = %v, want [main.go other.go]", hot)
	}

	d.WriteInitialState()
	data, err := os.ReadFile(filepath.Join(root, ".codemap", "state.json"))
	if err != nil {
		t.Fatalf("expected state file to be written: %v", err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("expected valid state JSON: %v", err)
	}
	if state.FileCount != 3 {
		t.Fatalf("state file_count = %d, want 3", state.FileCount)
	}
	if len(state.RecentEvents) != 3 {
		t.Fatalf("state recent events = %d, want 3", len(state.RecentEvents))
	}
	if len(state.Importers["pkg/types.go"]) != 2 {
		t.Fatalf("expected importers to be persisted, got %+v", state.Importers)
	}
}

func TestIsFileDirty(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	runGitWatchTestCmd(t, root, "init")
	file := filepath.Join(root, "main.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitWatchTestCmd(t, root, "add", ".")
	runGitWatchTestCmd(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")

	if isFileDirty(root, "main.go") {
		t.Fatal("expected committed file to be clean")
	}

	if err := os.WriteFile(file, []byte("package main\n\nfunc changed() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isFileDirty(root, "main.go") {
		t.Fatal("expected modified file to be dirty")
	}
}

func TestDaemonStartTracksWriteEventsAndState(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	runGitWatchTestCmd(t, root, "init")
	file := filepath.Join(root, "main.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitWatchTestCmd(t, root, "add", ".")
	runGitWatchTestCmd(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")

	d, err := NewDaemon(root, false)
	if err != nil {
		t.Fatalf("NewDaemon() error: %v", err)
	}
	defer d.Stop()

	if err := d.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	waitForWatchCondition(t, 2*time.Second, func() bool {
		state := ReadState(root)
		return state != nil && state.FileCount >= 1
	})

	if err := os.WriteFile(file, []byte("package main\n\nfunc changed() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	waitForWatchCondition(t, 2*time.Second, func() bool {
		events := d.GetEvents(0)
		return len(events) > 0
	})

	events := d.GetEvents(0)
	last := events[len(events)-1]
	if last.Path != "main.go" {
		t.Fatalf("last event path = %q, want main.go", last.Path)
	}
	if last.Op != "WRITE" && last.Op != "CREATE" {
		t.Fatalf("last event op = %q, want WRITE or CREATE", last.Op)
	}
	if !last.Dirty {
		t.Fatalf("expected modified file event to be marked dirty, got %+v", last)
	}
	if last.Delta <= 0 {
		t.Fatalf("expected positive line delta for write event, got %+v", last)
	}

	waitForWatchCondition(t, 2*time.Second, func() bool {
		state := ReadState(root)
		return state != nil && len(state.RecentEvents) > 0
	})

	state := ReadState(root)
	if state == nil || len(state.RecentEvents) == 0 {
		t.Fatalf("expected watch state with recent events, got %+v", state)
	}
}
