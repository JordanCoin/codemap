package watch

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codemap/limits"
)

func TestAppendBoundedEventsCapsHistory(t *testing.T) {
	total := limits.MaxDaemonEvents + 200
	events := make([]Event, 0, total)
	for i := 0; i < total; i++ {
		events = appendBoundedEvents(events, Event{
			Time: time.Unix(int64(i), 0),
			Path: fmt.Sprintf("file%04d.go", i),
		})
	}

	if len(events) != limits.MaxDaemonEvents {
		t.Fatalf("expected %d retained events, got %d", limits.MaxDaemonEvents, len(events))
	}

	firstExpected := fmt.Sprintf("file%04d.go", total-limits.MaxDaemonEvents)
	lastExpected := fmt.Sprintf("file%04d.go", total-1)
	if events[0].Path != firstExpected {
		t.Fatalf("expected first retained event %q, got %q", firstExpected, events[0].Path)
	}
	if events[len(events)-1].Path != lastExpected {
		t.Fatalf("expected last retained event %q, got %q", lastExpected, events[len(events)-1].Path)
	}
}

func TestTrimEventLogToBytes(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "events.log")

	var sb strings.Builder
	for i := 0; i < 1200; i++ {
		fmt.Fprintf(&sb, "2026-02-27 12:00:00 | WRITE  | src/file%04d.go |  100 |    +1 | dirty\n", i)
	}
	if err := os.WriteFile(logPath, []byte(sb.String()), 0644); err != nil {
		t.Fatalf("failed to seed log file: %v", err)
	}

	if err := trimEventLogToBytes(logPath, 4096, 2048); err != nil {
		t.Fatalf("trimEventLogToBytes returned error: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read trimmed log: %v", err)
	}
	if len(data) > 2048 {
		t.Fatalf("expected trimmed log <= 2048 bytes, got %d", len(data))
	}

	content := string(data)
	if strings.Contains(content, "file0001.go") {
		t.Fatalf("expected oldest entries to be trimmed out, got unexpected early entry")
	}
	if !strings.Contains(content, "file1199.go") {
		t.Fatalf("expected newest entry to be retained after trim")
	}
}
