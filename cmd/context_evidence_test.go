package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"codemap/scanner"
	"codemap/watch"
)

func TestContextGraphEvidenceTrueHubAndDeterministicOrder(t *testing.T) {
	files := []scanner.FileInfo{
		{Path: "cmd/context.go"}, {Path: "cmd/alpha.go"},
		{Path: "a.go"}, {Path: "b.go"}, {Path: "c.go"},
	}
	graph := &scanner.FileGraph{
		Imports: map[string][]string{
			"a.go": {"cmd/context.go", "cmd/alpha.go"},
			"b.go": {"cmd/context.go", "cmd/alpha.go"},
			"c.go": {"cmd/context.go", "cmd/alpha.go"},
		},
		Importers: map[string][]string{
			"cmd/context.go": {"a.go", "b.go", "c.go"},
			"cmd/alpha.go":   {"a.go", "b.go", "c.go"},
		},
	}

	envelope := buildContextEnvelopeWithDeps(context.Background(), t.TempDir(), "refactor cmd/context.go", true, testContextEnvelopeDeps(files, graph))

	if envelope.Version != 2 {
		t.Fatalf("version = %d, want 2", envelope.Version)
	}
	if envelope.Project.GraphEvidence.Status != graphEvidenceAvailable || envelope.Project.GraphEvidence.Source != graphEvidenceFreshScan {
		t.Fatalf("graph evidence = %#v", envelope.Project.GraphEvidence)
	}
	if envelope.Project.HubCount == nil || *envelope.Project.HubCount != 2 {
		t.Fatalf("hub count = %#v, want 2", envelope.Project.HubCount)
	}
	if want := []string{"cmd/alpha.go", "cmd/context.go"}; !reflect.DeepEqual(envelope.Project.TopHubs, want) {
		t.Fatalf("top hubs = %#v, want %#v", envelope.Project.TopHubs, want)
	}
	if envelope.Intent == nil || envelope.Intent.RiskLevel != "medium" {
		t.Fatalf("intent = %#v, want medium risk", envelope.Intent)
	}
}

func TestContextEnvelopeV2UnavailableShape(t *testing.T) {
	files := []scanner.FileInfo{{Path: "main.go"}}
	graph := &scanner.FileGraph{
		Packages:  map[string][]string{"main": {"main.go"}},
		Imports:   map[string][]string{},
		Importers: map[string][]string{},
	}
	envelope := buildContextEnvelopeWithDeps(context.Background(), t.TempDir(), "refactor main.go", true, testContextEnvelopeDeps(files, graph))

	data, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	project := decoded["project"].(map[string]any)
	if project["hub_count"] != nil {
		t.Fatalf("hub_count = %#v, want null", project["hub_count"])
	}
	if _, ok := project["top_hubs"]; ok {
		t.Fatalf("top_hubs must be omitted: %s", data)
	}
	evidence := project["graph_evidence"].(map[string]any)
	if evidence["reason"] != graphEvidenceScanIncomplete {
		t.Fatalf("evidence = %#v, want scan_incomplete", evidence)
	}
	if envelope.Intent == nil || envelope.Intent.RiskLevel != "unknown" {
		t.Fatalf("intent = %#v, want unknown risk", envelope.Intent)
	}
}

func TestContextGraphEvidenceLargeRepositorySkipsGraph(t *testing.T) {
	files := make([]scanner.FileInfo, 5001)
	for i := range files {
		files[i].Path = "pkg/file.go"
	}
	deps := testContextEnvelopeDeps(files, nil)
	graphCalls := 0
	deps.buildFileGraph = func(context.Context, string) (*scanner.FileGraph, error) {
		graphCalls++
		return nil, errors.New("must not run")
	}

	envelope := buildContextEnvelopeWithDeps(context.Background(), t.TempDir(), "refactor pkg/file.go", true, deps)

	if graphCalls != 0 {
		t.Fatalf("graph calls = %d, want 0", graphCalls)
	}
	if envelope.Project.GraphEvidence.Reason != graphEvidenceLargeRepository || envelope.Project.HubCount != nil {
		t.Fatalf("project = %#v", envelope.Project)
	}
}

func TestContextGraphEvidenceZeroConfiguredFilesProvesEmpty(t *testing.T) {
	deps := testContextEnvelopeDeps(nil, nil)
	graphCalls := 0
	deps.buildFileGraph = func(context.Context, string) (*scanner.FileGraph, error) {
		graphCalls++
		return nil, nil
	}

	envelope := buildContextEnvelopeWithDeps(context.Background(), t.TempDir(), "inspect project", true, deps)

	if graphCalls != 0 {
		t.Fatalf("graph calls = %d, want 0", graphCalls)
	}
	if envelope.Project.HubCount == nil || *envelope.Project.HubCount != 0 {
		t.Fatalf("hub count = %#v, want numeric zero", envelope.Project.HubCount)
	}
	if envelope.Project.GraphEvidence.Status != graphEvidenceAvailable {
		t.Fatalf("evidence = %#v", envelope.Project.GraphEvidence)
	}
}

func TestContextGraphEvidenceCancellationAndDeadline(t *testing.T) {
	t.Run("cancelled before inventory", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		deps := testContextEnvelopeDeps(nil, nil)
		deps.scanConfiguredFiles = func(ctx context.Context, _ string) ([]scanner.FileInfo, error) {
			return nil, ctx.Err()
		}
		envelope := buildContextEnvelopeWithDeps(ctx, t.TempDir(), "refactor main.go", true, deps)
		if envelope.Project.GraphEvidence.Reason != graphEvidenceCancelled {
			t.Fatalf("evidence = %#v, want cancelled", envelope.Project.GraphEvidence)
		}
	})

	t.Run("aggregate deadline", func(t *testing.T) {
		deps := testContextEnvelopeDeps(nil, nil)
		deps.graphTimeout = time.Millisecond
		deps.scanConfiguredFiles = func(ctx context.Context, _ string) ([]scanner.FileInfo, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		envelope := buildContextEnvelopeWithDeps(context.Background(), t.TempDir(), "refactor main.go", true, deps)
		if envelope.Project.GraphEvidence.Reason != graphEvidenceDeadline {
			t.Fatalf("evidence = %#v, want deadline", envelope.Project.GraphEvidence)
		}
	})
}

func TestContextExactPathResolution(t *testing.T) {
	files := []scanner.FileInfo{{Path: "cmd/context.go"}, {Path: "cmd/nonhub.go"}}
	graph := &scanner.FileGraph{
		Imports: map[string][]string{
			"cmd/nonhub.go": {"cmd/context.go"},
		},
		Importers: map[string][]string{
			"cmd/context.go": {"cmd/nonhub.go"},
		},
	}
	deps := testContextEnvelopeDeps(files, graph)

	for _, tt := range []struct {
		name     string
		prompt   string
		wantFile string
		wantRisk string
	}{
		{name: "present", prompt: "refactor cmd/context.go", wantFile: "cmd/context.go", wantRisk: "low"},
		{name: "separator normalized", prompt: `refactor cmd\context.go`, wantFile: "cmd/context.go", wantRisk: "low"},
		{name: "absent", prompt: "refactor cmd/missing.go", wantRisk: "unknown"},
		{name: "excluded", prompt: "refactor generated/context.go", wantRisk: "unknown"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			envelope := buildContextEnvelopeWithDeps(context.Background(), t.TempDir(), tt.prompt, true, deps)
			if envelope.Intent == nil {
				t.Fatal("missing intent")
			}
			if envelope.Intent.RiskLevel != tt.wantRisk {
				t.Fatalf("risk = %q, want %q", envelope.Intent.RiskLevel, tt.wantRisk)
			}
			if tt.wantFile == "" {
				if len(envelope.Intent.Files) != 0 {
					t.Fatalf("files = %#v, want none", envelope.Intent.Files)
				}
			} else if !reflect.DeepEqual(envelope.Intent.Files, []string{tt.wantFile}) {
				t.Fatalf("files = %#v, want %q", envelope.Intent.Files, tt.wantFile)
			}
		})
	}
}

func TestHookPromptSubmitDoesNotClaimRiskFromCachedGraph(t *testing.T) {
	root := t.TempDir()
	writeWatchState(t, root, watch.State{
		UpdatedAt: time.Now(),
		Importers: map[string][]string{
			"pkg/types.go": {"a.go", "b.go", "c.go"},
		},
	})

	withStdinInput(t, mustJSONInput(t, map[string]string{
		"prompt": "refactor pkg/types.go",
	}), func() {
		out := captureOutput(func() {
			if err := hookPromptSubmit(root); err != nil {
				t.Fatalf("hookPromptSubmit() error: %v", err)
			}
		})
		if !strings.Contains(out, `"risk":"unknown"`) {
			t.Fatalf("missing unknown-risk marker:\n%s", out)
		}
		if !strings.Contains(out, "pkg/types.go is a HUB") {
			t.Fatalf("cached display context should remain available:\n%s", out)
		}
	})

	status, err := os.ReadFile(filepath.Join(root, ".codemap", "status"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(status), " unknown") {
		t.Fatalf("status = %q, want unknown risk", status)
	}
}

func TestContextHTTPHandlerPropagatesRequestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest("GET", "/api/context?intent=inspect&compact=true", nil).WithContext(ctx)
	response := httptest.NewRecorder()

	var observed error
	handler := newContextHandler(t.TempDir(), func(ctx context.Context, _ string, _ string, _ bool) ContextEnvelope {
		observed = ctx.Err()
		return ContextEnvelope{Version: 2}
	})
	handler.ServeHTTP(response, request)

	if !errors.Is(observed, context.Canceled) {
		t.Fatalf("builder context error = %v, want canceled", observed)
	}
}

func TestContextRequestsKeepInventoryLocal(t *testing.T) {
	type result struct {
		count int
		langs []string
	}
	results := make(chan result, 2)
	requests := []contextEnvelopeDeps{
		testContextEnvelopeDeps([]scanner.FileInfo{{Path: "main.go"}}, &scanner.FileGraph{Imports: map[string][]string{"main.go": {"dep.go"}}}),
		testContextEnvelopeDeps([]scanner.FileInfo{{Path: "src/lib.rs"}, {Path: "src/main.rs"}}, &scanner.FileGraph{Imports: map[string][]string{"src/main.rs": {"src/lib.rs"}}}),
	}

	for _, deps := range requests {
		deps := deps
		go func() {
			envelope := buildContextEnvelopeWithDeps(context.Background(), t.TempDir(), "inspect", true, deps)
			results <- result{count: envelope.Project.FileCount, langs: envelope.Project.Languages}
		}()
	}

	got := []result{<-results, <-results}
	if got[0].count > got[1].count {
		got[0], got[1] = got[1], got[0]
	}
	if got[0].count != 1 || !reflect.DeepEqual(got[0].langs, []string{"go"}) {
		t.Fatalf("one-file request = %#v", got[0])
	}
	if got[1].count != 2 || !reflect.DeepEqual(got[1].langs, []string{"rust"}) {
		t.Fatalf("two-file request = %#v", got[1])
	}
}

func TestContextWorkingSetRequiresFreshGraphEvidence(t *testing.T) {
	cached := watch.NewWorkingSet()
	cached.Touch("pkg/types.go", 3, true, 9)
	foreignState := &watch.State{WorkingSet: cached}

	t.Run("foreign setup-root state cannot leak hub claims", func(t *testing.T) {
		deps := testContextEnvelopeDeps([]scanner.FileInfo{{Path: "pkg/types.go"}}, nil)
		deps.readState = func(string) *watch.State { return foreignState }
		envelope := buildContextEnvelopeWithDeps(context.Background(), t.TempDir(), "inspect", true, deps)
		if envelope.Project.GraphEvidence.Status != graphEvidenceUnavailable {
			t.Fatalf("evidence = %#v", envelope.Project.GraphEvidence)
		}
		if envelope.WorkingSet != nil {
			t.Fatalf("working set leaked stale structural claims: %#v", envelope.WorkingSet)
		}
	})

	t.Run("fresh graph recomputes cached labels", func(t *testing.T) {
		graph := &scanner.FileGraph{
			Imports:   map[string][]string{"pkg/types.go": {"dep.go"}},
			Importers: map[string][]string{"pkg/types.go": {}},
		}
		deps := testContextEnvelopeDeps([]scanner.FileInfo{{Path: "pkg/types.go"}}, graph)
		deps.readState = func(string) *watch.State { return foreignState }
		envelope := buildContextEnvelopeWithDeps(context.Background(), t.TempDir(), "inspect", true, deps)
		if envelope.WorkingSet == nil || envelope.WorkingSet.HubCount != 0 {
			t.Fatalf("working set = %#v, want fresh zero hubs", envelope.WorkingSet)
		}
		if len(envelope.WorkingSet.TopFiles) != 1 || envelope.WorkingSet.TopFiles[0].IsHub {
			t.Fatalf("working files retained cached hub label: %#v", envelope.WorkingSet.TopFiles)
		}
	})
}

func testContextEnvelopeDeps(files []scanner.FileInfo, graph *scanner.FileGraph) contextEnvelopeDeps {
	return contextEnvelopeDeps{
		readState: func(string) *watch.State { return nil },
		scanConfiguredFiles: func(context.Context, string) ([]scanner.FileInfo, error) {
			return append([]scanner.FileInfo(nil), files...), nil
		},
		buildFileGraph: func(context.Context, string) (*scanner.FileGraph, error) {
			return graph, nil
		},
		now:          func() time.Time { return time.Unix(1, 0).UTC() },
		graphTimeout: time.Second,
	}
}
