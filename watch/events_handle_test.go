package watch

import (
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

func TestHandleEventWriteUpdatesTrackedState(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}

	rel := "pkg/main.go"
	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "package main\n\nfunc run() {}\n"
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		t.Fatal(err)
	}

	d := &Daemon{
		root:     root,
		eventLog: filepath.Join(root, ".codemap", "events.log"),
		graph: &Graph{
			Files: map[string]*scanner.FileInfo{
				rel: {Path: rel, Size: 10, Ext: ".go"},
			},
			State: map[string]*FileState{
				rel: {Lines: 1, Size: 10},
			},
		},
	}

	d.handleEvent(fsnotify.Event{Name: abs, Op: fsnotify.Write})

	d.graph.mu.RLock()
	defer d.graph.mu.RUnlock()

	state, ok := d.graph.State[rel]
	if !ok {
		t.Fatalf("expected %q to remain tracked in graph state", rel)
	}
	if state.Lines != 3 {
		t.Fatalf("expected updated line count 3, got %d", state.Lines)
	}
	if state.Size != info.Size() {
		t.Fatalf("expected updated size %d, got %d", info.Size(), state.Size)
	}

	file, ok := d.graph.Files[rel]
	if !ok {
		t.Fatalf("expected %q to remain tracked in graph files", rel)
	}
	if file.Ext != ".go" {
		t.Fatalf("expected .go extension, got %q", file.Ext)
	}

	if len(d.graph.Events) != 1 {
		t.Fatalf("expected one event, got %d", len(d.graph.Events))
	}
	event := d.graph.Events[0]
	if event.Op != "WRITE" || event.Path != rel {
		t.Fatalf("unexpected event: %+v", event)
	}
	if event.Delta != 2 {
		t.Fatalf("expected line delta +2, got %d", event.Delta)
	}
	if event.SizeDelta != info.Size()-10 {
		t.Fatalf("expected size delta %d, got %d", info.Size()-10, event.SizeDelta)
	}

	logData, err := os.ReadFile(filepath.Join(root, ".codemap", "events.log"))
	if err != nil {
		t.Fatalf("expected log file to be written: %v", err)
	}
	if !strings.Contains(string(logData), "WRITE") || !strings.Contains(string(logData), rel) {
		t.Fatalf("expected event log to contain write event for %q, got:\n%s", rel, string(logData))
	}
}

func TestHandleEventRemoveDeletesTrackedState(t *testing.T) {
	root := t.TempDir()
	rel := "old.go"

	d := &Daemon{
		root: root,
		graph: &Graph{
			Files: map[string]*scanner.FileInfo{
				rel: {Path: rel, Size: 40, Ext: ".go"},
			},
			State: map[string]*FileState{
				rel: {Lines: 4, Size: 40},
			},
		},
	}

	d.handleEvent(fsnotify.Event{Name: filepath.Join(root, rel), Op: fsnotify.Remove})

	d.graph.mu.RLock()
	defer d.graph.mu.RUnlock()

	if _, ok := d.graph.Files[rel]; ok {
		t.Fatalf("expected %q to be removed from graph files", rel)
	}
	if _, ok := d.graph.State[rel]; ok {
		t.Fatalf("expected %q to be removed from graph state", rel)
	}

	if len(d.graph.Events) != 1 {
		t.Fatalf("expected one event, got %d", len(d.graph.Events))
	}
	event := d.graph.Events[0]
	if event.Op != "REMOVE" || event.Path != rel {
		t.Fatalf("unexpected event: %+v", event)
	}
	if event.Delta != -4 {
		t.Fatalf("expected line delta -4, got %d", event.Delta)
	}
	if event.SizeDelta != -40 {
		t.Fatalf("expected size delta -40, got %d", event.SizeDelta)
	}
}

func TestHandleEventSkipsGitignoredPath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rel := "ignored.go"
	abs := filepath.Join(root, rel)
	if err := os.WriteFile(abs, []byte("package ignored\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	gitCache := scanner.NewGitIgnoreCache(root)
	gitCache.EnsureDir(root)

	d := &Daemon{
		root:     root,
		gitCache: gitCache,
		eventLog: filepath.Join(root, ".codemap", "events.log"),
		graph: &Graph{
			Files:  map[string]*scanner.FileInfo{},
			State:  map[string]*FileState{},
			Events: []Event{},
		},
	}

	d.handleEvent(fsnotify.Event{Name: abs, Op: fsnotify.Write})

	d.graph.mu.RLock()
	defer d.graph.mu.RUnlock()

	if len(d.graph.Events) != 0 {
		t.Fatalf("expected gitignored event to be skipped, got %+v", d.graph.Events)
	}
	if len(d.graph.Files) != 0 || len(d.graph.State) != 0 {
		t.Fatalf("expected gitignored event to avoid graph updates, files=%d state=%d", len(d.graph.Files), len(d.graph.State))
	}
}
