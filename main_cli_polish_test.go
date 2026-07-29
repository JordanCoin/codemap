package main

import (
	"strings"
	"testing"

	"codemap/scanner"
)

func TestRenderImportersReportExplainsEmptyResult(t *testing.T) {
	report := scanner.ImportersReport{
		Root: "/repo",
		Mode: "importers",
		File: "watch/events.go",
	}

	var buf strings.Builder
	renderImportersReportCLI(&buf, report)
	out := buf.String()
	if !strings.Contains(out, "No files import watch/events.go") {
		t.Fatalf("empty importers result must say so instead of printing nothing:\n%q", out)
	}
	if !strings.Contains(out, "same package") {
		t.Fatalf("empty result should explain the same-package limitation for Go:\n%q", out)
	}

	// The token-budgeted blast-radius renderer must keep omitting empty
	// sections — the explanation is CLI-only.
	if bundleOut := renderImportersReportString(report); strings.Contains(bundleOut, "No files import") {
		t.Fatalf("bundle renderer should omit empty sections, got:\n%q", bundleOut)
	}
}

func TestNonexistentPathGetsFriendlyError(t *testing.T) {
	_, stderr, err := runCodemapWithInput("", "drift")
	if err == nil {
		t.Fatal("expected non-zero exit for nonexistent path")
	}
	if !strings.Contains(stderr, `"drift" does not exist`) {
		t.Fatalf("expected friendly missing-path error, got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "--help") {
		t.Fatalf("error should point at --help for available subcommands, got:\n%s", stderr)
	}
}

func TestHelpListsAllSubcommands(t *testing.T) {
	out, _, err := runCodemapWithInput("", "--help")
	if err != nil {
		t.Fatalf("--help failed: %v", err)
	}
	for _, sub := range []string{
		"codemap watch", "codemap hook", "codemap config", "codemap setup",
		"codemap doctor", "codemap mcp", "codemap skill", "codemap plugin",
		"codemap context", "codemap serve", "codemap handoff", "codemap blast-radius",
	} {
		if !strings.Contains(out, sub) {
			t.Fatalf("--help does not mention %q:\n%s", sub, out)
		}
	}
}
