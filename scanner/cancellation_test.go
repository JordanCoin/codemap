package scanner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type cancelAfterScannerChecks struct {
	context.Context
	remaining int
}

func (c *cancelAfterScannerChecks) Err() error {
	if c.remaining <= 0 {
		return context.Canceled
	}
	c.remaining--
	return nil
}

func TestScanContextCancellation(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 40; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("file-%02d.go", i)), []byte("package fixture\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ScanFiles(cancelled, root, NewGitIgnoreCache(root), nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled ScanFiles error = %v, want context.Canceled", err)
	}

	inFlight := &cancelAfterScannerChecks{Context: context.Background(), remaining: 8}
	if _, err := ScanConfiguredFiles(inFlight, root, NewGitIgnoreCache(root)); !errors.Is(err, context.Canceled) {
		t.Fatalf("in-flight ScanConfiguredFiles error = %v, want context.Canceled", err)
	}
}

func TestDependencyAndGraphContextCancellation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	analyses := make([]FileAnalysis, 100)
	for i := range analyses {
		analyses[i] = FileAnalysis{Path: "main.go", Language: "go", Imports: []string{"example.com/dependency"}}
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ScanForDeps(cancelled, root, ConfiguredFilters(root)); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled ScanForDeps error = %v, want context.Canceled", err)
	}
	if _, err := ScanForDeps(cancelled, root, Filters{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled ScanForDeps error = %v, want context.Canceled", err)
	}
	if _, err := BuildFileGraph(cancelled, root, ConfiguredFilters(root)); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled BuildFileGraph error = %v, want context.Canceled", err)
	}
	if _, err := BuildFileGraphFromAnalyses(cancelled, root, analyses, ConfiguredFilters(root)); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled BuildFileGraphFromAnalyses error = %v, want context.Canceled", err)
	}

	inFlight := &cancelAfterScannerChecks{Context: context.Background(), remaining: 12}
	if _, err := BuildFileGraphFromAnalyses(inFlight, root, analyses, ConfiguredFilters(root)); !errors.Is(err, context.Canceled) {
		t.Fatalf("in-flight graph build error = %v, want context.Canceled", err)
	}
}

func TestAstGrepCallerContextOutcomes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires shell script execution")
	}
	root := t.TempDir()
	fakeBinary := filepath.Join(root, "fake-sg.sh")
	marker := filepath.Join(t.TempDir(), "started")
	script := `#!/bin/sh
printf '%s\n' "$$" > "$CODEMAP_AST_GREP_TEST_MARKER"
exec sleep 10
`
	if err := os.WriteFile(fakeBinary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMAP_AST_GREP_TEST_MARKER", marker)
	s := &AstGrepScanner{rulesDir: root, binary: fakeBinary}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.ScanDirectory(cancelled, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled ScanDirectory error = %v, want context.Canceled", err)
	}

	const scanDeadline = 5 * time.Second
	deadline, stop := context.WithTimeout(context.Background(), scanDeadline)
	defer stop()
	started := time.Now()
	done := make(chan error, 1)
	go func() {
		_, err := s.ScanDirectory(deadline, root)
		done <- err
	}()
	var blockerPID string
	startupDeadline := time.Now().Add(5 * time.Second)
	for {
		if data, err := os.ReadFile(marker); err == nil && len(data) > 0 {
			blockerPID = strings.TrimSpace(string(data))
			break
		}
		select {
		case err := <-done:
			t.Fatalf("ScanDirectory exited before fake ast-grep subprocess started: %v", err)
		default:
		}
		if time.Now().After(startupDeadline) {
			t.Fatal("fake ast-grep subprocess did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Anchor the termination budget to the deadline: it runs concurrently
	// with the deadline, so a bare budget leaves only the spawn latency
	// of slack after the deadline fires.
	const terminateBudget = 5 * time.Second
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("caller-deadline ScanDirectory error = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(scanDeadline + terminateBudget):
		t.Fatal("ScanDirectory did not terminate deadline-exceeded subprocess")
	}
	if elapsed := time.Since(started); elapsed > scanDeadline+terminateBudget {
		t.Fatalf("caller deadline did not terminate ast-grep promptly: %s", elapsed)
	}
	if err := exec.Command("/bin/kill", "-0", blockerPID).Run(); err == nil {
		t.Fatalf("deadline-exceeded fake ast-grep left blocker process %s running", blockerPID)
	}
}

func TestGitDiffInfoContextTerminatesSubprocess(t *testing.T) {
	cancelled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if _, err := GitDiffInfo(cancelled, t.TempDir(), "main"); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled GitDiffInfo error = %v, want context.Canceled", err)
	}

	if runtime.GOOS == "windows" {
		t.Skip("requires shell script execution")
	}
	binDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "started")
	fakeGit := filepath.Join(binDir, "git")
	script := `#!/bin/sh
printf '%s\n' "$$" > "$CODEMAP_GIT_TEST_MARKER"
exec sleep 10
`
	if err := os.WriteFile(fakeGit, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMAP_GIT_TEST_MARKER", marker)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	gitRoot := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := GitDiffInfo(ctx, gitRoot, "main")
		done <- err
	}()
	var blockerPID string
	deadline := time.Now().Add(5 * time.Second)
	for {
		if data, err := os.ReadFile(marker); err == nil && len(data) > 0 {
			blockerPID = strings.TrimSpace(string(data))
			break
		}
		select {
		case err := <-done:
			t.Fatalf("GitDiffInfo exited before fake git subprocess started: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("fake git subprocess did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("GitDiffInfo error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GitDiffInfo did not terminate cancelled subprocess")
	}
	if err := exec.Command("/bin/kill", "-0", blockerPID).Run(); err == nil {
		t.Fatalf("cancelled fake git left blocker process %s running", blockerPID)
	}
}

func TestAnalyzeImpactContextPreCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := AnalyzeImpact(ctx, t.TempDir(), []FileInfo{{Path: "changed.go"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("AnalyzeImpact error = %v, want context.Canceled", err)
	}
}

func TestReadExternalDepsContextBudgetAndCompatibility(t *testing.T) {
	root := t.TempDir()
	large := strings.Repeat("x", int(MCPManifestByteBudget)+1)
	largeManifest := "require (\nexample.com/large v1.0.0\n)\n" + large
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(largeManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	smallDir := filepath.Join(root, "nested")
	if err := os.Mkdir(smallDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(smallDir, "requirements.txt"), []byte("small-dependency==1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	deps, err := ReadExternalDeps(context.Background(), root, MCPManifestByteBudget)
	if err != nil {
		t.Fatalf("ReadExternalDeps error: %v", err)
	}
	if got := deps["go"]; len(got) != 0 {
		t.Fatalf("bounded MCP read parsed oversized go.mod: %v", got)
	}
	if got := deps["python"]; len(got) != 1 || got[0] != "small-dependency" {
		t.Fatalf("bounded MCP read lost valid small manifest: %v", got)
	}
	legacy, err := ReadExternalDeps(context.Background(), root, 0)
	if err != nil {
		t.Fatalf("unbounded ReadExternalDeps error: %v", err)
	}
	if got := legacy["go"]; len(got) != 1 || got[0] != "example.com/large" {
		t.Fatalf("unbounded read no longer reads oversized manifest: %v", got)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadExternalDeps(cancelled, root, MCPManifestByteBudget); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled dependency read error = %v, want context.Canceled", err)
	}
	inFlight := &cancelAfterScannerChecks{Context: context.Background(), remaining: 2}
	if _, err := ReadExternalDeps(inFlight, root, MCPManifestByteBudget); !errors.Is(err, context.Canceled) {
		t.Fatalf("in-flight dependency read error = %v, want context.Canceled", err)
	}
}
