package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codemap/config"
	"codemap/handoff"
	"codemap/scanner"
	"codemap/watch"
)

func captureMainStreams(t *testing.T, fn func()) (string, string) {
	t.Helper()

	oldOut := os.Stdout
	oldErr := os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	os.Stdout = outW
	os.Stderr = errW
	fn()
	_ = outW.Close()
	_ = errW.Close()
	os.Stdout = oldOut
	os.Stderr = oldErr

	var stdout, stderr bytes.Buffer
	_, _ = io.Copy(&stdout, outR)
	_, _ = io.Copy(&stderr, errR)
	return stdout.String(), stderr.String()
}

func runCodemapWithInput(input string, args ...string) (string, string, error) {
	cmd := exec.Command("./codemap_test_binary", args...)
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	return out.String(), stderr.String(), err
}

func writeMainWatchState(t *testing.T, root string, state watch.State, running bool) {
	t.Helper()

	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".codemap", "state.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if running {
		if err := watch.WritePID(root); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { watch.RemovePID(root) })
	}
}

func runGitMainTestCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}

func makeMainTestRepo(t *testing.T, branch string) string {
	t.Helper()

	root := t.TempDir()
	runGitMainTestCmd(t, root, "init")
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitMainTestCmd(t, root, "add", ".")
	runGitMainTestCmd(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")
	runGitMainTestCmd(t, root, "branch", "-M", branch)
	return root
}

func writeImportersFixture(t *testing.T, root string) {
	t.Helper()

	files := map[string]string{
		"go.mod":            "module example.com/demo\n\ngo 1.22\n",
		"pkg/types/types.go": "package types\n\ntype Item struct{}\n",
		"a/a.go":            "package a\n\nimport _ \"example.com/demo/pkg/types\"\n",
		"b/b.go":            "package b\n\nimport _ \"example.com/demo/pkg/types\"\n",
		"c/c.go":            "package c\n\nimport _ \"example.com/demo/pkg/types\"\n",
		"main.go":           "package main\n\nimport _ \"example.com/demo/pkg/types\"\n",
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
}

func TestRunWatchSubcommandMessages(t *testing.T) {
	root := t.TempDir()

	stdout, _ := captureMainStreams(t, func() { runWatchSubcommand("status", root) })
	if !strings.Contains(stdout, "Watch daemon not running") {
		t.Fatalf("expected not-running status, got:\n%s", stdout)
	}

	writeMainWatchState(t, root, watch.State{
		UpdatedAt: time.Now(),
		FileCount: 9,
		Hubs:      []string{"pkg/types.go"},
	}, true)

	stdout, _ = captureMainStreams(t, func() { runWatchSubcommand("start", root) })
	if !strings.Contains(stdout, "Watch daemon already running") {
		t.Fatalf("expected already-running start output, got:\n%s", stdout)
	}

	stdout, _ = captureMainStreams(t, func() { runWatchSubcommand("status", root) })
	for _, check := range []string{"Watch daemon running", "Files: 9", "Hubs: 1"} {
		if !strings.Contains(stdout, check) {
			t.Fatalf("expected %q in output, got:\n%s", check, stdout)
		}
	}

	watch.RemovePID(root)
	stdout, _ = captureMainStreams(t, func() { runWatchSubcommand("stop", root) })
	if !strings.Contains(stdout, "Watch daemon not running") {
		t.Fatalf("expected stop to report not running, got:\n%s", stdout)
	}
}

func TestRunHandoffSubcommandLatestVariantsMore(t *testing.T) {
	root := t.TempDir()

	stdout, _ := captureMainStreams(t, func() {
		runHandoffSubcommand([]string{"--latest", root})
	})
	if !strings.Contains(stdout, "No handoff artifact found") {
		t.Fatalf("expected missing handoff message, got:\n%s", stdout)
	}

	artifact := &handoff.Artifact{
		SchemaVersion: handoff.SchemaVersion,
		GeneratedAt:   time.Now(),
		Branch:        "feature/test",
		BaseRef:       "main",
		Prefix:        handoff.PrefixSnapshot{FileCount: 4},
		Delta: handoff.DeltaSnapshot{
			Changed: []handoff.FileStub{{Path: "main.go", Status: "modified"}},
		},
	}
	if err := handoff.WriteLatest(root, artifact); err != nil {
		t.Fatal(err)
	}

	stdout, _ = captureMainStreams(t, func() {
		runHandoffSubcommand([]string{"--latest", "--prefix", "--json", root})
	})
	if !strings.Contains(stdout, `"file_count": 4`) {
		t.Fatalf("expected prefix JSON output, got:\n%s", stdout)
	}
}

func TestRunImportersMode(t *testing.T) {
	if !scanner.NewAstGrepAnalyzer().Available() {
		t.Skip("ast-grep not available")
	}

	root := t.TempDir()
	writeImportersFixture(t, root)

	stdout, _ := captureMainStreams(t, func() {
		runImportersMode(root, filepath.Join(root, "pkg", "types", "types.go"))
	})

	for _, check := range []string{"HUB FILE: pkg/types/types.go", "Imported by 4 files", "Dependents:"} {
		if !strings.Contains(stdout, check) {
			t.Fatalf("expected %q in output, got:\n%s", check, stdout)
		}
	}
}

func TestSubcommandDispatchViaBinary(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.ProjectConfig{Only: []string{"go"}}
	cfgData, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.ConfigPath(root), cfgData, 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("watch status", func(t *testing.T) {
		stdout, stderr, err := runCodemapWithInput("", "watch", "status", root)
		if err != nil {
			t.Fatalf("watch status failed: %v\nstderr=%s", err, stderr)
		}
		if !strings.Contains(stdout, "Watch daemon not running") {
			t.Fatalf("unexpected watch status output:\n%s", stdout)
		}
	})

	t.Run("hook usage", func(t *testing.T) {
		_, stderr, err := runCodemapWithInput("", "hook")
		if err == nil {
			t.Fatal("expected hook command without name to fail")
		}
		if !strings.Contains(stderr, "Usage: codemap hook <hookname>") {
			t.Fatalf("unexpected stderr:\n%s", stderr)
		}
	})

	t.Run("unknown hook", func(t *testing.T) {
		_, stderr, err := runCodemapWithInput("", "hook", "unknown-hook", root)
		if err == nil {
			t.Fatal("expected unknown hook to fail")
		}
		if !strings.Contains(stderr, "Hook error: unknown hook") {
			t.Fatalf("unexpected stderr:\n%s", stderr)
		}
	})

	t.Run("config show", func(t *testing.T) {
		stdout, stderr, err := runCodemapWithInput("", "config", "show", root)
		if err != nil {
			t.Fatalf("config show failed: %v\nstderr=%s", err, stderr)
		}
		if !strings.Contains(stdout, "only:    go") {
			t.Fatalf("unexpected config show output:\n%s", stdout)
		}
	})

	t.Run("handoff latest missing", func(t *testing.T) {
		missingRoot := t.TempDir()
		stdout, stderr, err := runCodemapWithInput("", "handoff", "--latest", missingRoot)
		if err != nil {
			t.Fatalf("handoff latest failed: %v\nstderr=%s", err, stderr)
		}
		if !strings.Contains(stdout, "No handoff artifact found") {
			t.Fatalf("unexpected handoff output:\n%s", stdout)
		}
	})
}
