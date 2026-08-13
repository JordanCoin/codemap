package watch

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"codemap/internal/projectpath"
	"codemap/scanner"
)

func TestReadStateStaleButRunning(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codemap-state-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	codemapDir := projectpath.ProjectRuntimeDir(tmpDir)
	if err := os.MkdirAll(codemapDir, 0755); err != nil {
		t.Fatalf("Failed to create .codemap dir: %v", err)
	}

	state := State{
		UpdatedAt: time.Now().Add(-2 * time.Minute),
		FileCount: 42,
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("Failed to marshal state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(codemapDir, "state.json"), data, 0644); err != nil {
		t.Fatalf("Failed to write state file: %v", err)
	}

	// Simulate running daemon by pointing pid file to current process.
	if err := WritePID(tmpDir); err != nil {
		t.Fatalf("Failed to write pid file: %v", err)
	}
	defer RemovePID(tmpDir)

	got := ReadState(tmpDir)
	if got == nil {
		t.Fatal("Expected stale state to be returned when daemon is running")
	}
	if got.FileCount != 42 {
		t.Fatalf("Expected file_count 42, got %d", got.FileCount)
	}
}

func TestReadStateStaleAndNotRunning(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codemap-state-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	codemapDir := projectpath.ProjectRuntimeDir(tmpDir)
	if err := os.MkdirAll(codemapDir, 0755); err != nil {
		t.Fatalf("Failed to create .codemap dir: %v", err)
	}

	state := State{
		UpdatedAt: time.Now().Add(-2 * time.Minute),
		FileCount: 10,
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("Failed to marshal state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(codemapDir, "state.json"), data, 0644); err != nil {
		t.Fatalf("Failed to write state file: %v", err)
	}

	if got := ReadState(tmpDir); got != nil {
		t.Fatal("Expected nil for stale state when daemon is not running")
	}
}

func TestWriteStateWithoutFileGraph(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codemap-state-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.MkdirAll(filepath.Join(tmpDir, ".codemap"), 0755); err != nil {
		t.Fatalf("Failed to create .codemap dir: %v", err)
	}

	d := &Daemon{
		root: tmpDir,
		graph: &Graph{
			Files: map[string]*scanner.FileInfo{
				"main.go": {Path: "main.go", Ext: ".go"},
			},
			Events: []Event{},
		},
	}

	d.writeState()

	state := ReadState(tmpDir)
	if state == nil {
		t.Fatal("Expected state file to be written without file graph")
	}
	if state.FileCount != 1 {
		t.Fatalf("Expected file_count 1, got %d", state.FileCount)
	}
	if len(state.Hubs) != 0 {
		t.Fatalf("Expected 0 hubs without file graph, got %d", len(state.Hubs))
	}
}

func TestWriteInitialStateWritesReadableState(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatalf("Failed to create .codemap dir: %v", err)
	}

	d := &Daemon{
		root: root,
		graph: &Graph{
			Files: map[string]*scanner.FileInfo{
				"main.go": {Path: "main.go", Ext: ".go"},
			},
			Events: []Event{
				{Time: time.Now(), Op: "WRITE", Path: "main.go"},
			},
		},
	}

	d.WriteInitialState()

	got := ReadState(root)
	if got == nil {
		t.Fatal("expected state to be readable after WriteInitialState")
	}
	if got.FileCount != 1 {
		t.Fatalf("expected file_count 1, got %d", got.FileCount)
	}
	if len(got.RecentEvents) != 1 || got.RecentEvents[0].Path != "main.go" {
		t.Fatalf("unexpected recent events in state: %#v", got.RecentEvents)
	}
}

func TestWatchStorageUsesSetupRoot(t *testing.T) {
	projectRoot := t.TempDir()
	setupRoot := t.TempDir()
	projectpath.SetSetupRoot(setupRoot)
	t.Cleanup(projectpath.ResetSetupRoot)
	if err := os.MkdirAll(filepath.Join(setupRoot, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := WritePID(projectRoot); err != nil {
		t.Fatalf("WritePID() error: %v", err)
	}
	wantPID := filepath.Join(projectpath.ProjectRuntimeDir(projectRoot), "watch.pid")
	if _, err := os.Stat(wantPID); err != nil {
		t.Fatalf("setup-root PID missing: %v", err)
	}

	d, err := NewDaemon(projectRoot, false)
	if err != nil {
		t.Fatalf("NewDaemon() error: %v", err)
	}
	defer d.watcher.Close()
	wantLog := filepath.Join(projectpath.ProjectRuntimeDir(projectRoot), "events.log")
	if d.eventLog != wantLog {
		t.Fatalf("eventLog = %q, want %q", d.eventLog, wantLog)
	}
}

func TestAutomaticLinkedWorktreeUsesLocalWatchStorage(t *testing.T) {
	projectpath.ResetSetupRoot()
	t.Cleanup(projectpath.ResetSetupRoot)
	primary := t.TempDir()
	gitDir := filepath.Join(primary, ".git", "worktrees", "agent")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(primary, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(t.TempDir(), "linked")
	if err := os.MkdirAll(filepath.Join(linked, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(linked, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	linked, _ = filepath.EvalSymlinks(linked)

	if err := WritePID(linked); err != nil {
		t.Fatalf("WritePID() error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectpath.ProjectRuntimeDir(linked), "watch.pid")); err != nil {
		t.Fatalf("linked-worktree PID missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectpath.ProjectRuntimeDir(primary), "watch.pid")); !os.IsNotExist(err) {
		t.Fatalf("primary PID unexpectedly created: %v", err)
	}

	d, err := NewDaemon(linked, false)
	if err != nil {
		t.Fatalf("NewDaemon() error: %v", err)
	}
	defer d.watcher.Close()
	if want := filepath.Join(projectpath.ProjectRuntimeDir(linked), "events.log"); d.eventLog != want {
		t.Fatalf("eventLog = %q, want %q", d.eventLog, want)
	}
	d.WriteInitialState()
	if _, err := os.Stat(filepath.Join(projectpath.ProjectRuntimeDir(linked), "state.json")); err != nil {
		t.Fatalf("linked-worktree state missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(primary, ".codemap", "state.json")); !os.IsNotExist(err) {
		t.Fatalf("primary state unexpectedly created: %v", err)
	}
}

func TestProcessAliveDetectsLiveAndDeadPIDs(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Fatal("current process should be reported alive")
	}
	if processAlive(0) {
		t.Fatal("pid 0 should not be reported alive")
	}
	// Spawn a short-lived process, wait for it to exit, then confirm it is dead.
	// Re-exec this test binary with a run filter that matches nothing so it
	// exits immediately — self-contained, no dependency on an external tool.
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot spawn helper process: %v", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Wait()
	if processAlive(pid) {
		t.Fatalf("exited process %d should not be reported alive", pid)
	}
}

func TestReadStateRejectsForeignRootAndAcceptsDescendants(t *testing.T) {
	// A shared setup root makes every project read the same runtime dir, so
	// the State.Root check is what separates one project's state from another.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	setupRoot := t.TempDir()
	projectpath.SetSetupRoot(setupRoot)
	t.Cleanup(projectpath.ResetSetupRoot)
	if err := os.MkdirAll(filepath.Join(setupRoot, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{root: root, graph: &Graph{
		Files:  map[string]*scanner.FileInfo{"main.go": {Path: "main.go", Ext: ".go"}},
		Events: []Event{{Time: time.Now(), Op: "WRITE", Path: "main.go"}},
	}}
	d.WriteInitialState()

	if got := ReadState(root); got == nil || got.FileCount != 1 {
		t.Fatalf("ReadState(own root) = %v, want the state", got)
	}
	subdir := filepath.Join(root, "pkg", "x")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ReadState(subdir); got == nil {
		t.Fatal("ReadState(subdirectory) = nil, want the state (same project)")
	}
	foreign := t.TempDir()
	if got := ReadState(foreign); got != nil {
		t.Fatalf("ReadState(foreign root) = %v, want nil (never serve another project's state)", got)
	}
}

func TestTwoProjectsSharingSetupRootDoNotCollideOnPID(t *testing.T) {
	setupRoot := t.TempDir()
	projectpath.SetSetupRoot(setupRoot)
	t.Cleanup(projectpath.ResetSetupRoot)
	projA := filepath.Join(t.TempDir(), "projA")
	projB := filepath.Join(t.TempDir(), "projB")
	for _, p := range []string{projA, projB} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if pidA, pidB := projectpath.ProjectRuntimeDir(projA), projectpath.ProjectRuntimeDir(projB); pidA == pidB {
		t.Fatalf("projects under one setup root collide on the pid path: %s", pidA)
	}
	if err := WritePID(projA); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPID(projB); err == nil {
		t.Fatal("projB read projA's pid — the collision is not fixed")
	}
}
