package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codemap/watch"
)

func writeProvenanceState(t *testing.T, root string, paths []string) {
	t.Helper()
	ws := &watch.WorkingSet{Files: map[string]*watch.WorkingFile{}, StartedAt: time.Now()}
	var events []watch.Event
	for _, p := range paths {
		ws.Files[p] = &watch.WorkingFile{Path: p, EditCount: 1, NetDelta: 10, LastTouch: time.Now()}
		events = append(events, watch.Event{Path: p, Op: "WRITE"})
	}
	state := watch.State{
		UpdatedAt:    time.Now(),
		FileCount:    len(paths),
		RecentEvents: events,
		WorkingSet:   ws,
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".codemap", "state.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRecordAgentEditRoundTrip(t *testing.T) {
	root := t.TempDir()

	if err := recordAgentEdit(root, "session-a", filepath.Join(root, "watch", "events.go"), time.Now()); err != nil {
		t.Fatalf("recordAgentEdit: %v", err)
	}
	if err := recordAgentEdit(root, "session-b", "cmd/hooks.go", time.Now()); err != nil {
		t.Fatalf("recordAgentEdit: %v", err)
	}

	edits := loadAgentEdits(root, time.Now().Add(-time.Hour))
	if !edits.paths["watch/events.go"] {
		t.Fatalf("absolute path not normalized to repo-relative slash form: %+v", edits.paths)
	}
	if !edits.paths["cmd/hooks.go"] {
		t.Fatalf("missing recorded edit: %+v", edits.paths)
	}
	if !edits.bySession["session-a"]["watch/events.go"] || edits.bySession["session-a"]["cmd/hooks.go"] {
		t.Fatalf("session attribution wrong: %+v", edits.bySession)
	}

	stale := loadAgentEdits(root, time.Now().Add(time.Hour))
	if len(stale.paths) != 0 {
		t.Fatalf("cutoff not applied: %+v", stale.paths)
	}
}

func TestHookPostEditRecordsAgentEdit(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "pkg", "types.go")

	input := `{"file_path":` + jsonQuote(target) + `,"session_id":"post-edit-session"}`
	withStdinInput(t, input, func() {
		_ = captureOutput(func() {
			if err := hookPostEdit(root); err != nil {
				t.Fatalf("hookPostEdit: %v", err)
			}
		})
	})

	edits := loadAgentEdits(root, time.Now().Add(-time.Minute))
	if !edits.bySession["post-edit-session"]["pkg/types.go"] {
		t.Fatalf("post-edit hook did not record agent edit: %+v", edits.bySession)
	}
}

func jsonQuote(path string) string {
	data, _ := json.Marshal(path)
	return string(data)
}

func TestWorkingSetSummarySeparatesAgentEditsFromDiskChurn(t *testing.T) {
	root := t.TempDir()
	writeProvenanceState(t, root, []string{"a.go", "b.go", "c.go"})
	if err := recordAgentEdit(root, "session-a", "a.go", time.Now()); err != nil {
		t.Fatal(err)
	}

	out := captureOutput(func() { showWorkingSetSummary(root, "session-a") })

	if !strings.Contains(out, "a.go") {
		t.Fatalf("agent-edited file missing from working set:\n%s", out)
	}
	if strings.Contains(out, "b.go") || strings.Contains(out, "c.go") {
		t.Fatalf("disk churn listed as if agent-edited:\n%s", out)
	}
	if !strings.Contains(out, "2 other files changed on disk") {
		t.Fatalf("disk churn should be summarized separately:\n%s", out)
	}
}

func TestWorkingSetSummaryFallsBackToHonestDiskLabel(t *testing.T) {
	root := t.TempDir()
	writeProvenanceState(t, root, []string{"a.go", "b.go"})

	out := captureOutput(func() { showWorkingSetSummary(root, "session-a") })

	if strings.Contains(out, "Working set") {
		t.Fatalf("without recorded agent edits the output must not claim a working set:\n%s", out)
	}
	if !strings.Contains(out, "changed on disk") {
		t.Fatalf("expected honest disk-activity label:\n%s", out)
	}
}

func TestSessionProgressCountsOnlyThisSessionsAgentEdits(t *testing.T) {
	root := t.TempDir()
	writeProvenanceState(t, root, []string{"a.go", "b.go", "c.go", "d.go"})
	if err := recordAgentEdit(root, "session-a", "a.go", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := recordAgentEdit(root, "session-a", "b.go", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := recordAgentEdit(root, "session-b", "c.go", time.Now()); err != nil {
		t.Fatal(err)
	}

	out := captureOutput(func() { showSessionProgress(root, "session-a") })
	if !strings.Contains(out, "2 files edited") {
		t.Fatalf("session progress should count only this session's agent edits:\n%s", out)
	}

	fallback := captureOutput(func() { showSessionProgress(root, "session-without-edits") })
	if strings.Contains(fallback, "files edited") {
		t.Fatalf("without agent edits, progress must not claim edits:\n%s", fallback)
	}
}
