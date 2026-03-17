package watch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"codemap/scanner"

	"github.com/fsnotify/fsnotify"
)

func TestHandleEventMissingWriteClearsStaleTrackedFile(t *testing.T) {
	root := t.TempDir()
	rel := "ghost.go"
	abs := filepath.Join(root, rel)

	d := &Daemon{
		root: root,
		graph: &Graph{
			Files: map[string]*scanner.FileInfo{
				rel: {Path: rel, Size: 32, Ext: ".go"},
			},
			State: map[string]*FileState{
				rel: {Lines: 3, Size: 32},
			},
		},
	}

	d.handleEvent(fsnotify.Event{Name: abs, Op: fsnotify.Write})

	d.graph.mu.RLock()
	defer d.graph.mu.RUnlock()

	if _, exists := d.graph.Files[rel]; exists {
		t.Fatalf("expected stale file %q to be removed from graph.Files", rel)
	}
	if _, exists := d.graph.State[rel]; exists {
		t.Fatalf("expected stale file %q to be removed from graph.State", rel)
	}
}

func TestGetEventsReturnsCopyAndRespectsLimit(t *testing.T) {
	now := time.Now()
	daemon := &Daemon{
		graph: &Graph{
			Events: []Event{
				{Time: now.Add(-3 * time.Second), Op: "WRITE", Path: "a.go"},
				{Time: now.Add(-2 * time.Second), Op: "WRITE", Path: "b.go"},
				{Time: now.Add(-1 * time.Second), Op: "CREATE", Path: "c.go"},
			},
		},
	}

	tests := []struct {
		name      string
		limit     int
		wantPaths []string
	}{
		{name: "no limit returns all", limit: 0, wantPaths: []string{"a.go", "b.go", "c.go"}},
		{name: "positive limit returns tail", limit: 2, wantPaths: []string{"b.go", "c.go"}},
		{name: "limit above size returns all", limit: 10, wantPaths: []string{"a.go", "b.go", "c.go"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := daemon.GetEvents(tt.limit)
			if len(got) != len(tt.wantPaths) {
				t.Fatalf("GetEvents(%d) length = %d, want %d", tt.limit, len(got), len(tt.wantPaths))
			}

			for i := range got {
				if got[i].Path != tt.wantPaths[i] {
					t.Fatalf("GetEvents(%d)[%d].Path = %q, want %q", tt.limit, i, got[i].Path, tt.wantPaths[i])
				}
			}

			if len(got) > 0 {
				got[0].Path = "mutated.go"
				next := daemon.GetEvents(tt.limit)
				if len(next) > 0 && next[0].Path == "mutated.go" {
					t.Fatalf("GetEvents(%d) returned alias to internal slice", tt.limit)
				}
			}
		})
	}
}

func TestGetGraphReturnsBackingGraph(t *testing.T) {
	graph := &Graph{Files: map[string]*scanner.FileInfo{"a.go": {Path: "a.go"}}}
	daemon := &Daemon{graph: graph}

	if got := daemon.GetGraph(); got != graph {
		t.Fatal("expected GetGraph to return the daemon's backing graph pointer")
	}
}

func TestFindRelatedHot(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name      string
		graph     *scanner.FileGraph
		events    []Event
		path      string
		window    time.Duration
		wantPaths []string
	}{
		{
			name:      "nil file graph",
			path:      "a.go",
			window:    5 * time.Minute,
			wantPaths: nil,
		},
		{
			name: "returns connected files edited recently",
			graph: &scanner.FileGraph{
				Imports: map[string][]string{
					"a.go": {"b.go", "c.go"},
				},
				Importers: map[string][]string{
					"a.go": {"d.go"},
				},
			},
			events: []Event{
				{Time: now.Add(-7 * time.Minute), Op: "WRITE", Path: "c.go"},
				{Time: now.Add(-2 * time.Minute), Op: "WRITE", Path: "b.go"},
				{Time: now.Add(-1 * time.Minute), Op: "CREATE", Path: "d.go"},
				{Time: now.Add(-30 * time.Second), Op: "REMOVE", Path: "b.go"},
				{Time: now.Add(-20 * time.Second), Op: "WRITE", Path: "a.go"},
			},
			path:      "a.go",
			window:    5 * time.Minute,
			wantPaths: []string{"b.go", "d.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			daemon := &Daemon{
				graph: &Graph{
					FileGraph: tt.graph,
					Events:    tt.events,
				},
			}

			daemon.graph.mu.Lock()
			got := daemon.findRelatedHot(tt.path, tt.window)
			daemon.graph.mu.Unlock()

			sort.Strings(got)
			sort.Strings(tt.wantPaths)
			if !reflect.DeepEqual(got, tt.wantPaths) {
				t.Fatalf("findRelatedHot(%q) = %v, want %v", tt.path, got, tt.wantPaths)
			}
		})
	}
}

func TestHandleEventUnknownOpIgnored(t *testing.T) {
	root := t.TempDir()

	d := &Daemon{
		root:     root,
		eventLog: filepath.Join(root, ".codemap", "events.log"),
		graph: &Graph{
			Files: map[string]*scanner.FileInfo{},
			State: map[string]*FileState{},
		},
	}

	d.handleEvent(fsnotify.Event{Name: filepath.Join(root, "noop.go"), Op: fsnotify.Chmod})

	if got := d.GetEvents(0); len(got) != 0 {
		t.Fatalf("expected unknown op to be ignored, got %d events", len(got))
	}
}

func TestHandleEventRemoveDropsTrackedState(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	rel := "removed.go"
	abs := filepath.Join(root, rel)

	d := &Daemon{
		root:     root,
		eventLog: filepath.Join(root, ".codemap", "events.log"),
		graph: &Graph{
			Files: map[string]*scanner.FileInfo{
				rel: {Path: rel, Size: 42, Ext: ".go"},
			},
			State: map[string]*FileState{
				rel: {Lines: 6, Size: 42},
			},
		},
	}

	d.handleEvent(fsnotify.Event{Name: abs, Op: fsnotify.Remove})

	d.graph.mu.RLock()
	defer d.graph.mu.RUnlock()

	if _, ok := d.graph.Files[rel]; ok {
		t.Fatalf("expected %q to be removed from tracked files", rel)
	}
	if _, ok := d.graph.State[rel]; ok {
		t.Fatalf("expected %q to be removed from cached state", rel)
	}
	if len(d.graph.Events) != 1 {
		t.Fatalf("expected 1 recorded event, got %d", len(d.graph.Events))
	}
	got := d.graph.Events[0]
	if got.Op != "REMOVE" {
		t.Fatalf("event op = %q, want REMOVE", got.Op)
	}
	if got.Delta != -6 {
		t.Fatalf("event delta = %d, want -6", got.Delta)
	}
	if got.SizeDelta != -42 {
		t.Fatalf("event size delta = %d, want -42", got.SizeDelta)
	}
}

func TestEventLoopSkipsNonSourceCreateFile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}

	watcher := &fsnotify.Watcher{
		Events: make(chan fsnotify.Event, 1),
		Errors: make(chan error, 1),
	}

	d := &Daemon{
		root:     root,
		watcher:  watcher,
		done:     make(chan struct{}),
		eventLog: filepath.Join(root, ".codemap", "events.log"),
		graph: &Graph{
			Files: map[string]*scanner.FileInfo{},
			State: map[string]*FileState{},
		},
	}

	go d.eventLoop()
	defer close(d.done)
	defer close(watcher.Events)
	defer close(watcher.Errors)

	txtPath := filepath.Join(root, "README.txt")
	if err := os.WriteFile(txtPath, []byte("ignore me"), 0o644); err != nil {
		t.Fatal(err)
	}

	watcher.Events <- fsnotify.Event{Name: txtPath, Op: fsnotify.Create}
	time.Sleep(50 * time.Millisecond)

	if got := d.GetEvents(0); len(got) != 0 {
		t.Fatalf("expected non-source create to be ignored, got events: %+v", got)
	}
}

func TestLogEventWritesLineAndState(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}

	d := &Daemon{
		root:     root,
		eventLog: filepath.Join(root, ".codemap", "events.log"),
		graph: &Graph{
			Files: map[string]*scanner.FileInfo{"a.go": {Path: "a.go", Ext: ".go"}},
			State: map[string]*FileState{},
			Events: []Event{
				{Time: time.Now().Add(-time.Second), Op: "WRITE", Path: "a.go"},
			},
		},
	}

	eventTime := time.Unix(1700000000, 0)
	d.logEvent(Event{
		Time:  eventTime,
		Op:    "WRITE",
		Path:  "a.go",
		Lines: 10,
		Delta: 2,
		Dirty: true,
	})

	logBytes, err := os.ReadFile(d.eventLog)
	if err != nil {
		t.Fatalf("expected event log to be written: %v", err)
	}
	logText := string(logBytes)
	if logText == "" {
		t.Fatal("expected non-empty log output")
	}
	if !strings.Contains(logText, eventTime.Format("2006-01-02 15:04:05")+" | WRITE") {
		t.Fatalf("expected formatted timestamp and operation in log line, got %q", logText)
	}
	if !strings.Contains(logText, "a.go") || !strings.Contains(logText, "+2") || !strings.Contains(logText, "dirty") {
		t.Fatalf("expected path/delta/dirty details in log line, got %q", logText)
	}

	statePath := filepath.Join(root, ".codemap", "state.json")
	waitForWatchCondition(t, 2*time.Second, func() bool {
		_, err := os.Stat(statePath)
		return err == nil
	})

	stateBytes, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("expected state file to be readable: %v", err)
	}
	var state State
	if err := json.Unmarshal(stateBytes, &state); err != nil {
		t.Fatalf("expected valid state JSON: %v", err)
	}
	if state.FileCount != 1 {
		t.Fatalf("state file_count = %d, want 1", state.FileCount)
	}
	if len(state.RecentEvents) != 1 || state.RecentEvents[0].Path != "a.go" {
		t.Fatalf("state recent events = %+v, want one event for a.go", state.RecentEvents)
	}
}
