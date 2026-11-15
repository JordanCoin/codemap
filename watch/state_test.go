package watch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"codemap/internal/projectpath"
	"codemap/scanner"

	"github.com/fsnotify/fsnotify"
)

func TestHelperWatchDaemonProcess(t *testing.T) {
	if os.Getenv("CODEMAP_TEST_WATCH_HELPER") != "1" {
		return
	}
	time.Sleep(time.Minute)
}

func TestMutableWatcherStateFailsClosedOnRuntimeIdentityMismatch(t *testing.T) {
	root, setup := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	projectpath.SetSetupRoot(setup)
	t.Cleanup(projectpath.ResetSetupRoot)
	selection, err := projectpath.SelectRuntime(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(selection.RuntimeDir, "project.json"), []byte(`{"canonical_root":"/other"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := WriteProcessPID(root, 42); err == nil {
		t.Fatal("WriteProcessPID accepted mismatched runtime identity")
	}
	if d, err := NewDaemon(root, false); err == nil {
		d.watcher.Close()
		t.Fatal("NewDaemon accepted mismatched runtime identity")
	}
	for _, name := range []string{"watch.pid", "state.json", "events.log"} {
		if _, err := os.Stat(filepath.Join(root, ".codemap", name)); !os.IsNotExist(err) {
			t.Fatalf("unsafe fallback artifact %s exists: %v", name, err)
		}
	}
}

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

	process := exec.Command(os.Args[0], "-test.run=TestHelperWatchDaemonProcess", "--", "watch", "daemon", tmpDir)
	process.Env = append(os.Environ(), "CODEMAP_TEST_WATCH_HELPER=1")
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan struct{})
	go func() {
		_ = process.Wait()
		close(waited)
	}()
	t.Cleanup(func() { _ = process.Process.Kill(); <-waited })
	if err := os.WriteFile(filepath.Join(codemapDir, "watch.pid"), []byte(fmt.Sprint(process.Process.Pid)), 0o644); err != nil {
		t.Fatal(err)
	}

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

func TestExplicitSetupSeparatesMutableWatchFiles(t *testing.T) {
	setup := t.TempDir()
	projectA := t.TempDir()
	projectB := t.TempDir()
	projectpath.SetSetupRoot(setup)
	t.Cleanup(projectpath.ResetSetupRoot)

	if err := WritePID(projectA); err != nil {
		t.Fatal(err)
	}
	if err := WritePID(projectB); err != nil {
		t.Fatal(err)
	}
	a, err := projectpath.SelectRuntime(projectA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := projectpath.SelectRuntime(projectB)
	if err != nil {
		t.Fatal(err)
	}
	aDir := projectpath.ProjectRuntimeDir(a.ProjectRoot)
	bDir := projectpath.ProjectRuntimeDir(b.ProjectRoot)
	if aDir == bDir {
		t.Fatalf("projects share runtime %q", aDir)
	}
	for _, dir := range []string{aDir, bDir} {
		if _, err := os.Stat(filepath.Join(dir, "watch.pid")); err != nil {
			t.Fatalf("namespaced PID missing in %q: %v", dir, err)
		}
	}
	if _, err := os.Stat(filepath.Join(setup, ".codemap", "watch.pid")); !os.IsNotExist(err) {
		t.Fatalf("legacy shared PID unexpectedly written: %v", err)
	}
}

func startHelperDaemon(t *testing.T, root string) *exec.Cmd {
	t.Helper()
	process := exec.Command(os.Args[0], "-test.run=TestHelperWatchDaemonProcess", "--", "watch", "daemon", root)
	process.Env = append(os.Environ(), "CODEMAP_TEST_WATCH_HELPER=1")
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = process.Wait() }() // reap, so a terminated helper is not a live zombie
	t.Cleanup(func() { _ = process.Process.Kill() })
	return process
}

// A daemon from an older binary publishes state elsewhere; only the registry
// knows it. It must be found (so no second one starts) yet count as not
// running here (so the start path replaces it), and Stop must end it.
func TestResolveActiveRuntimeFindsRegisteredDaemonOutsideStateDir(t *testing.T) {
	project := filepath.Join(t.TempDir(), "project with space")
	if err := os.MkdirAll(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	root := projectpath.CanonicalPath(project)
	process := startHelperDaemon(t, root)
	registerDaemon(root, process.Process.Pid, "")

	active, err := ResolveActiveRuntime(project)
	if err != nil {
		t.Fatal(err)
	}
	if !active.Legacy || active.PID != process.Process.Pid {
		t.Fatalf("ResolveActiveRuntime() = %#v, want the registered daemon", active)
	}
	if IsRunning(project) {
		t.Fatal("IsRunning = true for a daemon that publishes outside the state dir")
	}
	if err := Stop(project); err != nil {
		t.Fatalf("Stop() = %v", err)
	}
	if _, ok := registeredDaemon(root); ok {
		t.Fatal("registry entry survived Stop")
	}
}

// A daemon under an explicit --setup-root keeps its pid in another current
// layout; the registry names that dir, so it is used, never replaced.
func TestResolveActiveRuntimeUsesRegisteredDaemonOfAnotherLayout(t *testing.T) {
	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	root := projectpath.CanonicalPath(project)
	process := startHelperDaemon(t, root)
	elsewhere := t.TempDir()
	if err := os.WriteFile(filepath.Join(elsewhere, "watch.pid"), []byte(fmt.Sprint(process.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	registerDaemon(root, process.Process.Pid, elsewhere)

	active, err := ResolveActiveRuntime(project)
	if err != nil {
		t.Fatal(err)
	}
	if active.Legacy || active.PID != process.Process.Pid || active.Directory != elsewhere {
		t.Fatalf("ResolveActiveRuntime() = %#v, want the other layout's daemon, not legacy", active)
	}
	if !IsRunning(project) {
		t.Fatal("IsRunning = false for a current daemon under another layout")
	}
}

func TestResolveActiveRuntimeIgnoresForeignRegisteredPID(t *testing.T) {
	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := startHelperDaemon(t, t.TempDir())
	registerDaemon(project, foreign.Process.Pid, "")

	active, err := ResolveActiveRuntime(project)
	if err != nil {
		t.Fatal(err)
	}
	if active.PID != 0 || active.Legacy {
		t.Fatalf("ResolveActiveRuntime() = %#v, want no daemon for a foreign pid", active)
	}
	if err := Stop(project); err == nil {
		t.Fatal("Stop() succeeded with no daemon")
	}
	if !processAlive(foreign.Process.Pid) {
		t.Fatal("Stop killed a daemon for another root")
	}
}

func TestTransitionLockSerializesStartsAndReleases(t *testing.T) {
	root := t.TempDir()
	first, err := AcquireTransition(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireTransition(root); !errors.Is(err, ErrTransitionLocked) {
		t.Fatalf("second AcquireTransition() error = %v, want ErrTransitionLocked", err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	third, err := AcquireTransition(root)
	if err != nil {
		t.Fatalf("AcquireTransition() after release: %v", err)
	}
	if err := third.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestWriteProcessPIDRejectsSymlinkEndpoint(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks may require elevated privileges")
	}
	root := t.TempDir()
	runtimeDir := projectpath.ProjectRuntimeDir(root)
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("preserve"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(runtimeDir, "watch.pid")); err != nil {
		t.Fatal(err)
	}
	if err := WriteProcessPID(root, 123); err == nil {
		t.Fatal("WriteProcessPID followed a symlink")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "preserve" {
		t.Fatalf("symlink target changed: %q, %v", data, err)
	}
}

func TestStopOwnedDaemonWaitsForVerifiedExitBeforeRemovingPID(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	process := exec.Command(os.Args[0], "-test.run=TestHelperWatchDaemonProcess", "--", "watch", "daemon", root)
	process.Env = append(os.Environ(), "CODEMAP_TEST_WATCH_HELPER=1")
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan struct{})
	go func() {
		_ = process.Wait()
		close(waited)
	}()
	t.Cleanup(func() { _ = process.Process.Kill(); <-waited })
	if err := WriteProcessPID(root, process.Process.Pid); err != nil {
		t.Fatal(err)
	}
	if err := Stop(root); err != nil {
		t.Fatal(err)
	}
	selection, err := projectpath.SelectRuntime(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(projectpath.ProjectRuntimeDir(selection.ProjectRoot), "watch.pid")); !os.IsNotExist(err) {
		t.Fatalf("PID evidence remains after verified exit: %v", err)
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
	if err := os.WriteFile(filepath.Join(primary, ".codemap", "config.json"), []byte(`{"only":["go"]}`), 0o644); err != nil {
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
	if _, err := os.Stat(filepath.Join(projectpath.ProjectRuntimeDir(primary), "state.json")); !os.IsNotExist(err) {
		t.Fatalf("primary state unexpectedly created: %v", err)
	}

	if err := os.WriteFile(filepath.Join(linked, ".git"), []byte("invalid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	textFile := filepath.Join(linked, "notes.txt")
	if err := os.WriteFile(textFile, []byte("not configured\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if changed := d.handleConfiguredMembershipEvent(fsnotify.Event{Name: textFile, Op: fsnotify.Create}); changed {
		t.Fatal("linked worktree stopped using its resolved primary policy")
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
