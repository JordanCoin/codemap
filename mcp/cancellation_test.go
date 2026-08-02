package codemapmcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"codemap/handoff"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type cancelAfterMCPChecks struct {
	context.Context
	remaining int
}

type scriptedProjectDirReader struct {
	entries []os.DirEntry
	offset  int
}

func (r *scriptedProjectDirReader) ReadDir(n int) ([]os.DirEntry, error) {
	if r.offset >= len(r.entries) {
		return nil, io.EOF
	}
	end := min(r.offset+n, len(r.entries))
	batch := r.entries[r.offset:end]
	r.offset = end
	return batch, nil
}

type namedProjectDirEntry string

func (e namedProjectDirEntry) Name() string               { return string(e) }
func (e namedProjectDirEntry) IsDir() bool                { return true }
func (e namedProjectDirEntry) Type() fs.FileMode          { return fs.ModeDir }
func (e namedProjectDirEntry) Info() (fs.FileInfo, error) { return nil, errors.New("unused") }

func (c *cancelAfterMCPChecks) Err() error {
	if c.remaining <= 0 {
		return context.Canceled
	}
	c.remaining--
	return nil
}

func TestMCPTraversalHandlersReportPreCancellation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		call func(context.Context) (bool, string, error)
	}{
		{"structure", func(ctx context.Context) (bool, string, error) {
			r, _, e := handleGetStructure(ctx, nil, StructureInput{Path: root})
			return resultState(t, r, e)
		}},
		{"dependencies", func(ctx context.Context) (bool, string, error) {
			r, _, e := handleGetDependencies(ctx, nil, PathInput{Path: root})
			return resultState(t, r, e)
		}},
		{"diff", func(ctx context.Context) (bool, string, error) {
			r, _, e := handleGetDiff(ctx, nil, DiffInput{Path: root, Ref: "HEAD"})
			return resultState(t, r, e)
		}},
		{"find", func(ctx context.Context) (bool, string, error) {
			r, _, e := handleFindFile(ctx, nil, FindInput{Path: root, Pattern: "main"})
			return resultState(t, r, e)
		}},
		{"importers", func(ctx context.Context) (bool, string, error) {
			r, _, e := handleGetImporters(ctx, nil, ImportersInput{Path: root, File: "main.go"})
			return resultState(t, r, e)
		}},
		{"hubs", func(ctx context.Context) (bool, string, error) {
			r, _, e := handleGetHubs(ctx, nil, PathInput{Path: root})
			return resultState(t, r, e)
		}},
		{"file context", func(ctx context.Context) (bool, string, error) {
			r, _, e := handleGetFileContext(ctx, nil, ImportersInput{Path: root, File: "main.go"})
			return resultState(t, r, e)
		}},
		{"handoff", func(ctx context.Context) (bool, string, error) {
			r, _, e := handleGetHandoff(ctx, nil, HandoffInput{Path: root})
			return resultState(t, r, e)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			isError, text, err := tt.call(ctx)
			if err != nil {
				t.Fatalf("handler returned transport error instead of explicit result: %v", err)
			}
			if !isError || !strings.Contains(strings.ToLower(text), "cancel") {
				t.Fatalf("cancelled handler result: IsError=%v text=%q", isError, text)
			}
		})
	}
}

func TestMCPTraversalHandlersReportInFlightCancellation(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 20; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("file-%02d.go", i)), []byte("package fixture\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name string
		call func(context.Context) (bool, string, error)
	}{
		{"structure", func(ctx context.Context) (bool, string, error) {
			r, _, e := handleGetStructure(ctx, nil, StructureInput{Path: root})
			return resultState(t, r, e)
		}},
		{"dependencies", func(ctx context.Context) (bool, string, error) {
			r, _, e := handleGetDependencies(ctx, nil, PathInput{Path: root})
			return resultState(t, r, e)
		}},
		{"diff", func(ctx context.Context) (bool, string, error) {
			r, _, e := handleGetDiff(ctx, nil, DiffInput{Path: root, Ref: "HEAD"})
			return resultState(t, r, e)
		}},
		{"find", func(ctx context.Context) (bool, string, error) {
			r, _, e := handleFindFile(ctx, nil, FindInput{Path: root, Pattern: "file"})
			return resultState(t, r, e)
		}},
		{"importers", func(ctx context.Context) (bool, string, error) {
			r, _, e := handleGetImporters(ctx, nil, ImportersInput{Path: root, File: "file-00.go"})
			return resultState(t, r, e)
		}},
		{"hubs", func(ctx context.Context) (bool, string, error) {
			r, _, e := handleGetHubs(ctx, nil, PathInput{Path: root})
			return resultState(t, r, e)
		}},
		{"file context", func(ctx context.Context) (bool, string, error) {
			r, _, e := handleGetFileContext(ctx, nil, ImportersInput{Path: root, File: "file-00.go"})
			return resultState(t, r, e)
		}},
		{"handoff", func(ctx context.Context) (bool, string, error) {
			r, _, e := handleGetHandoff(ctx, nil, HandoffInput{Path: root})
			return resultState(t, r, e)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &cancelAfterMCPChecks{Context: context.Background(), remaining: 1}
			isError, text, err := tt.call(ctx)
			if err != nil {
				t.Fatalf("handler returned transport error instead of explicit result: %v", err)
			}
			if !isError || !strings.Contains(strings.ToLower(text), "cancel") {
				t.Fatalf("cancelled handler result: IsError=%v text=%q", isError, text)
			}
		})
	}
}

func resultState(t *testing.T, result interface{}, err error) (bool, string, error) {
	t.Helper()
	if err != nil {
		return false, "", err
	}
	if result == nil {
		return false, "", errors.New("nil MCP result")
	}
	r, ok := result.(*mcp.CallToolResult)
	if !ok {
		return false, "", fmt.Errorf("unexpected MCP result type %T", result)
	}
	return r.IsError, resultText(t, r), nil
}

func TestListProjectsUsesSeparateExaminedAndResultCaps(t *testing.T) {
	t.Run("exact result cap is complete", func(t *testing.T) {
		parent := t.TempDir()
		createProjectDirs(t, parent, 50, "project")
		result, _, err := handleListProjects(context.Background(), nil, ListProjectsInput{Path: parent})
		if err != nil {
			t.Fatal(err)
		}
		out := resultText(t, result)
		if result.IsError || strings.Contains(out, "truncated") || strings.Count(out, "project-") != 50 {
			t.Fatalf("exact-cap result: IsError=%v count=%d output=%q", result.IsError, strings.Count(out, "project-"), out)
		}
	})

	t.Run("over result cap truncates", func(t *testing.T) {
		parent := t.TempDir()
		createProjectDirs(t, parent, 51, "project")
		result, _, err := handleListProjects(context.Background(), nil, ListProjectsInput{Path: parent})
		if err != nil {
			t.Fatal(err)
		}
		out := resultText(t, result)
		if result.IsError || strings.Count(out, "project-") != 50 || !strings.Contains(out, "truncated") {
			t.Fatalf("over-cap result: IsError=%v count=%d output=%q", result.IsError, strings.Count(out, "project-"), out)
		}
	})

	t.Run("examined cap bounds nonmatches", func(t *testing.T) {
		parent := t.TempDir()
		createProjectDirs(t, parent, 240, "nonmatch")
		result, _, err := handleListProjects(context.Background(), nil, ListProjectsInput{Path: parent, Pattern: "wanted"})
		if err != nil {
			t.Fatal(err)
		}
		out := resultText(t, result)
		want := fmt.Sprintf("Project discovery matching 'wanted' in %s was truncated after examining 200 entries; results are unavailable because the directory exceeds the examination cap.", parent)
		if result.IsError || out != want {
			t.Fatalf("patterned examined-cap result: IsError=%v output=%q, want %q", result.IsError, out, want)
		}
		if strings.Contains(out, "No projects matching") {
			t.Fatalf("patterned examined-cap result made a false exhaustive claim: %q", out)
		}
	})

	t.Run("examined cap does not claim parent has no projects", func(t *testing.T) {
		parent := t.TempDir()
		createProjectDirs(t, parent, 201, "project")
		result, _, err := handleListProjects(context.Background(), nil, ListProjectsInput{Path: parent})
		if err != nil {
			t.Fatal(err)
		}
		out := resultText(t, result)
		want := fmt.Sprintf("Project discovery in %s was truncated after examining 200 entries; results are unavailable because the directory exceeds the examination cap.", parent)
		if result.IsError || out != want {
			t.Fatalf("unpatterned examined-cap result: IsError=%v output=%q, want %q", result.IsError, out, want)
		}
		if strings.Contains(out, "No project directories found") {
			t.Fatalf("unpatterned examined-cap result made a false exhaustive claim: %q", out)
		}
	})
}

func TestSelectProjectCandidatesSortsAcrossBatchesAndCapsDeterministically(t *testing.T) {
	makeEntries := func(count int) []os.DirEntry {
		entries := make([]os.DirEntry, 0, count)
		for i := count - 1; i >= 0; i-- {
			entries = append(entries, namedProjectDirEntry(fmt.Sprintf("project-%03d", i)))
		}
		return entries
	}

	tests := []struct {
		name               string
		count              int
		wantCount          int
		wantExamined       int
		wantResultCap      bool
		wantExaminationCap bool
	}{
		{name: "exact result cap", count: 50, wantCount: 50, wantExamined: 50},
		{name: "over result cap", count: 51, wantCount: 50, wantExamined: 51, wantResultCap: true},
		{name: "over examination cap", count: 201, wantCount: 0, wantExamined: 200, wantExaminationCap: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &scriptedProjectDirReader{entries: makeEntries(tt.count)}
			candidates, examined, byResults, byExamined, err := selectProjectCandidates(context.Background(), reader, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(candidates) != tt.wantCount || examined != tt.wantExamined || byResults != tt.wantResultCap || byExamined != tt.wantExaminationCap {
				t.Fatalf("selection = count %d, examined %d, result cap %v, examination cap %v", len(candidates), examined, byResults, byExamined)
			}
			for i, candidate := range candidates {
				want := fmt.Sprintf("project-%03d", tt.count-tt.wantExamined+i)
				if candidate != want {
					t.Fatalf("candidate[%d] = %q, want %q; selection was not sorted across batches", i, candidate, want)
				}
			}
		})
	}
}

func TestSelectProjectCandidatesAboveExamCapIsIndependentOfEnumerationOrder(t *testing.T) {
	forward := make([]os.DirEntry, 0, maxExaminedProjectEntries+1)
	reverse := make([]os.DirEntry, 0, maxExaminedProjectEntries+1)
	for i := 0; i <= maxExaminedProjectEntries; i++ {
		forward = append(forward, namedProjectDirEntry(fmt.Sprintf("project-%03d", i)))
	}
	for i := maxExaminedProjectEntries; i >= 0; i-- {
		reverse = append(reverse, namedProjectDirEntry(fmt.Sprintf("project-%03d", i)))
	}

	for name, entries := range map[string][]os.DirEntry{"forward": forward, "reverse": reverse} {
		t.Run(name, func(t *testing.T) {
			candidates, examined, byResults, byExamined, err := selectProjectCandidates(context.Background(), &scriptedProjectDirReader{entries: entries}, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(candidates) != 0 || examined != maxExaminedProjectEntries || byResults || !byExamined {
				t.Fatalf("oversized selection leaked filesystem order: candidates=%v examined=%d result cap=%v examination cap=%v", candidates, examined, byResults, byExamined)
			}
		})
	}
}

func TestListProjectsCancellationIsNotTruncation(t *testing.T) {
	parent := t.TempDir()
	createProjectDirs(t, parent, 80, "project")
	ctx := &cancelAfterMCPChecks{Context: context.Background(), remaining: 1}
	result, _, err := handleListProjects(ctx, nil, ListProjectsInput{Path: parent})
	if err != nil {
		t.Fatal(err)
	}
	out := resultText(t, result)
	if !result.IsError || !strings.Contains(strings.ToLower(out), "cancel") || strings.Contains(strings.ToLower(out), "truncated") {
		t.Fatalf("cancelled discovery result: IsError=%v output=%q", result.IsError, out)
	}
}

func TestListProjectsPreservesPerChildScanErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission fixture is Unix-specific")
	}
	parent := t.TempDir()
	project := filepath.Join(parent, "restricted")
	blocked := filepath.Join(project, "blocked")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })

	result, _, err := handleListProjects(context.Background(), nil, ListProjectsInput{Path: parent})
	if err != nil {
		t.Fatal(err)
	}
	out := resultText(t, result)
	if result.IsError || !strings.Contains(out, "restricted/") || !strings.Contains(out, "(error scanning)") {
		t.Fatalf("per-child scan failure was not preserved: IsError=%v output=%q", result.IsError, out)
	}
}

func TestTraversalRootsFailClosed(t *testing.T) {
	notDir := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(notDir, []byte("not a project"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		call func() (bool, string, error)
	}{
		{"structure", func() (bool, string, error) {
			r, _, e := handleGetStructure(context.Background(), nil, StructureInput{Path: notDir})
			return resultState(t, r, e)
		}},
		{"dependencies", func() (bool, string, error) {
			r, _, e := handleGetDependencies(context.Background(), nil, PathInput{Path: notDir})
			return resultState(t, r, e)
		}},
		{"diff", func() (bool, string, error) {
			r, _, e := handleGetDiff(context.Background(), nil, DiffInput{Path: notDir, Ref: "HEAD"})
			return resultState(t, r, e)
		}},
		{"find", func() (bool, string, error) {
			r, _, e := handleFindFile(context.Background(), nil, FindInput{Path: notDir, Pattern: "x"})
			return resultState(t, r, e)
		}},
		{"importers", func() (bool, string, error) {
			r, _, e := handleGetImporters(context.Background(), nil, ImportersInput{Path: notDir, File: "x.go"})
			return resultState(t, r, e)
		}},
		{"hubs", func() (bool, string, error) {
			r, _, e := handleGetHubs(context.Background(), nil, PathInput{Path: notDir})
			return resultState(t, r, e)
		}},
		{"file context", func() (bool, string, error) {
			r, _, e := handleGetFileContext(context.Background(), nil, ImportersInput{Path: notDir, File: "x.go"})
			return resultState(t, r, e)
		}},
		{"handoff", func() (bool, string, error) {
			r, _, e := handleGetHandoff(context.Background(), nil, HandoffInput{Path: notDir})
			return resultState(t, r, e)
		}},
		{"list projects", func() (bool, string, error) {
			r, _, e := handleListProjects(context.Background(), nil, ListProjectsInput{Path: notDir})
			return resultState(t, r, e)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isError, text, err := tt.call()
			if err != nil {
				t.Fatal(err)
			}
			if !isError || text == "" {
				t.Fatalf("invalid explicit root did not fail closed: IsError=%v text=%q", isError, text)
			}
		})
	}

	result, _, err := handleListProjects(context.Background(), nil, ListProjectsInput{Path: string([]byte{'b', 'a', 'd', 0, 'p', 'a', 't', 'h'})})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("malformed root did not fail closed: %q", resultText(t, result))
	}
}

func TestHandoffCancellationBeforePersistenceCommitPoint(t *testing.T) {
	oldBuild := buildHandoffForMCP
	oldWrite := writeLatestForMCP
	t.Cleanup(func() {
		buildHandoffForMCP = oldBuild
		writeLatestForMCP = oldWrite
	})

	ctx, cancel := context.WithCancel(context.Background())
	buildHandoffForMCP = func(context.Context, string, handoff.BuildOptions) (*handoff.Artifact, error) {
		cancel()
		return &handoff.Artifact{}, nil
	}
	writeCalled := false
	writeLatestForMCP = func(string, *handoff.Artifact) error {
		writeCalled = true
		return nil
	}

	result, _, err := handleGetHandoff(ctx, nil, HandoffInput{Path: t.TempDir(), Save: true})
	if err != nil {
		t.Fatal(err)
	}
	if writeCalled {
		t.Fatal("WriteLatest began after cancellation was observed before persistence commit point")
	}
	if !result.IsError || !strings.Contains(strings.ToLower(resultText(t, result)), "cancel") {
		t.Fatalf("pre-persistence cancellation result: IsError=%v text=%q", result.IsError, resultText(t, result))
	}
}

func TestGetDiffPreservesBestEffortUnavailableImpactScan(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake Git script is Unix-specific")
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	fakeGit := filepath.Join(binDir, "git")
	script := `#!/bin/sh
if [ "$1" = "diff" ] && [ "$2" = "--numstat" ]; then
  printf '1\t0\tmain.go\n'
fi
exit 0
`
	if err := os.WriteFile(fakeGit, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// Keep Git available while making ast-grep unavailable.
	t.Setenv("PATH", binDir)

	result, _, err := handleGetDiff(context.Background(), nil, DiffInput{Path: root, Ref: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || !strings.Contains(resultText(t, result), "main.go") {
		t.Fatalf("ordinary impact scan failure escaped get_diff: IsError=%v output=%q", result.IsError, resultText(t, result))
	}
}

func createProjectDirs(t *testing.T, parent string, count int, prefix string) {
	t.Helper()
	for i := 0; i < count; i++ {
		root := filepath.Join(parent, fmt.Sprintf("%s-%03d", prefix, i))
		if err := os.Mkdir(root, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
