package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codemap/cmd"
	"codemap/scanner"
	"codemap/watch"
)

func TestRenderImportersReportExplainsEmptyResult(t *testing.T) {
	report := scanner.ImportersReport{
		Root:            "/repo",
		Mode:            "importers",
		File:            "watch/events.go",
		Package:         "codemap/watch",
		PackageSiblings: 10,
	}

	var buf strings.Builder
	renderImportersReportCLI(&buf, report)
	out := buf.String()
	// A Go file inside a multi-file package: the zero is a cross-package
	// zero, and the line must say what the graph does not model, with the
	// count, instead of a bare "No files import" (#138 part 1).
	want := "No cross-package importers of watch/events.go. Same-package references are not modeled (10 other files in package codemap/watch).\n"
	if !strings.Contains(out, want) {
		t.Fatalf("empty Go importers result must name the unmodeled siblings:\n got %q\nwant %q", out, want)
	}
	if strings.Contains(out, "No files import") {
		t.Fatalf("a Go file with package siblings must not print a bare zero:\n%q", out)
	}

	// The token-budgeted blast-radius renderer must keep omitting empty
	// sections — the explanation is CLI-only.
	if bundleOut := renderImportersReportString(report); strings.Contains(bundleOut, "No files import") {
		t.Fatalf("bundle renderer should omit empty sections, got:\n%q", bundleOut)
	}
}

// A partial scan must not read as a confident negative: the empty importers
// branch shows coverage and skips the Go same-package note for non-Go files.
func TestRenderImportersReportCLIEmitsCoverageInEmptyBranch(t *testing.T) {
	report := scanner.ImportersReport{
		Root:           "/repo",
		Mode:           "importers",
		File:           "web/util.ts",
		CoverageStatus: "partial",
		CoverageNotes:  []string{"ast-grep not found (checked bundled tools and PATH)", "Go parser fallback recovered 1 of 1 Go files"},
	}

	var buf strings.Builder
	renderImportersReportCLI(&buf, report)
	out := buf.String()
	if !strings.Contains(out, "No files import web/util.ts") {
		t.Fatalf("empty importers result must say so:\n%q", out)
	}
	if !strings.Contains(out, "Coverage: partial") {
		t.Fatalf("empty result must carry the coverage provenance:\n%q", out)
	}
	if strings.Contains(out, "same package") {
		t.Fatalf("Go same-package note must not be printed about a TypeScript file:\n%q", out)
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
	shortOut, shortErr, err := runCodemapWithInput("", "-h")
	if err != nil {
		t.Fatalf("-h failed: %v\n%s", err, shortErr)
	}
	if shortOut != out {
		t.Fatalf("-h differs from --help:\n--- -h ---\n%s\n--- --help ---\n%s", shortOut, out)
	}
	for _, sub := range []string{
		"--json", "--debug", "--watch", "Claude Code and Codex",
		"codemap watch", "codemap hook", "codemap config", "codemap setup",
		"codemap doctor", "codemap mcp", "codemap skill", "codemap plugin",
		"codemap context", "codemap serve", "codemap handoff", "codemap blast-radius",
		// #151: the daemon side effect of session-start is stated, not hidden.
		"session-start starts 'codemap watch daemon <root>'", "'codemap watch stop' ends it",
	} {
		if !strings.Contains(out, sub) {
			t.Fatalf("--help does not mention %q:\n%s", sub, out)
		}
	}
}

func TestSubcommandHelpReturnsSuccess(t *testing.T) {
	for _, command := range []string{"doctor", "context", "serve"} {
		for _, help := range []string{"--help", "-h"} {
			args := []string{command, help}
			stdout, stderr, err := runCodemapWithInput("", args...)
			if err != nil {
				t.Errorf("%v failed: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout, stderr)
				continue
			}
			if !strings.Contains(stdout+stderr, "Usage") {
				t.Errorf("%v omitted usage:\n%s%s", args, stdout, stderr)
			}
		}
	}
}

// TestWaitForDaemonExitReturnsWhenRootRemoved is #227: a daemon whose root
// (typically an agent worktree) is deleted must exit without a signal.
func TestWaitForDaemonExitReturnsWhenRootRemoved(t *testing.T) {
	root := t.TempDir()
	done := make(chan struct{})
	go func() {
		waitForDaemonExit(make(chan os.Signal), 5*time.Millisecond, func() bool { return cmd.DaemonShouldExit(root, time.Now()) })
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("waitForDaemonExit returned while the root still existed")
	default:
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("waitForDaemonExit did not notice the removed root")
	}
}

// TestWatchStatusAndStopAllCoverRegisteredDaemons is #155 defect 2 and #151
// item 3: every published daemon is listed and stoppable from any directory.
func TestWatchStatusAndStopAllCoverRegisteredDaemons(t *testing.T) {
	t.Setenv("CODEMAP_DAEMON_REGISTRY", t.TempDir())
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	process := exec.Command(os.Args[0], "-test.run=TestMainWatchHelperProcess", "--", "watch", "daemon", root)
	process.Env = append(os.Environ(), "CODEMAP_MAIN_WATCH_HELPER=1")
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan struct{})
	go func() { _ = process.Wait(); close(waited) }()
	t.Cleanup(func() { _ = process.Process.Kill(); <-waited })
	if err := watch.WriteProcessPID(root, process.Process.Pid); err != nil {
		t.Fatal(err)
	}

	out, err := runCodemap("watch", "status", "--all")
	if err != nil {
		t.Fatalf("watch status --all: %v\n%s", err, out)
	}
	if want := fmt.Sprintf("pid %d", process.Process.Pid); !strings.Contains(out, want) || !strings.Contains(out, root) {
		t.Fatalf("status --all omitted the registered daemon (want %q and %q):\n%s", want, root, out)
	}

	out, err = runCodemap("watch", "stop", "--all")
	if err != nil {
		t.Fatalf("watch stop --all: %v\n%s", err, out)
	}
	if !strings.Contains(out, "stopped") {
		t.Fatalf("stop --all did not report the stop:\n%s", out)
	}
	<-waited
	if out, err = runCodemap("watch", "status", "--all"); err != nil || !strings.Contains(out, "No watch daemons running") {
		t.Fatalf("status --all after stop --all = %v:\n%s", err, out)
	}
}
