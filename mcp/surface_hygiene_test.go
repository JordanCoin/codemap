package codemapmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"codemap/handoff"
	"codemap/limits"
	"codemap/watch"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTextResultContentMatrix(t *testing.T) {
	t.Run("ordinary text is UTF-8 bounded", func(t *testing.T) {
		result := textResult(strings.Repeat("界", limits.MaxContextOutputBytes))
		text := resultText(t, result)
		assertSingleTextContent(t, result)
		if len(text) > limits.MaxContextOutputBytes {
			t.Fatalf("ordinary text length = %d, want <= %d", len(text), limits.MaxContextOutputBytes)
		}
		if !utf8.ValidString(text) {
			t.Fatal("ordinary text truncation split a UTF-8 encoding")
		}
		if !strings.Contains(text, "truncated") {
			t.Fatal("ordinary text lacks a truncation marker")
		}
	})

	t.Run("error stays explicit text", func(t *testing.T) {
		result := errorResult("boom")
		assertSingleTextContent(t, result)
		if !result.IsError || resultText(t, result) != "boom" {
			t.Fatalf("error result = %#v", result)
		}
	})

	t.Run("complete JSON stays complete", func(t *testing.T) {
		payload := map[string]string{"value": strings.Repeat("界", limits.MaxContextOutputBytes)}
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		result := completeJSONResult(string(encoded))
		assertSingleTextContent(t, result)
		if got := resultText(t, result); got != string(encoded) || !json.Valid([]byte(got)) {
			t.Fatalf("complete JSON result was altered or made invalid: length=%d", len(got))
		}
	})
}

func TestTextResultCallerClassification(t *testing.T) {
	type calls struct{ ordinary, completeJSON int }
	want := map[string]calls{
		"handleGetStructure":    {1, 0},
		"handleGetDependencies": {1, 0},
		"handleGetDiff":         {2, 0},
		"handleFindFile":        {3, 0},
		"handleStatus":          {1, 0},
		"handleListProjects":    {4, 0},
		"handleGetImporters":    {2, 0},
		"handleGetHandoff":      {3, 2},
		"handleStartWatch":      {2, 0},
		"handleStopWatch":       {2, 0},
		"handleGetActivity":     {2, 0},
		"handleGetHubs":         {2, 0},
		"handleGetFileContext":  {1, 0},
		"handleGetWorkingSet":   {2, 0},
		"handleListSkills":      {2, 0},
		"handleGetSkill":        {1, 0},
	}

	parsed, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]calls)
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			callee, ok := call.Fun.(*ast.Ident)
			if !ok {
				return true
			}
			counts := got[function.Name.Name]
			switch callee.Name {
			case "textResult":
				counts.ordinary++
			case "completeJSONResult":
				counts.completeJSON++
			default:
				return true
			}
			got[function.Name.Name] = counts
			return true
		})
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("text response caller classification = %#v, want %#v", got, want)
	}
}

func TestStatusGuidanceIsBoundedAfterFinalComposition(t *testing.T) {
	guidance := "Upgrade guidance: " + strings.Repeat("界", limits.MaxContextOutputBytes)
	result, _, err := statusHandler(guidance)(context.Background(), nil, EmptyInput{})
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, result)
	if len(text) > limits.MaxContextOutputBytes {
		t.Fatalf("status with guidance length = %d, want <= %d", len(text), limits.MaxContextOutputBytes)
	}
	if !utf8.ValidString(text) {
		t.Fatal("status guidance truncation split a UTF-8 encoding")
	}
	if !strings.Contains(text, "truncated") {
		t.Fatal("status with oversized final guidance lacks a truncation marker")
	}
}

func TestOverBudgetHandoffJSONRemainsCompleteAndStructuredContentStaysBounded(t *testing.T) {
	root := t.TempDir()
	changed := make([]handoff.FileStub, maxStructuredItems+3)
	for index := range changed {
		changed[index].Path = fmt.Sprintf("file-%03d-%s.go", index, strings.Repeat("x", 256))
	}
	artifact := &handoff.Artifact{
		SchemaVersion: handoff.SchemaVersion,
		Root:          root,
		Delta:         handoff.DeltaSnapshot{Changed: changed},
	}
	if err := handoff.WriteLatest(root, artifact); err != nil {
		t.Fatal(err)
	}

	result, structured, err := handleGetHandoff(context.Background(), nil, HandoffInput{Path: root, Latest: true, JSON: true})
	if err != nil {
		t.Fatal(err)
	}
	assertSingleTextContent(t, result)
	text := resultText(t, result)
	if len(text) <= limits.MaxContextOutputBytes {
		t.Fatalf("fixture JSON length = %d, want over ordinary-text budget %d", len(text), limits.MaxContextOutputBytes)
	}
	var decoded handoff.Artifact
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		t.Fatalf("legacy JSON is incomplete or malformed: %v", err)
	}
	if len(decoded.Delta.Changed) != len(changed) || decoded.Delta.Changed[len(changed)-1].Path != changed[len(changed)-1].Path {
		t.Fatalf("legacy JSON changed files = %d, want complete %d-item payload", len(decoded.Delta.Changed), len(changed))
	}
	output, ok := structured.(HandoffOutput)
	if !ok {
		t.Fatalf("structured output type = %T, want HandoffOutput", structured)
	}
	if len(output.Artifact.Delta.Changed) != maxStructuredItems || !output.Truncated || output.OmittedCount != 3 {
		t.Fatalf("structured output did not retain its independent bound: %#v", output)
	}
}

func TestStatusInventoryExactlyMatchesRegisteredTools(t *testing.T) {
	session := connectParitySession(t)
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	registered := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		registered = append(registered, tool.Name)
	}
	sort.Strings(registered)

	expected := []string{
		"find_file",
		"get_activity",
		"get_dependencies",
		"get_diff",
		"get_file_context",
		"get_handoff",
		"get_hubs",
		"get_importers",
		"get_skill",
		"get_structure",
		"get_working_set",
		"list_projects",
		"list_skills",
		"start_watch",
		"status",
		"stop_watch",
	}
	if !reflect.DeepEqual(registered, expected) {
		t.Fatalf("registered inventory = %v, want %v", registered, expected)
	}

	// The status tool no longer embeds the tool inventory: clients enumerate
	// tools via the protocol's tools/list (the SDK serves it from the live
	// registry), so the exact-surface contract is the registered set above.
}

func TestFileContextUsesNativeKeysAndKeepsProjectRootsRequestLocal(t *testing.T) {
	alpha := writeFileContextProject(t, "example.com/alpha", "alpha/alpha.go")
	beta := writeFileContextProject(t, "example.com/beta", "beta/beta.go")

	alphaResult, _, err := handleGetFileContext(context.Background(), nil, ImportersInput{
		Path: alpha,
		File: filepath.Join(alpha, "pkg", "types", "types.go"),
	})
	if err != nil {
		t.Fatal(err)
	}
	alphaText := resultText(t, alphaResult)
	if !strings.Contains(alphaText, "alpha/alpha.go") || strings.Contains(alphaText, "beta/beta.go") || strings.Contains(alphaText, alpha) {
		t.Fatalf("absolute alpha context was not normalized and isolated:\n%s", alphaText)
	}

	betaResult, _, err := handleGetFileContext(context.Background(), nil, ImportersInput{
		Path: beta,
		File: "pkg/types/types.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	betaText := resultText(t, betaResult)
	if !strings.Contains(betaText, "beta/beta.go") || strings.Contains(betaText, "alpha/alpha.go") {
		t.Fatalf("beta context leaked another requested root:\n%s", betaText)
	}

	alphaAgain, _, err := handleGetFileContext(context.Background(), nil, ImportersInput{
		Path: alpha,
		File: filepath.ToSlash(filepath.Join("pkg", "types", "types.go")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := resultText(t, alphaAgain); got != alphaText {
		t.Fatalf("repeated alpha context changed after beta request:\nfirst:\n%s\nagain:\n%s", alphaText, got)
	}
}

func TestFindFileResolvesHomeAndKeepsConfigRootsRequestLocal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	alpha := filepath.Join(home, "alpha")
	beta := filepath.Join(home, "beta")
	writeFindProject(t, alpha, `{"only":["go"]}`)
	writeFindProject(t, beta, `{"only":["proto"]}`)

	alphaHome, _, err := handleFindFile(context.Background(), nil, FindInput{Path: "~/alpha", Pattern: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if alphaHome.IsError || !strings.Contains(resultText(t, alphaHome), "main.go") {
		t.Fatalf("home-relative find failed: error=%v output=%q", alphaHome.IsError, resultText(t, alphaHome))
	}

	alphaSchema, _, err := handleFindFile(context.Background(), nil, FindInput{Path: alpha, Pattern: "schema"})
	if err != nil {
		t.Fatal(err)
	}
	betaSchema, _, err := handleFindFile(context.Background(), nil, FindInput{Path: beta, Pattern: "schema"})
	if err != nil {
		t.Fatal(err)
	}
	alphaAgain, _, err := handleFindFile(context.Background(), nil, FindInput{Path: alpha, Pattern: "schema"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resultText(t, alphaSchema), "excluded by `only` config") {
		t.Fatalf("alpha config was not loaded from its requested root:\n%s", resultText(t, alphaSchema))
	}
	if !strings.Contains(resultText(t, betaSchema), "Found 1 files") || !strings.Contains(resultText(t, betaSchema), "schema.proto") {
		t.Fatalf("beta config was not loaded from its requested root:\n%s", resultText(t, betaSchema))
	}
	if resultText(t, alphaAgain) != resultText(t, alphaSchema) {
		t.Fatalf("alpha find changed after beta request:\nfirst:\n%s\nagain:\n%s", resultText(t, alphaSchema), resultText(t, alphaAgain))
	}
}

func TestStatusWatcherOrderingIsDeterministic(t *testing.T) {
	withWatcherRegistry(t)
	watchersMu.Lock()
	for _, path := range []string{"/tmp/zeta", "/tmp/gamma", "/tmp/alpha", "/tmp/omega", "/tmp/beta"} {
		watchers[path] = nil
	}
	watchersMu.Unlock()

	wantLine := "Active watchers: 5 active: /tmp/alpha, /tmp/beta, /tmp/gamma, /tmp/omega, /tmp/zeta"
	var previous string
	for attempt := 0; attempt < 32; attempt++ {
		result, _, err := handleStatus(context.Background(), nil, EmptyInput{})
		if err != nil {
			t.Fatal(err)
		}
		text := resultText(t, result)
		if !strings.Contains(text, wantLine) {
			t.Fatalf("watcher inventory is not canonically ordered:\n%s", text)
		}
		if previous != "" && text != previous {
			t.Fatalf("status changed between repeated calls:\nfirst:\n%s\nnext:\n%s", previous, text)
		}
		previous = text
	}
}

func TestActivityAndHubTieOrderingIsDeterministic(t *testing.T) {
	t.Run("activity", func(t *testing.T) {
		withWatcherRegistry(t)
		root := t.TempDir()
		daemon, err := watch.NewDaemon(root, false)
		if err != nil {
			t.Fatal(err)
		}
		watchersMu.Lock()
		watchers[root] = daemon
		watchersMu.Unlock()
		graph := daemon.GetGraph()
		for index, path := range []string{"z.go", "m.go", "a.go", "q.go", "b.go"} {
			graph.Events = append(graph.Events, watch.Event{Time: time.Now().Add(-time.Duration(index+1) * time.Minute), Op: "WRITE", Path: path, Delta: 1})
		}
		result, _, err := handleGetActivity(context.Background(), nil, WatchActivityInput{Path: root, Minutes: 60})
		if err != nil {
			t.Fatal(err)
		}
		assertPathsOrdered(t, resultText(t, result), []string{"a.go", "b.go", "m.go", "q.go", "z.go"})
	})

	t.Run("hubs and their importers", func(t *testing.T) {
		root := writeEqualHubProject(t)
		var previous string
		for attempt := 0; attempt < 16; attempt++ {
			result, _, err := handleGetHubs(context.Background(), nil, PathInput{Path: root})
			if err != nil {
				t.Fatal(err)
			}
			text := resultText(t, result)
			assertPathsOrdered(t, text, []string{"pkg/a/a.go", "pkg/z/z.go"})
			assertPathsOrdered(t, text, []string{"one/one.go", "three/three.go", "two/two.go"})
			if previous != "" && text != previous {
				t.Fatalf("hub output changed between repeated calls:\nfirst:\n%s\nnext:\n%s", previous, text)
			}
			previous = text
		}
	})

	t.Run("structure hubs tie-break alphabetically", func(t *testing.T) {
		root := writeEqualHubProject(t)
		result, _, err := handleGetStructure(context.Background(), nil, StructureInput{Path: root})
		if err != nil {
			t.Fatal(err)
		}
		text := resultText(t, result)
		// Equal-importer-count hubs must order alphabetically (tie-breaker).
		assertPathsOrdered(t, text, []string{"pkg/a/a.go", "pkg/z/z.go"})
	})
}

func assertSingleTextContent(t *testing.T, result *mcp.CallToolResult) {
	t.Helper()
	if result == nil || len(result.Content) != 1 {
		t.Fatalf("result content shape = %#v, want exactly one item", result)
	}
	if _, ok := result.Content[0].(*mcp.TextContent); !ok {
		t.Fatalf("result content type = %T, want *mcp.TextContent", result.Content[0])
	}
}

func writeFileContextProject(t *testing.T, module, importerPath string) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":             "module " + module + "\n\ngo 1.22\n",
		"pkg/types/types.go": "package types\n\ntype Item struct{}\n",
		importerPath:         "package caller\n\nimport _ \"" + module + "/pkg/types\"\n",
	}
	for path, content := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func writeFindProject(t *testing.T, root, configJSON string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		".codemap/config.json": configJSON,
		"main.go":              "package main\n",
		"schema.proto":         "syntax = \"proto3\";\n",
	} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func writeEqualHubProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":     "module example.com/hubs\n\ngo 1.22\n",
		"pkg/a/a.go": "package a\n",
		"pkg/z/z.go": "package z\n",
	}
	for _, caller := range []string{"one", "three", "two"} {
		files[filepath.Join(caller, caller+".go")] = "package " + caller + "\n\nimport (\n\t_ \"example.com/hubs/pkg/a\"\n\t_ \"example.com/hubs/pkg/z\"\n)\n"
	}
	for path, content := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func assertPathsOrdered(t *testing.T, text string, paths []string) {
	t.Helper()
	previous := -1
	for _, path := range paths {
		index := strings.Index(text, path)
		if index < 0 {
			t.Fatalf("output does not contain %q:\n%s", path, text)
		}
		if index <= previous {
			t.Fatalf("paths are not ordered as %v:\n%s", paths, text)
		}
		previous = index
	}
}

func TestTraversalRootRejectsInaccessiblePath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	root, result := traversalRoot(missing)
	if root != "" || result == nil || !result.IsError {
		t.Fatalf("traversalRoot(%q) = %q, %v; want error", missing, root, result)
	}
	if !strings.Contains(resultText(t, result), "not an accessible directory") {
		t.Fatalf("unexpected rejection: %q", resultText(t, result))
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "proj"), 0o755); err != nil {
		t.Fatal(err)
	}
	expanded, result := traversalRoot("~/proj")
	if result != nil || expanded != filepath.Join(home, "proj") {
		t.Fatalf("traversalRoot(~/) = %q, %v; want %q", expanded, result, filepath.Join(home, "proj"))
	}
}
