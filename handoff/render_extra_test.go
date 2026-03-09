package handoff

import (
	"strings"
	"testing"
	"time"
)

func TestRenderPrefixMarkdownIncludesHubList(t *testing.T) {
	out := RenderPrefixMarkdown(PrefixSnapshot{
		FileCount: 12,
		Hubs:      []HubSummary{{Path: "internal/router.go", Importers: 4}},
	})

	checks := []string{
		"## Handoff Prefix",
		"- File count: 12",
		"- Hub files: 1",
		"- `internal/router.go` (4 importers)",
	}
	for _, check := range checks {
		if !strings.Contains(out, check) {
			t.Fatalf("expected prefix markdown to contain %q, got:\n%s", check, out)
		}
	}
}

func TestRenderDeltaMarkdownDefaultsChangedStatusAndRiskFiles(t *testing.T) {
	out := RenderDeltaMarkdown(DeltaSnapshot{
		Changed:   []FileStub{{Path: "cmd/hooks.go"}},
		RiskFiles: []RiskFile{{Path: "cmd/hooks.go", Importers: 5}},
	})

	checks := []string{
		"## Handoff Delta",
		"- Changed files: 1",
		"- `cmd/hooks.go` (changed)",
		"### Risk Files",
		"- `cmd/hooks.go` (5 importers)",
	}
	for _, check := range checks {
		if !strings.Contains(out, check) {
			t.Fatalf("expected delta markdown to contain %q, got:\n%s", check, out)
		}
	}
}

func TestRenderFileDetailMarkdownIncludesOptionalSections(t *testing.T) {
	out := RenderFileDetailMarkdown(&FileDetail{
		Path:      "scanner/filegraph.go",
		Status:    "modified",
		Hash:      "abc123",
		Size:      512,
		IsHub:     true,
		Importers: []string{"main.go"},
		Imports:   []string{"scanner/types.go"},
		RecentEvents: []EventSummary{{
			Time:  time.Date(2026, time.March, 9, 10, 0, 0, 0, time.UTC),
			Op:    "WRITE",
			Delta: 7,
		}},
	})

	checks := []string{
		"## Handoff File Detail: `scanner/filegraph.go`",
		"- Status: modified",
		"- Hash: `abc123`",
		"- Size: 512 bytes",
		"- Hub: yes",
		"### Importers",
		"- `main.go`",
		"### Imports",
		"- `scanner/types.go`",
		"### Recent Events",
		"- 10:00:00 `WRITE` (7)",
	}
	for _, check := range checks {
		if !strings.Contains(out, check) {
			t.Fatalf("expected file detail markdown to contain %q, got:\n%s", check, out)
		}
	}
}

func TestRenderHelpersHandleNilInputs(t *testing.T) {
	if RenderMarkdown(nil) != "" {
		t.Fatal("expected nil artifact markdown to be empty")
	}
	if RenderFileDetailMarkdown(nil) != "" {
		t.Fatal("expected nil file detail markdown to be empty")
	}
	if RenderCompact(nil, 0) != "" {
		t.Fatal("expected nil compact render to be empty")
	}
}
