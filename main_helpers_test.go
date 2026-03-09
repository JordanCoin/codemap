package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codemap/handoff"
	"codemap/scanner"
	"codemap/watch"
)

func TestIsGitHubURLAndExtractRepoName(t *testing.T) {
	cases := []struct {
		url    string
		isRepo bool
		repo   string
	}{
		{url: "github.com/acme/codemap", isRepo: true, repo: "acme/codemap"},
		{url: "https://github.com/acme/codemap.git", isRepo: true, repo: "acme/codemap"},
		{url: "http://github.com/acme/codemap/", isRepo: true, repo: "acme/codemap"},
		{url: "gitlab.com/acme/codemap", isRepo: true, repo: "acme/codemap"},
		{url: "example.com/acme/codemap", isRepo: false, repo: "example.com/acme/codemap"},
	}

	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			if got := isGitHubURL(tc.url); got != tc.isRepo {
				t.Fatalf("isGitHubURL(%q) = %v, want %v", tc.url, got, tc.isRepo)
			}
			if got := extractRepoName(tc.url); got != tc.repo {
				t.Fatalf("extractRepoName(%q) = %q, want %q", tc.url, got, tc.repo)
			}
		})
	}
}

func TestRunHandoffSubcommandLatestMissing(t *testing.T) {
	root := t.TempDir()
	out := captureMainOutput(func() { runHandoffSubcommand([]string{"--latest", root}) })
	if !strings.Contains(out, "No handoff artifact found") {
		t.Fatalf("expected missing handoff message, got:\n%s", out)
	}
}

func TestRunHandoffSubcommandLatestVariants(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}

	artifact := &handoff.Artifact{
		SchemaVersion: handoff.SchemaVersion,
		Branch:        "feature/test",
		BaseRef:       "main",
		Prefix:        handoff.PrefixSnapshot{FileCount: 3},
		Delta: handoff.DeltaSnapshot{
			Changed: []handoff.FileStub{{Path: "main.go", Status: "modified", Size: 42}},
			RecentEvents: []handoff.EventSummary{{
				Time:  time.Date(2026, time.March, 9, 12, 0, 0, 0, time.UTC),
				Op:    "WRITE",
				Path:  "main.go",
				Delta: 2,
			}},
		},
	}
	if err := handoff.WriteLatest(root, artifact); err != nil {
		t.Fatalf("WriteLatest error: %v", err)
	}

	state := watch.State{
		UpdatedAt: time.Now(),
		Importers: map[string][]string{"main.go": {"a.go", "b.go", "c.go"}},
		Imports:   map[string][]string{"main.go": {"dep.go"}},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".codemap", "state.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	prefixJSON := captureMainOutput(func() { runHandoffSubcommand([]string{"--latest", "--prefix", "--json", root}) })
	if !strings.Contains(prefixJSON, "\"file_count\": 3") {
		t.Fatalf("expected prefix JSON output, got:\n%s", prefixJSON)
	}

	deltaOut := captureMainOutput(func() { runHandoffSubcommand([]string{"--latest", "--delta", root}) })
	if !strings.Contains(deltaOut, "## Handoff Delta") || !strings.Contains(deltaOut, "`main.go` (modified)") {
		t.Fatalf("expected delta markdown output, got:\n%s", deltaOut)
	}

	detailOut := captureMainOutput(func() { runHandoffSubcommand([]string{"--latest", "--detail", "main.go", root}) })
	checks := []string{"## Handoff File Detail", "`dep.go`", "`a.go`", "`b.go`", "`c.go`"}
	for _, check := range checks {
		if !strings.Contains(detailOut, check) {
			t.Fatalf("expected detail output to contain %q, got:\n%s", check, detailOut)
		}
	}
}

func TestRunWatchSubcommandInactiveCases(t *testing.T) {
	root := t.TempDir()

	status := captureMainOutput(func() { runWatchSubcommand("status", root) })
	if !strings.Contains(status, "Watch daemon not running") {
		t.Fatalf("expected inactive status output, got:\n%s", status)
	}

	stop := captureMainOutput(func() { runWatchSubcommand("stop", root) })
	if !strings.Contains(stop, "Watch daemon not running") {
		t.Fatalf("expected inactive stop output, got:\n%s", stop)
	}
}

func TestIsTerminalOnPipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe error: %v", err)
	}
	defer r.Close()
	defer w.Close()
	if isTerminal(w) {
		t.Fatal("pipe should not be detected as a terminal")
	}
}

func TestMainDispatchesSubcommands(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		args  []string
		check string
	}{
		{name: "watch", args: []string{"codemap", "watch", "status", root}, check: "Watch daemon not running"},
		{name: "handoff", args: []string{"codemap", "handoff", "--latest", root}, check: "No handoff artifact found"},
		{name: "config", args: []string{"codemap", "config", "show", root}, check: "No config file found."},
		{name: "setup", args: []string{"codemap", "setup", "--no-config", "--no-hooks", root}, check: "Config: skipped (--no-config)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := runMainWithArgs(t, tt.args)
			if !strings.Contains(out, tt.check) {
				t.Fatalf("expected output to contain %q, got:\n%s", tt.check, out)
			}
		})
	}
}

func TestMainJSONModeUsesConfigDefaults(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".codemap", "config.json"), []byte("{\"only\": [\"go\"]}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("docs\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := runMainWithArgs(t, []string{"codemap", "--json", root})

	var project scanner.Project
	if err := json.Unmarshal([]byte(out), &project); err != nil {
		t.Fatalf("expected JSON output, got error %v with body:\n%s", err, out)
	}
	if project.Mode != "tree" {
		t.Fatalf("project mode = %q, want tree", project.Mode)
	}
	if len(project.Files) != 1 || project.Files[0].Path != "main.go" {
		t.Fatalf("expected config only-filter to keep just main.go, got %+v", project.Files)
	}
	if len(project.Only) != 1 || project.Only[0] != "go" {
		t.Fatalf("expected project only filters to come from config, got %+v", project.Only)
	}
}

func captureMainOutput(fn func()) string {
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		panic(err)
	}
	defer r.Close()

	os.Stdout = w
	os.Stderr = w
	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()

	fn()
	_ = w.Close()

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

func runMainWithArgs(t *testing.T, args []string) string {
	t.Helper()

	oldArgs := os.Args
	oldFlags := flag.CommandLine
	os.Args = args
	flag.CommandLine = flag.NewFlagSet(args[0], flag.ContinueOnError)
	flag.CommandLine.SetOutput(io.Discard)
	defer func() {
		os.Args = oldArgs
		flag.CommandLine = oldFlags
	}()

	return captureMainOutput(func() { main() })
}
