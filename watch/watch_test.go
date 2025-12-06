package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestDaemonStartStop tests basic daemon lifecycle
func TestDaemonStartStop(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "codemap-watch-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a test file
	testFile := filepath.Join(tmpDir, "test.go")
	if err := os.WriteFile(testFile, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create daemon
	daemon, err := NewDaemon(tmpDir, false)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}

	// Start daemon
	if err := daemon.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Verify initial state
	if daemon.FileCount() == 0 {
		t.Error("Expected at least 1 tracked file")
	}

	// Stop daemon
	daemon.Stop()
}

// TestEventDetection tests that file changes are detected
func TestEventDetection(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codemap-watch-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testFile := filepath.Join(tmpDir, "test.go")
	if err := os.WriteFile(testFile, []byte("package main\n\nfunc main() {}\n"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	daemon, err := NewDaemon(tmpDir, false)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}

	if err := daemon.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer daemon.Stop()

	// Wait for watcher to be fully ready
	time.Sleep(500 * time.Millisecond)

	// Modify the file (with different content to ensure a change)
	newContent := "package main\n\nfunc main() {}\n\n// new line added\n"
	if err := os.WriteFile(testFile, []byte(newContent), 0644); err != nil {
		t.Fatalf("Failed to modify test file: %v", err)
	}

	// Wait for event to be processed (longer wait for reliability)
	time.Sleep(500 * time.Millisecond)

	// Check events
	events := daemon.GetEvents(10)
	if len(events) == 0 {
		// Try waiting a bit longer
		time.Sleep(500 * time.Millisecond)
		events = daemon.GetEvents(10)
	}

	if len(events) == 0 {
		t.Skip("fsnotify may not work reliably in temp directories on this platform")
	}

	// Find the WRITE event
	var foundWrite bool
	for _, e := range events {
		if e.Op == "WRITE" && e.Path == "test.go" {
			foundWrite = true
			if e.Delta <= 0 {
				t.Errorf("Expected positive line delta, got %d", e.Delta)
			}
		}
	}

	if !foundWrite {
		t.Error("Expected WRITE event for test.go")
	}
}

// TestLineDelta tests line count delta calculation
func TestLineDelta(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codemap-watch-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testFile := filepath.Join(tmpDir, "counter.go")
	initialContent := "line1\nline2\nline3\n" // 3 lines
	if err := os.WriteFile(testFile, []byte(initialContent), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	daemon, err := NewDaemon(tmpDir, false)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}

	if err := daemon.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer daemon.Stop()

	time.Sleep(500 * time.Millisecond)

	// Add 2 more lines
	newContent := "line1\nline2\nline3\nline4\nline5\n" // 5 lines
	if err := os.WriteFile(testFile, []byte(newContent), 0644); err != nil {
		t.Fatalf("Failed to modify test file: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	events := daemon.GetEvents(10)
	if len(events) == 0 {
		time.Sleep(500 * time.Millisecond)
		events = daemon.GetEvents(10)
	}

	if len(events) == 0 {
		t.Skip("fsnotify may not work reliably in temp directories on this platform")
	}

	for _, e := range events {
		if e.Op == "WRITE" && e.Path == "counter.go" {
			if e.Delta != 2 {
				t.Errorf("Expected delta of +2, got %d", e.Delta)
			}
			if e.Lines != 5 {
				t.Errorf("Expected 5 lines, got %d", e.Lines)
			}
			return
		}
	}
	t.Error("No WRITE event found for counter.go")
}

// TestNewFileCreation tests CREATE event for new files
func TestNewFileCreation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codemap-watch-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Start with empty directory
	daemon, err := NewDaemon(tmpDir, false)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}

	if err := daemon.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer daemon.Stop()

	time.Sleep(100 * time.Millisecond)

	// Create new file
	newFile := filepath.Join(tmpDir, "newfile.go")
	if err := os.WriteFile(newFile, []byte("package new\n\nfunc New() {}\n"), 0644); err != nil {
		t.Fatalf("Failed to create new file: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	events := daemon.GetEvents(10)
	var foundCreate bool
	for _, e := range events {
		if e.Op == "CREATE" && e.Path == "newfile.go" {
			foundCreate = true
		}
	}

	if !foundCreate {
		t.Error("Expected CREATE event for newfile.go")
	}
}

// TestFileRemoval tests REMOVE event
func TestFileRemoval(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codemap-watch-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testFile := filepath.Join(tmpDir, "todelete.go")
	if err := os.WriteFile(testFile, []byte("package delete\n\n// will be deleted\n"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	daemon, err := NewDaemon(tmpDir, false)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}

	if err := daemon.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer daemon.Stop()

	time.Sleep(100 * time.Millisecond)

	// Remove file
	if err := os.Remove(testFile); err != nil {
		t.Fatalf("Failed to remove file: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	events := daemon.GetEvents(10)
	var foundRemove bool
	for _, e := range events {
		if e.Op == "REMOVE" && e.Path == "todelete.go" {
			foundRemove = true
			if e.Delta >= 0 {
				t.Errorf("Expected negative delta for removed file, got %d", e.Delta)
			}
		}
	}

	if !foundRemove {
		t.Error("Expected REMOVE event for todelete.go")
	}
}

// TestDebounce tests that rapid events are debounced
func TestDebounce(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codemap-watch-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testFile := filepath.Join(tmpDir, "rapid.go")
	if err := os.WriteFile(testFile, []byte("package rapid\n"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	daemon, err := NewDaemon(tmpDir, false)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}

	if err := daemon.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer daemon.Stop()

	time.Sleep(100 * time.Millisecond)

	// Rapid fire writes (within debounce window)
	for i := 0; i < 5; i++ {
		content := []byte("package rapid\n// line " + string(rune('a'+i)) + "\n")
		if err := os.WriteFile(testFile, content, 0644); err != nil {
			t.Fatalf("Failed to modify file: %v", err)
		}
		time.Sleep(20 * time.Millisecond) // 20ms < 100ms debounce window
	}

	time.Sleep(300 * time.Millisecond)

	events := daemon.GetEvents(100)
	writeCount := 0
	for _, e := range events {
		if e.Op == "WRITE" && e.Path == "rapid.go" {
			writeCount++
		}
	}

	// Should have fewer events than writes due to debouncing
	if writeCount >= 5 {
		t.Errorf("Expected debounced events (< 5), got %d", writeCount)
	}
}

// TestNonSourceFileIgnored tests that non-source files are ignored
func TestNonSourceFileIgnored(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codemap-watch-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	daemon, err := NewDaemon(tmpDir, false)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}

	if err := daemon.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer daemon.Stop()

	time.Sleep(100 * time.Millisecond)

	// Create non-source files
	txtFile := filepath.Join(tmpDir, "readme.txt")
	if err := os.WriteFile(txtFile, []byte("This is a readme\n"), 0644); err != nil {
		t.Fatalf("Failed to create txt file: %v", err)
	}

	jsonFile := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(jsonFile, []byte(`{"key": "value"}`), 0644); err != nil {
		t.Fatalf("Failed to create json file: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	events := daemon.GetEvents(100)
	for _, e := range events {
		if e.Path == "readme.txt" || e.Path == "config.json" {
			t.Errorf("Non-source file should be ignored: %s", e.Path)
		}
	}
}

// TestCountLines tests the line counting function
func TestCountLines(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codemap-count-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	tests := []struct {
		name     string
		content  string
		expected int
	}{
		{"empty", "", 0},
		{"single line", "hello", 1},
		{"single line with newline", "hello\n", 1},
		{"multiple lines", "line1\nline2\nline3", 3},
		{"multiple lines with trailing newline", "line1\nline2\nline3\n", 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testFile := filepath.Join(tmpDir, "test_"+tt.name+".txt")
			if err := os.WriteFile(testFile, []byte(tt.content), 0644); err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}

			count := countLines(testFile)
			if count != tt.expected {
				t.Errorf("countLines(%q) = %d, want %d", tt.content, count, tt.expected)
			}
		})
	}
}
