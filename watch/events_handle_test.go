package watch

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
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

func TestHandleEventWriteUpdatesTrackedStateAndContext(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}

	filePath := filepath.Join(root, "main.go")
	content := "package main\n\nfunc main() {}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	d := &Daemon{
		root:     root,
		eventLog: filepath.Join(root, ".codemap", "events.log"),
		graph: &Graph{
			Files: map[string]*scanner.FileInfo{
				"main.go": {Path: "main.go", Size: 10, Ext: ".go"},
			},
			State: map[string]*FileState{
				"main.go": {Lines: 1, Size: 10},
			},
			Events: []Event{
				{Time: time.Now().Add(-10 * time.Second), Op: "WRITE", Path: "dep.go"},
			},
			HasDeps: true,
			FileGraph: &scanner.FileGraph{
				Imports: map[string][]string{
					"main.go": {"dep.go"},
				},
				Importers: map[string][]string{
					"main.go": {"a.go", "b.go", "c.go"},
				},
			},
		},
	}

	d.handleEvent(fsnotify.Event{Name: filePath, Op: fsnotify.Write})

	d.graph.mu.RLock()
	defer d.graph.mu.RUnlock()

	if got := d.graph.State["main.go"]; got == nil || got.Lines != 3 {
		t.Fatalf("expected updated state with 3 lines, got %+v", got)
	}
	if got := d.graph.Files["main.go"]; got == nil || got.Size <= 10 {
		t.Fatalf("expected tracked file size update, got %+v", got)
	}

	last := d.graph.Events[len(d.graph.Events)-1]
	if last.Op != "WRITE" {
		t.Fatalf("event op = %q, want WRITE", last.Op)
	}
	if last.Delta != 2 {
		t.Fatalf("event delta = %d, want 2", last.Delta)
	}
	if last.Importers != 3 || last.Imports != 1 {
		t.Fatalf("dependency context = imports:%d importers:%d", last.Imports, last.Importers)
	}
	if !last.IsHub {
		t.Fatal("expected event to be marked as hub")
	}
	sort.Strings(last.RelatedHot)
	if len(last.RelatedHot) != 1 || last.RelatedHot[0] != "dep.go" {
		t.Fatalf("related hot files = %v, want [dep.go]", last.RelatedHot)
	}
}

func TestHandleEventRemoveAndRenameDropTrackedState(t *testing.T) {
	tests := []struct {
		name string
		op   fsnotify.Op
		want string
	}{
		{name: "remove", op: fsnotify.Remove, want: "REMOVE"},
		{name: "rename", op: fsnotify.Rename, want: "RENAME"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			rel := "dead.go"
			abs := filepath.Join(root, rel)

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

			d.handleEvent(fsnotify.Event{Name: abs, Op: tt.op})

			d.graph.mu.RLock()
			defer d.graph.mu.RUnlock()

			if _, exists := d.graph.Files[rel]; exists {
				t.Fatalf("expected %q to be removed from tracked files", rel)
			}
			if _, exists := d.graph.State[rel]; exists {
				t.Fatalf("expected %q to be removed from tracked state", rel)
			}
			last := d.graph.Events[len(d.graph.Events)-1]
			if last.Op != tt.want {
				t.Fatalf("event op = %q, want %q", last.Op, tt.want)
			}
			if last.Delta != -4 {
				t.Fatalf("event delta = %d, want -4", last.Delta)
			}
		})
	}
}

func TestHandleEventCreateDirectoryReturnsWithoutTrackingFile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()

	dirPath := filepath.Join(root, "src")
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		t.Fatal(err)
	}

	d := &Daemon{
		root:     root,
		watcher:  watcher,
		eventLog: filepath.Join(root, ".codemap", "events.log"),
		graph: &Graph{
			Files: make(map[string]*scanner.FileInfo),
			State: make(map[string]*FileState),
		},
	}

	d.handleEvent(fsnotify.Event{Name: dirPath, Op: fsnotify.Create})

	d.graph.mu.RLock()
	defer d.graph.mu.RUnlock()

	if len(d.graph.Events) != 0 {
		t.Fatalf("expected no file event for created directory, got %d events", len(d.graph.Events))
	}
	if len(d.graph.Files) != 0 {
		t.Fatalf("expected no tracked files for directory create, got %d", len(d.graph.Files))
	}
}
