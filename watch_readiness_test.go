package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A readiness file caught mid-write reads as zero or partial bytes. Treating
// that as fatal reported a startup failure for a daemon that was starting
// fine, and made TestRunWatchStartWaitsForChildReadinessFailure fail whenever
// the machine was loaded enough to land in the window.
func TestWaitWatchReadinessWaitsThroughPartialWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ready.json")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = os.WriteFile(path, []byte(`{"error":"claim rejected"}`), 0o644)
	}()

	err := waitWatchReadiness(path, 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "claim rejected") {
		t.Fatalf("waitWatchReadiness() = %v, want the daemon's own error once the file is complete", err)
	}
}

func TestWaitWatchReadinessSucceedsAfterPartialWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ready.json")
	if err := os.WriteFile(path, []byte(`{"err`), 0o644); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = os.WriteFile(path, []byte(`{}`), 0o644)
	}()

	if err := waitWatchReadiness(path, 5*time.Second); err != nil {
		t.Fatalf("waitWatchReadiness() = %v, want success once the file is complete", err)
	}
}

// A file that never becomes valid still has to say why, rather than reporting
// only that it timed out.
func TestWaitWatchReadinessReportsPersistentGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ready.json")
	if err := os.WriteFile(path, []byte("not json at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := waitWatchReadiness(path, 200*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "reading daemon readiness") {
		t.Fatalf("waitWatchReadiness() = %v, want the parse failure surfaced", err)
	}
}

// A missing file must still time out rather than hang.
func TestWaitWatchReadinessTimesOutWhenAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "never-written.json")
	err := waitWatchReadiness(path, 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("waitWatchReadiness() = %v, want a timeout", err)
	}
}
