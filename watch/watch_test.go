package watch

import (
	"os"
	"path/filepath"
	"strings"
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

// waitForWatching polls until the daemon's watcher has at least one path
// registered, which is the point after which a write can be observed.
func waitForWatching(t *testing.T, daemon *Daemon) {
	t.Helper()
	waitForWatchCondition(t, 5*time.Second, func() bool {
		return len(daemon.watcher.WatchList()) > 0
	})
}

// writeEventFor returns the most recent WRITE event for path, if any.
func writeEventFor(daemon *Daemon, path string) (Event, bool) {
	events := daemon.GetEvents(100)
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Op == "WRITE" && events[i].Path == path {
			return events[i], true
		}
	}
	return Event{}, false
}

// waitForWriteEvent polls until a WRITE event for path exists or the deadline
// passes. It reports whether one arrived; callers decide what a miss means.
func waitForWriteEvent(daemon *Daemon, path string, timeout time.Duration) (Event, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if event, ok := writeEventFor(daemon, path); ok {
			return event, true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return writeEventFor(daemon, path)
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

	waitForWatching(t, daemon)

	// Modify the file (with different content to ensure a change)
	newContent := "package main\n\nfunc main() {}\n\n// new line added\n"
	if err := os.WriteFile(testFile, []byte(newContent), 0644); err != nil {
		t.Fatalf("Failed to modify test file: %v", err)
	}

	// Poll for the WRITE event instead of sleeping a fixed interval (#135).
	event, ok := waitForWriteEvent(daemon, "test.go", 5*time.Second)
	if !ok {
		if len(daemon.GetEvents(10)) == 0 {
			t.Skip("fsnotify may not work reliably in temp directories on this platform")
		}
		t.Fatal("Expected WRITE event for test.go")
	}
	if event.Delta <= 0 {
		t.Errorf("Expected positive line delta, got %d", event.Delta)
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

	waitForWatching(t, daemon)

	// Add 2 more lines
	newContent := "line1\nline2\nline3\nline4\nline5\n" // 5 lines
	if err := os.WriteFile(testFile, []byte(newContent), 0644); err != nil {
		t.Fatalf("Failed to modify test file: %v", err)
	}

	// Poll for the WRITE event instead of sleeping a fixed interval (#135).
	event, ok := waitForWriteEvent(daemon, "counter.go", 5*time.Second)
	if !ok {
		if len(daemon.GetEvents(10)) == 0 {
			t.Skip("fsnotify may not work reliably in temp directories on this platform")
		}
		t.Fatal("No WRITE event found for counter.go")
	}
	if event.Delta != 2 {
		t.Errorf("Expected delta of +2, got %d", event.Delta)
	}
	if event.Lines != 5 {
		t.Errorf("Expected 5 lines, got %d", event.Lines)
	}
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

func TestDebounce(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codemap-watch-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	if err := os.MkdirAll(filepath.Join(tmpDir, ".codemap"), 0o755); err != nil {
		t.Fatalf("Failed to create config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, ".codemap", "config.json"), []byte(`{"exclude":["rapid.go"]}`), 0o644); err != nil {
		t.Fatalf("Failed to exclude debounce fixture: %v", err)
	}

	testFile := filepath.Join(tmpDir, "rapid.go")
	if err := os.WriteFile(testFile, []byte("package rapid\n"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	daemon, err := NewDaemon(tmpDir, false)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}
	// A generous window so five writes a few milliseconds apart always fall
	// inside one debounce window, however slowly the runner schedules them;
	// with the 100ms default a loaded CI host spread them over four events
	// (#135). The default is untouched for production.
	daemon.debounceWindow = time.Second

	if err := daemon.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer daemon.Stop()

	waitForWatching(t, daemon)

	// Rapid fire writes (within debounce window)
	for i := 0; i < 5; i++ {
		content := []byte("package rapid\n" + strings.Repeat("// line\n", i+1))
		if err := os.WriteFile(testFile, content, 0644); err != nil {
			t.Fatalf("Failed to modify file: %v", err)
		}
	}

	countWrites := func() (writeCount, lastWriteLines int) {
		for _, e := range daemon.GetEvents(100) {
			if e.Op == "WRITE" && e.Path == "rapid.go" {
				writeCount++
				lastWriteLines = e.Lines
			}
		}
		return writeCount, lastWriteLines
	}
	// Poll for the trailing event instead of sleeping past the window.
	waitForWatchCondition(t, 5*time.Second, func() bool {
		writeCount, lastWriteLines := countWrites()
		return writeCount >= 1 && lastWriteLines == 6
	})
	writeCount, lastWriteLines := countWrites()

	// The first write may be processed immediately; the changed-size burst must
	// collapse to at most one trailing event with the latest contents.
	if writeCount == 0 || writeCount > 2 {
		t.Errorf("Expected 1-2 debounced events, got %d", writeCount)
	}
	if lastWriteLines != 6 {
		t.Errorf("Expected trailing event with 6 lines, got %d", lastWriteLines)
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

func TestWatchIgnoresGitignoredDirectories(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codemap-watch-gitignore-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte("third_party/\n"), 0644); err != nil {
		t.Fatalf("Failed to create .gitignore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "tracked.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatalf("Failed to create tracked file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmpDir, "third_party", "freerdp"), 0755); err != nil {
		t.Fatalf("Failed to create ignored dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "third_party", "freerdp", "ignored.go"), []byte("package ignored\n"), 0644); err != nil {
		t.Fatalf("Failed to create ignored file: %v", err)
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

	if err := os.WriteFile(filepath.Join(tmpDir, "tracked.go"), []byte("package main\n// touched\n"), 0644); err != nil {
		t.Fatalf("Failed to modify tracked file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "third_party", "freerdp", "ignored.go"), []byte("package ignored\n// touched\n"), 0644); err != nil {
		t.Fatalf("Failed to modify ignored file: %v", err)
	}

	time.Sleep(700 * time.Millisecond)

	events := daemon.GetEvents(200)
	if len(events) == 0 {
		t.Skip("fsnotify may not work reliably in temp directories on this platform")
	}

	foundTrackedWrite := false
	for _, e := range events {
		if e.Op == "WRITE" && e.Path == "tracked.go" {
			foundTrackedWrite = true
		}
		if strings.HasPrefix(filepath.ToSlash(e.Path), "third_party/") {
			t.Fatalf("expected gitignored path to be excluded from watcher events, got: %s %s", e.Op, e.Path)
		}
	}

	if !foundTrackedWrite {
		t.Fatalf("expected a WRITE event for tracked.go, got events: %+v", events)
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
