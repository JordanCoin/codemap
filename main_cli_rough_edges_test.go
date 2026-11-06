package main

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"codemap/topology"
)

// #289: the CLI routes that the MCP server already had, and the rough edges
// hit when running every command.

func runCodemapIn(t *testing.T, dir string, args ...string) (string, string, error) {
	t.Helper()
	command := exec.Command(codemapTestBinaryPath, args...)
	command.Dir = dir
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.String(), stderr.String(), err
}

func TestImportersFromSubdirectoryUsesEnclosingRepository(t *testing.T) {
	repo := makeMainGitRepo(t, "main")
	stdout, stderr, err := runCodemapIn(t, filepath.Join(repo, "pkg"), "--importers", "types/types.go")
	if err != nil {
		t.Fatalf("--importers from a subdirectory failed: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "pkg/types/types.go") || !strings.Contains(stdout, "HUB FILE") {
		t.Fatalf("importers must resolve against the repository root:\n%s", stdout)
	}
}

func TestMissingFlagValuePointsAtHelpInsteadOfDumpingUsage(t *testing.T) {
	_, stderr, err := runCodemapWithInput("", "--importers")
	if err == nil {
		t.Fatal("expected a usage error")
	}
	if !strings.Contains(stderr, "Run 'codemap --help'") || strings.Contains(stderr, "-animate") {
		t.Fatalf("flag error must be one hint, not the full flag dump:\n%s", stderr)
	}
}

func TestHubsAndFileFlagsMatchMCPOutput(t *testing.T) {
	repo := makeMainGitRepo(t, "main")
	hubs, stderr, err := runCodemapIn(t, repo, "--hubs")
	if err != nil {
		t.Fatalf("--hubs failed: %v\n%s", err, stderr)
	}
	if !strings.Contains(hubs, "=== Hub Files (1 total) ===") || !strings.Contains(hubs, "pkg/types/types.go (4 importers)") {
		t.Fatalf("--hubs output:\n%s", hubs)
	}
	file, stderr, err := runCodemapIn(t, repo, "--file", "pkg/types/types.go")
	if err != nil {
		t.Fatalf("--file failed: %v\n%s", err, stderr)
	}
	if !strings.Contains(file, "=== File Context: pkg/types/types.go ===") || !strings.Contains(file, "IMPORTED BY (4 files)") {
		t.Fatalf("--file output:\n%s", file)
	}
}

func TestStatusSubcommand(t *testing.T) {
	repo := makeMainGitRepo(t, "main")
	stdout, stderr, err := runCodemapIn(t, repo, "status")
	if err != nil {
		t.Fatalf("status failed: %v\n%s", err, stderr)
	}
	for _, want := range []string{"codemap ", "Project root: ", "Config: ", "Watch daemon "} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("status output missing %q:\n%s", want, stdout)
		}
	}
	help, _, _ := runCodemapWithInput("", "--help")
	for _, want := range []string{"--hubs", "--file <file>", "codemap status"} {
		if !strings.Contains(help, want) {
			t.Fatalf("--help does not mention %q", want)
		}
	}
}

func TestTopologyWithNoModulesPointsAtDeps(t *testing.T) {
	original := buildTopologyGraphOnly
	buildTopologyGraphOnly = func(context.Context, string) (*topology.Graph, topology.CacheIdentity, error) {
		return &topology.Graph{Coverage: topology.Coverage{Status: topology.CoverageUnavailable}}, topology.CacheIdentity{}, nil
	}
	t.Cleanup(func() { buildTopologyGraphOnly = original })

	var out bytes.Buffer
	if err := runTopologyMode(context.Background(), "/repo", "", "", "", false, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Modules: 0") || !strings.Contains(out.String(), "codemap --deps") {
		t.Fatalf("empty topology must explain itself:\n%s", out.String())
	}
}
