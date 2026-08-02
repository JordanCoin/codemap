package codemapmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"codemap/handoff"
	"codemap/scanner"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAnalysisToolsAdvertiseAndReturnStructuredEnvelopes(t *testing.T) {
	root := writeParityRepository(t)
	session := connectParitySession(t)
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"get_structure", "get_diff", "get_importers", "get_handoff"} {
		if findParityTool(t, tools.Tools, name).OutputSchema == nil {
			t.Fatalf("%s does not advertise an output schema", name)
		}
	}

	cases := []struct {
		label string
		tool  string
		args  map[string]any
		kind  string
	}{
		{"structure", "get_structure", map[string]any{"path": root, "depth": 2}, "structure"},
		{"diff", "get_diff", map[string]any{"path": root, "ref": "HEAD~1"}, "diff"},
		{"empty_diff", "get_diff", map[string]any{"path": root, "ref": "HEAD"}, "empty"},
		{"importers", "get_importers", map[string]any{"path": root, "file": "pkg/types/types.go"}, "importers"},
		{"absolute_importers", "get_importers", map[string]any{"path": root, "file": filepath.Join(root, "pkg/types/types.go")}, "importers"},
		{"empty_importers", "get_importers", map[string]any{"path": root, "file": "main.go"}, "empty"},
		{"handoff", "get_handoff", map[string]any{"path": root, "ref": "HEAD~1"}, "artifact"},
		{"handoff_prefix", "get_handoff", map[string]any{"path": root, "ref": "HEAD~1", "prefix": true}, "prefix"},
		{"handoff_delta", "get_handoff", map[string]any{"path": root, "ref": "HEAD~1", "delta": true}, "delta"},
		{"missing_handoff", "get_handoff", map[string]any{"path": root, "latest": true}, "not_found"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tc.tool, Arguments: tc.args})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError || result.StructuredContent == nil || resultText(t, result) == "" {
				t.Fatalf("%s result = %#v", tc.tool, result)
			}
			payload := structuredParityMap(t, result.StructuredContent)
			if payload["kind"] != tc.kind {
				t.Fatalf("%s kind = %#v, want %q", tc.tool, payload["kind"], tc.kind)
			}
			if _, ok := payload["truncated"]; !ok {
				t.Fatalf("%s payload lacks truncation metadata: %#v", tc.tool, payload)
			}
			if _, ok := payload["omitted_count"]; !ok {
				t.Fatalf("%s payload lacks omitted count: %#v", tc.tool, payload)
			}
		})
	}
}

func TestStructuredImporterPreservesCLIReportFields(t *testing.T) {
	root := writeParityRepository(t)
	session := connectParitySession(t)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_importers",
		Arguments: map[string]any{"path": root, "file": "pkg/types/types.go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := structuredParityMap(t, result.StructuredContent)
	for _, field := range []string{"imports", "importers", "hub_imports", "importer_count", "is_hub", "coverage_status", "coverage_notes"} {
		if _, ok := payload[field]; !ok {
			t.Fatalf("structured importer payload lacks %q: %#v", field, payload)
		}
	}
}

func TestNormalizeImporterFileUsesNativeGraphKeys(t *testing.T) {
	root := filepath.Join(string(filepath.Separator)+"repo", "project")
	want := filepath.Join("pkg", "types", "types.go")
	for _, input := range []string{
		"pkg/types/types.go",
		filepath.Join(root, "pkg", "types", "types.go"),
	} {
		if got := normalizeImporterFile(root, input); got != want {
			t.Fatalf("normalizeImporterFile(%q) = %q, want native key %q", input, got, want)
		}
	}
}

func TestHandoffVariantTruncationTracksSelectedPayload(t *testing.T) {
	artifact := &handoff.Artifact{
		Prefix: handoff.PrefixSnapshot{Hubs: []handoff.HubSummary{{Path: "hub.go"}}},
		Delta:  handoff.DeltaSnapshot{Changed: make([]handoff.FileStub, maxStructuredItems+1)},
	}
	if output := newHandoffOutput("prefix", artifact); output.Truncated || output.OmittedCount != 0 {
		t.Fatalf("prefix metadata counted omitted delta fields: %#v", output)
	}
	artifact.Prefix.Hubs = make([]handoff.HubSummary, maxStructuredItems+1)
	artifact.Delta.Changed = []handoff.FileStub{{Path: "main.go"}}
	if output := newHandoffOutput("delta", artifact); output.Truncated || output.OmittedCount != 0 {
		t.Fatalf("delta metadata counted omitted prefix fields: %#v", output)
	}
}

func TestHandoffJSONPreservesLegacyPayloadAndBoundsStructuredContent(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/handoffbound\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "target.go"), []byte("package pkg\n\ntype Target struct{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed := make([]handoff.FileStub, maxStructuredItems+3)
	for index := range changed {
		changed[index] = handoff.FileStub{Path: fmt.Sprintf("file-%03d.go", index)}
	}
	changed[0].Path = "pkg/target.go"
	artifact := &handoff.Artifact{SchemaVersion: handoff.SchemaVersion, Root: root, Delta: handoff.DeltaSnapshot{Changed: changed}}
	if err := handoff.WriteLatest(root, artifact); err != nil {
		t.Fatal(err)
	}

	result, structured, err := handleGetHandoff(context.Background(), nil, HandoffInput{Path: root, Latest: true, JSON: true})
	if err != nil {
		t.Fatal(err)
	}
	var payload handoff.Artifact
	if err := json.Unmarshal([]byte(resultText(t, result)), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Delta.Changed) != len(changed) {
		t.Fatalf("legacy JSON artifact changed files = %d, want %d", len(payload.Delta.Changed), len(changed))
	}
	// The artifact includes both the delta and its legacy top-level mirror.
	output := structured.(HandoffOutput)
	if len(output.Artifact.Delta.Changed) != maxStructuredItems || !output.Truncated || output.OmittedCount != 3 {
		t.Fatalf("bounded structured artifact = %#v", output)
	}

	events := make([]handoff.EventSummary, maxStructuredItems+3)
	for index := range events {
		events[index].Path = "pkg/target.go"
	}
	artifact.Delta.RecentEvents = events
	if err := handoff.WriteLatest(root, artifact); err != nil {
		t.Fatal(err)
	}
	result, structured, err = handleGetHandoff(context.Background(), nil, HandoffInput{Path: root, Latest: true, File: "pkg/target.go", JSON: true})
	if err != nil {
		t.Fatal(err)
	}
	var detail handoff.FileDetail
	if err := json.Unmarshal([]byte(resultText(t, result)), &detail); err != nil {
		t.Fatal(err)
	}
	detailOutput := structured.(HandoffOutput)
	if len(detail.RecentEvents) != len(events) || len(detailOutput.FileDetail.RecentEvents) != maxStructuredItems || detailOutput.OmittedCount != 3 {
		t.Fatalf("legacy detail events = %d; bounded structured detail = %#v", len(detail.RecentEvents), detailOutput)
	}
}

func TestStructuredCollectionsAreBounded(t *testing.T) {
	values := make([]string, maxStructuredItems+3)
	bounded, omitted := boundedSlice(values)
	if len(bounded) != maxStructuredItems || omitted != 3 {
		t.Fatalf("bounded collection = %d items, %d omitted", len(bounded), omitted)
	}
	if bounded == nil {
		t.Fatal("bounded empty-compatible collections must serialize as arrays")
	}
}

func structuredParityMap(t *testing.T, value any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func writeParityRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for path, body := range map[string]string{
		"go.mod":             "module example.com/parity\n\ngo 1.22\n",
		"main.go":            "package main\n\nimport \"example.com/parity/pkg/types\"\n\nfunc main() { _ = types.Item{} }\n",
		"pkg/types/types.go": "package types\n\ntype Item struct{}\n",
	} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGitMCPTestCmd(t, root, "init", "-b", "main")
	runGitMCPTestCmd(t, root, "add", ".")
	runGitMCPTestCmd(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "fixture")
	if err := os.WriteFile(filepath.Join(root, "pkg/types/types.go"), []byte("package types\n\ntype Item struct{ Name string }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitMCPTestCmd(t, root, "add", ".")
	runGitMCPTestCmd(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "change")
	return root
}

func TestNewImportersOutputSortsDeterministically(t *testing.T) {
	graph := &scanner.FileGraph{
		Importers: map[string][]string{
			"main.go": {"zebra.go", "alpha.go", "mango.go"},
			"hub.go":  {"p.go", "q.go", "r.go"},
		},
		Imports: map[string][]string{
			"main.go": {"zeta.go", "beta.go", "hub.go", "apple.go"},
		},
	}
	out := newImportersOutput("/repo", "main.go", graph)
	want := []string{"alpha.go", "mango.go", "zebra.go"}
	if !slices.Equal(out.Importers, want) {
		t.Fatalf("importers = %v, want sorted %v", out.Importers, want)
	}
	wantImports := []string{"apple.go", "beta.go", "hub.go", "zeta.go"}
	if !slices.Equal(out.Imports, wantImports) {
		t.Fatalf("imports = %v, want sorted %v", out.Imports, wantImports)
	}
	wantHubs := []string{"hub.go"}
	if !slices.Equal(out.HubImports, wantHubs) {
		t.Fatalf("hub_imports = %v, want %v", out.HubImports, wantHubs)
	}
}
