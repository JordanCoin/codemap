package watch

import (
	"path/filepath"
	"testing"

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
