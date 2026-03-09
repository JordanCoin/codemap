package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codemap/handoff"
	"codemap/watch"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTextResultAndErrorResult(t *testing.T) {
	ok := textResult("ready")
	if ok.IsError {
		t.Fatal("textResult should not mark errors")
	}
	if got := resultText(t, ok); got != "ready" {
		t.Fatalf("textResult text = %q, want ready", got)
	}

	bad := errorResult("boom")
	if !bad.IsError {
		t.Fatal("errorResult should mark IsError")
	}
	if got := resultText(t, bad); got != "boom" {
		t.Fatalf("errorResult text = %q, want boom", got)
	}
}

func TestStripANSI(t *testing.T) {
	in := "\x1b[31mred\x1b[0m plain"
	if got := stripANSI(in); got != "red plain" {
		t.Fatalf("stripANSI(%q) = %q", in, got)
	}
}

func TestGetProjectStatsAndHandleListProjects(t *testing.T) {
	parent := t.TempDir()
	alpha := filepath.Join(parent, "alpha")
	beta := filepath.Join(parent, "beta")
	if err := os.MkdirAll(filepath.Join(alpha, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(beta, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(alpha, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beta, "app.py"), []byte("print('ok')\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stats := getProjectStats(alpha)
	if !strings.Contains(stats, "1 files") || !strings.Contains(stats, "Go") || !strings.Contains(stats, "[git]") {
		t.Fatalf("unexpected project stats: %s", stats)
	}

	res, _, err := handleListProjects(context.Background(), nil, ListProjectsInput{Path: parent, Pattern: "alp"})
	if err != nil {
		t.Fatalf("handleListProjects error: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "Projects matching 'alp'") || !strings.Contains(out, "alpha/") || strings.Contains(out, "beta/") {
		t.Fatalf("unexpected list_projects output:\n%s", out)
	}
}

func TestHandleFindFileAndStatus(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "helper.go"), []byte("package nested\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	findRes, _, err := handleFindFile(context.Background(), nil, FindInput{Path: root, Pattern: "main"})
	if err != nil {
		t.Fatalf("handleFindFile error: %v", err)
	}
	findOut := resultText(t, findRes)
	if !strings.Contains(findOut, "Found 1 files") || !strings.Contains(findOut, "main.go") {
		t.Fatalf("unexpected find output:\n%s", findOut)
	}

	missingRes, _, err := handleFindFile(context.Background(), nil, FindInput{Path: root, Pattern: "absent"})
	if err != nil {
		t.Fatalf("handleFindFile missing error: %v", err)
	}
	if !strings.Contains(resultText(t, missingRes), "No files found matching 'absent'") {
		t.Fatalf("unexpected missing-file output:\n%s", resultText(t, missingRes))
	}

	watchersMu.Lock()
	watchers = map[string]*watch.Daemon{"/tmp/demo": nil}
	watchersMu.Unlock()
	t.Cleanup(func() {
		watchersMu.Lock()
		watchers = make(map[string]*watch.Daemon)
		watchersMu.Unlock()
	})

	statusRes, _, err := handleStatus(context.Background(), nil, EmptyInput{})
	if err != nil {
		t.Fatalf("handleStatus error: %v", err)
	}
	statusOut := resultText(t, statusRes)
	if !strings.Contains(statusOut, "codemap MCP server") || !strings.Contains(statusOut, "Active watchers: 1 active: /tmp/demo") {
		t.Fatalf("unexpected status output:\n%s", statusOut)
	}
}

func TestHandleGetStructureUsesStateHubs(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := watch.State{
		UpdatedAt: time.Now(),
		Hubs:      []string{"pkg/main.go"},
		Importers: map[string][]string{"pkg/main.go": {"a.go", "b.go", "c.go"}},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".codemap", "state.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	res, _, err := handleGetStructure(context.Background(), nil, PathInput{Path: root})
	if err != nil {
		t.Fatalf("handleGetStructure error: %v", err)
	}
	out := resultText(t, res)
	checks := []string{"HUB FILES", "pkg/main.go", "3 importers"}
	for _, check := range checks {
		if !strings.Contains(out, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, out)
		}
	}
}

func TestHandleGetHandoffValidationAndLatest(t *testing.T) {
	root := t.TempDir()

	res, _, err := handleGetHandoff(context.Background(), nil, HandoffInput{Path: root, Prefix: true, Delta: true})
	if err != nil {
		t.Fatalf("handleGetHandoff validation error: %v", err)
	}
	if !strings.Contains(resultText(t, res), "mutually exclusive") {
		t.Fatalf("expected mutual exclusion error, got:\n%s", resultText(t, res))
	}

	missing, _, err := handleGetHandoff(context.Background(), nil, HandoffInput{Path: root, Latest: true})
	if err != nil {
		t.Fatalf("handleGetHandoff latest error: %v", err)
	}
	if !strings.Contains(resultText(t, missing), "No saved handoff found") {
		t.Fatalf("expected missing latest message, got:\n%s", resultText(t, missing))
	}

	artifact := &handoff.Artifact{
		SchemaVersion: handoff.SchemaVersion,
		Branch:        "feature/test",
		BaseRef:       "main",
		Prefix:        handoff.PrefixSnapshot{FileCount: 7},
		Delta:         handoff.DeltaSnapshot{Changed: []handoff.FileStub{{Path: "main.go"}}},
	}
	if err := handoff.WriteLatest(root, artifact); err != nil {
		t.Fatalf("WriteLatest error: %v", err)
	}

	jsonRes, _, err := handleGetHandoff(context.Background(), nil, HandoffInput{Path: root, Latest: true, JSON: true, Prefix: true})
	if err != nil {
		t.Fatalf("handleGetHandoff latest json error: %v", err)
	}
	jsonOut := resultText(t, jsonRes)
	if !strings.Contains(jsonOut, "\"file_count\": 7") {
		t.Fatalf("expected prefix JSON payload, got:\n%s", jsonOut)
	}
}

func TestHandleGetHandoffRejectsInvalidSince(t *testing.T) {
	root := t.TempDir()

	invalid, _, err := handleGetHandoff(context.Background(), nil, HandoffInput{Path: root, Since: "later"})
	if err != nil {
		t.Fatalf("unexpected error for invalid since result: %v", err)
	}
	if !strings.Contains(resultText(t, invalid), "Invalid since duration") {
		t.Fatalf("expected invalid duration message, got:\n%s", resultText(t, invalid))
	}

	zero, _, err := handleGetHandoff(context.Background(), nil, HandoffInput{Path: root, Since: "0s"})
	if err != nil {
		t.Fatalf("unexpected error for zero since result: %v", err)
	}
	if !strings.Contains(resultText(t, zero), "must be > 0") {
		t.Fatalf("expected non-positive duration message, got:\n%s", resultText(t, zero))
	}
}

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if len(res.Content) != 1 {
		t.Fatalf("expected exactly one content item, got %d", len(res.Content))
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected text content, got %T", res.Content[0])
	}
	return text.Text
}
