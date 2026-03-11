package watch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codemap/scanner"
)

func TestReadStateStaleButRunning(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codemap-state-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	codemapDir := filepath.Join(tmpDir, ".codemap")
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

	codemapDir := filepath.Join(tmpDir, ".codemap")
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
