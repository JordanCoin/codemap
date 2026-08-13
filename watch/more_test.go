package watch

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"codemap/internal/projectpath"
	"codemap/limits"
	"codemap/scanner"

	"github.com/fsnotify/fsnotify"
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
	data, err := os.ReadFile(filepath.Join(projectpath.ProjectRuntimeDir(root), "state.json"))
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

func TestConfiguredFileCountTracksConfiguredFilesAcrossEvents(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".codemap", "config.json"), []byte(`{"only":["go"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	goFile := filepath.Join(root, "main.go")
	textFile := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(goFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(textFile, []byte("not source\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	d, err := NewDaemon(root, false)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop()
	if err := d.fullScan(); err != nil {
		t.Fatal(err)
	}

	if got := d.FileCount(); got <= 1 {
		t.Fatalf("tracked file count = %d, want it to remain broader than configured files", got)
	}
	if got := d.ConfiguredFileCount(); got != 1 {
		t.Fatalf("configured file count = %d, want 1", got)
	}

	addedGo := filepath.Join(root, "added.go")
	if err := os.WriteFile(addedGo, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d.handleEvent(fsnotify.Event{Name: addedGo, Op: fsnotify.Create})
	if got := d.ConfiguredFileCount(); got != 2 {
		t.Fatalf("configured file count after Go create = %d, want 2", got)
	}

	addedText := filepath.Join(root, "added.txt")
	if err := os.WriteFile(addedText, []byte("not source\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d.handleEvent(fsnotify.Event{Name: addedText, Op: fsnotify.Create})
	if got := d.ConfiguredFileCount(); got != 2 {
		t.Fatalf("configured file count after text create = %d, want 2", got)
	}

	if err := os.Remove(addedGo); err != nil {
		t.Fatal(err)
	}
	d.handleEvent(fsnotify.Event{Name: addedGo, Op: fsnotify.Remove})
	if got := d.ConfiguredFileCount(); got != 1 {
		t.Fatalf("configured file count after Go remove = %d, want 1", got)
	}
}

func TestConfiguredFileCountTracksLiveFilterChanges(t *testing.T) {
	root := t.TempDir()
	codemapDir := projectpath.CodemapDir(root)
	if err := os.MkdirAll(codemapDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(codemapDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"only":["go"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "query.sql"), []byte("select 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	d, err := NewDaemon(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Stop()
	configured := func(path string) bool {
		d.graph.mu.RLock()
		defer d.graph.mu.RUnlock()
		_, ok := d.graph.ConfiguredFiles[path]
		return ok
	}
	if got := d.ConfiguredFileCount(); got != 1 {
		t.Fatalf("initial configured count = %d, want 1", got)
	}

	if err := os.WriteFile(configPath, []byte(`{"only":["sql"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForWatchCondition(t, 2*time.Second, func() bool {
		d.graph.mu.RLock()
		defer d.graph.mu.RUnlock()
		_, configured := d.graph.ConfiguredFiles["query.sql"]
		return configured
	})
	if err := os.WriteFile(filepath.Join(root, "second.sql"), []byte("select 2;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForWatchCondition(t, 2*time.Second, func() bool { return d.ConfiguredFileCount() == 2 })

	if err := os.WriteFile(configPath, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForWatchCondition(t, 2*time.Second, func() bool { return configured("main.go") })
	notesPath := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(notesPath, []byte("notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForWatchCondition(t, 2*time.Second, func() bool { return configured("notes.txt") })
	if err := os.Remove(notesPath); err != nil {
		t.Fatal(err)
	}
	waitForWatchCondition(t, 2*time.Second, func() bool { return !configured("notes.txt") })

	if err := os.WriteFile(configPath, []byte(`{"exclude":["*.tmp"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForWatchCondition(t, 2*time.Second, func() bool { return !configured("ignored.tmp") })
	if err := os.WriteFile(filepath.Join(root, "included.txt"), []byte("included\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ignored.tmp"), []byte("ignored\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForWatchCondition(t, 2*time.Second, func() bool { return configured("included.txt") && !configured("ignored.tmp") })
	if err := os.Remove(filepath.Join(root, "included.txt")); err != nil {
		t.Fatal(err)
	}
	waitForWatchCondition(t, 2*time.Second, func() bool { return !configured("included.txt") })

	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("second.sql\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForWatchCondition(t, 2*time.Second, func() bool { return !configured("second.sql") })
	if err := os.Remove(filepath.Join(root, ".gitignore")); err != nil {
		t.Fatal(err)
	}
	waitForWatchCondition(t, 2*time.Second, func() bool { return configured("second.sql") })
}

func TestConfiguredFilterChangeInvalidatesDependencyState(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, ".codemap", "config.json")
	if err := os.WriteFile(configPath, []byte(`{"only":["go"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := NewDaemon(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Stop()
	d.graph.mu.Lock()
	d.graph.FileGraph = &scanner.FileGraph{Importers: map[string][]string{"old.go": {"a.go", "b.go", "c.go"}}}
	d.graph.DepCtx = map[string]*DepContext{"old.go": {Importers: []string{"a.go"}}}
	d.graph.HasDeps = true
	d.graph.mu.Unlock()
	baseline := publishedStateTime(t, root)
	if err := os.WriteFile(configPath, []byte(`{"only":["sql"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// The invariant is that state computed under the old filters is discarded,
	// not that the graph is left destroyed: refreshConfiguredFiles rebuilds it
	// (see TestConfiguredFilterChangeRebuildsDependencyGraph), so assert the
	// stale entries are gone rather than that the graph is nil.
	//
	// Wait on the published state advancing rather than on in-memory fields.
	// The refresh nils the graph before rebuilding it, so an in-memory
	// condition can be satisfied mid-refresh, before writeState has run, and
	// the ReadState assertion below would then race the daemon.
	waitForWatchCondition(t, 5*time.Second, func() bool {
		if !publishedStateAdvanced(root, baseline) {
			return false
		}
		d.graph.mu.RLock()
		defer d.graph.mu.RUnlock()
		if _, stale := d.graph.DepCtx["old.go"]; stale {
			return false
		}
		if d.graph.FileGraph == nil {
			return false
		}
		_, stale := d.graph.FileGraph.Importers["old.go"]
		return !stale
	})
	state := ReadState(root)
	if state == nil || len(state.Hubs) != 0 || len(state.Imports) != 0 || len(state.Importers) != 0 {
		t.Fatalf("stale dependency state persisted: %#v", state)
	}
}

func TestConfiguredFileCountExcludesDaemonState(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".codemap", "config.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := NewDaemon(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Stop()
	waitForWatchCondition(t, 2*time.Second, func() bool { return d.ConfiguredFileCount() == 1 })
}

func TestConfiguredFileCountDrivesDependencyGraphLimit(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".codemap", "config.json"), []byte(`{"only":["go"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= limits.LargeRepoFileCount; i++ {
		path := filepath.Join(root, fmt.Sprintf("fixture-%04d.txt", i))
		if err := os.WriteFile(path, []byte("not source\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	d, err := NewDaemon(root, false)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop()
	if err := d.fullScan(); err != nil {
		t.Fatal(err)
	}

	if d.FileCount() <= limits.LargeRepoFileCount {
		t.Fatalf("tracked file count = %d, want a large repo", d.FileCount())
	}
	if got := d.ConfiguredFileCount(); got != 1 {
		t.Fatalf("configured file count = %d, want 1", got)
	}
	if !shouldComputeDependencyGraph(d.ConfiguredFileCount()) {
		t.Fatal("expected dependency graph to use the configured source count")
	}
	if shouldComputeDependencyGraph(d.FileCount()) {
		t.Fatal("expected tracked file count alone to exceed the dependency graph limit")
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

	var last Event
	waitForWatchCondition(t, 2*time.Second, func() bool {
		events := d.GetEvents(0)
		for i := len(events) - 1; i >= 0; i-- {
			event := events[i]
			if event.Path == "main.go" &&
				(event.Op == "WRITE" || event.Op == "CREATE") &&
				event.Dirty && event.Delta > 0 {
				last = event
				return true
			}
		}
		return false
	})
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

// TestConfiguredFilterChangeRebuildsDependencyGraph pins that invalidating the
// dependency graph after a filter change is followed by rebuilding it. Dropping
// it and waiting for a restart leaves the daemon serving no hub or importer
// intelligence for the rest of its life, so every hook that reads daemon state
// silently degrades after a single config edit.
func TestConfiguredFilterChangeRebuildsDependencyGraph(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, ".codemap", "config.json")
	if err := os.WriteFile(configPath, []byte(`{"only":["go"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := NewDaemon(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Stop()

	// Plant a sentinel so a rebuilt graph is distinguishable both from the
	// startup graph and from one that was merely dropped.
	d.graph.mu.Lock()
	d.graph.FileGraph = &scanner.FileGraph{Importers: map[string][]string{"stale.go": {"a.go"}}}
	d.graph.DepCtx = map[string]*DepContext{"stale.go": {Importers: []string{"a.go"}}}
	d.graph.HasDeps = true
	d.graph.mu.Unlock()
	baseline := publishedStateTime(t, root)

	// Widen the filters; Go files stay configured, so dependency intelligence
	// must come back rather than stay dropped.
	if err := os.WriteFile(configPath, []byte(`{"only":["go","md"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForWatchCondition(t, 5*time.Second, func() bool {
		if !publishedStateAdvanced(root, baseline) {
			return false
		}
		d.graph.mu.RLock()
		defer d.graph.mu.RUnlock()
		if !d.graph.HasDeps || d.graph.FileGraph == nil {
			return false
		}
		_, stale := d.graph.FileGraph.Importers["stale.go"]
		return !stale
	})
}

// publishedStateTime returns the current state file timestamp, used as a
// baseline so tests can wait for the daemon to publish a *newer* state rather
// than sampling in-memory fields mid-refresh.
func publishedStateTime(t *testing.T, root string) time.Time {
	t.Helper()
	state := ReadState(root)
	if state == nil {
		t.Fatal("daemon has not published initial state")
	}
	return state.UpdatedAt
}

// publishedStateAdvanced reports whether the daemon has written state newer
// than baseline, which is the observable completion of a refresh cycle.
func publishedStateAdvanced(root string, baseline time.Time) bool {
	state := ReadState(root)
	return state != nil && state.UpdatedAt.After(baseline)
}
