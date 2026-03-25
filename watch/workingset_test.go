package watch

import (
	"testing"
	"time"
)

func TestWorkingSet_Touch(t *testing.T) {
	ws := NewWorkingSet()

	ws.Touch("main.go", 10, false, 0)
	if ws.Size() != 1 {
		t.Fatalf("expected 1 file, got %d", ws.Size())
	}

	wf := ws.Files["main.go"]
	if wf.EditCount != 1 {
		t.Errorf("expected edit count 1, got %d", wf.EditCount)
	}
	if wf.NetDelta != 10 {
		t.Errorf("expected net delta 10, got %d", wf.NetDelta)
	}

	// Second touch accumulates
	ws.Touch("main.go", -3, false, 0)
	wf = ws.Files["main.go"]
	if wf.EditCount != 2 {
		t.Errorf("expected edit count 2, got %d", wf.EditCount)
	}
	if wf.NetDelta != 7 {
		t.Errorf("expected net delta 7, got %d", wf.NetDelta)
	}
}

func TestWorkingSet_TouchHub(t *testing.T) {
	ws := NewWorkingSet()
	ws.Touch("scanner/types.go", 5, true, 10)

	wf := ws.Files["scanner/types.go"]
	if !wf.IsHub {
		t.Error("expected IsHub = true")
	}
	if wf.Importers != 10 {
		t.Errorf("expected importers 10, got %d", wf.Importers)
	}
	if ws.HubCount() != 1 {
		t.Errorf("expected hub count 1, got %d", ws.HubCount())
	}
}

func TestWorkingSet_Remove(t *testing.T) {
	ws := NewWorkingSet()
	ws.Touch("a.go", 1, false, 0)
	ws.Touch("b.go", 2, false, 0)
	ws.Remove("a.go")

	if ws.Size() != 1 {
		t.Errorf("expected 1 file after remove, got %d", ws.Size())
	}
	if _, ok := ws.Files["a.go"]; ok {
		t.Error("a.go should have been removed")
	}
}

func TestWorkingSet_ActiveFiles(t *testing.T) {
	ws := NewWorkingSet()
	ws.Touch("recent.go", 5, false, 0)

	// All files should be active within a large window
	active := ws.ActiveFiles(1 * time.Hour)
	if len(active) != 1 {
		t.Errorf("expected 1 active file, got %d", len(active))
	}

	// No files should be active with zero window (edge case)
	active = ws.ActiveFiles(0)
	if len(active) != 0 {
		t.Errorf("expected 0 active files with zero window, got %d", len(active))
	}
}

func TestWorkingSet_HotFiles(t *testing.T) {
	ws := NewWorkingSet()
	ws.Touch("hot.go", 1, false, 0)
	ws.Touch("hot.go", 2, false, 0)
	ws.Touch("hot.go", 3, false, 0) // 3 edits
	ws.Touch("warm.go", 1, false, 0)
	ws.Touch("warm.go", 2, false, 0) // 2 edits
	ws.Touch("cold.go", 1, false, 0) // 1 edit

	hot := ws.HotFiles(2)
	if len(hot) != 2 {
		t.Fatalf("expected 2 hot files, got %d", len(hot))
	}
	if hot[0].Path != "hot.go" {
		t.Errorf("expected hot.go first, got %s", hot[0].Path)
	}
	if hot[1].Path != "warm.go" {
		t.Errorf("expected warm.go second, got %s", hot[1].Path)
	}
}

func TestWorkingSet_HotFilesEmpty(t *testing.T) {
	ws := NewWorkingSet()
	hot := ws.HotFiles(5)
	if hot != nil {
		t.Errorf("expected nil for empty working set, got %v", hot)
	}
}

func TestWorkingSet_HubCount(t *testing.T) {
	ws := NewWorkingSet()
	ws.Touch("a.go", 1, true, 5)
	ws.Touch("b.go", 1, false, 1)
	ws.Touch("c.go", 1, true, 3)

	if ws.HubCount() != 2 {
		t.Errorf("expected 2 hubs, got %d", ws.HubCount())
	}
}
