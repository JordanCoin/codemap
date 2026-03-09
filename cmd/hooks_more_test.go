package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codemap/config"
	"codemap/handoff"
	"codemap/limits"
	"codemap/watch"
)

func captureOutputAndError(t *testing.T, fn func()) (string, string) {
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

func withStdinInput(t *testing.T, input string, fn func()) {
	t.Helper()

	old := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, input); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	os.Stdin = r
	defer func() {
		os.Stdin = old
		_ = r.Close()
	}()

	fn()
}

func writeProjectConfig(t *testing.T, root string, cfg config.ProjectConfig) {
	t.Helper()

	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.ConfigPath(root), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeStateOnly(t *testing.T, root string, state watch.State) {
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
}

func TestHookTimeoutHelpers(t *testing.T) {
	timeoutErr := &HookTimeoutError{Hook: "prompt-submit", Timeout: 25 * time.Millisecond}
	if got := timeoutErr.Error(); got != `hook "prompt-submit" timed out after 25ms` {
		t.Fatalf("HookTimeoutError.Error() = %q", got)
	}
	if !IsHookTimeoutError(timeoutErr) {
		t.Fatal("expected IsHookTimeoutError to detect timeout error")
	}
	if IsHookTimeoutError(errors.New("plain error")) {
		t.Fatal("did not expect plain error to match timeout error")
	}

	err := RunHookWithTimeout("unknown-hook", t.TempDir(), 0)
	if err == nil || !strings.Contains(err.Error(), "unknown hook") {
		t.Fatalf("expected unknown hook error, got %v", err)
	}
}

func TestWaitForDaemonState(t *testing.T) {
	root := t.TempDir()
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(150 * time.Millisecond)
		_ = os.MkdirAll(filepath.Join(root, ".codemap"), 0o755)
		data, _ := json.Marshal(watch.State{
			UpdatedAt: time.Now(),
			FileCount: 7,
		})
		_ = os.WriteFile(filepath.Join(root, ".codemap", "state.json"), data, 0o644)
	}()

	state := waitForDaemonState(root, time.Second)
	<-done
	if state == nil {
		t.Fatal("expected state before timeout")
	}
	if state.FileCount != 7 {
		t.Fatalf("expected file count 7, got %d", state.FileCount)
	}
}

func TestGetRecentHandoffAndBranchDetection(t *testing.T) {
	root := t.TempDir()
	if got := getRecentHandoff(root); got != nil {
		t.Fatalf("expected nil without handoff, got %+v", got)
	}

	oldArtifact := &handoff.Artifact{
		SchemaVersion: handoff.SchemaVersion,
		GeneratedAt:   time.Now().Add(-25 * time.Hour),
		Branch:        "feature/old",
	}
	if err := handoff.WriteLatest(root, oldArtifact); err != nil {
		t.Fatalf("WriteLatest old artifact: %v", err)
	}
	if got := getRecentHandoff(root); got != nil {
		t.Fatalf("expected stale handoff to be ignored, got %+v", got)
	}

	freshArtifact := &handoff.Artifact{
		SchemaVersion: handoff.SchemaVersion,
		GeneratedAt:   time.Now(),
		Branch:        "feature/fresh",
		Delta: handoff.DeltaSnapshot{
			Changed: []handoff.FileStub{{Path: "main.go", Status: "modified"}},
		},
	}
	if err := handoff.WriteLatest(root, freshArtifact); err != nil {
		t.Fatalf("WriteLatest fresh artifact: %v", err)
	}
	if got := getRecentHandoff(root); got == nil || got.Branch != "feature/fresh" {
		t.Fatalf("expected fresh handoff, got %+v", got)
	}

	repo := makeRepoOnBranch(t, "feature/current")
	branch, ok := gitCurrentBranch(repo)
	if !ok || branch != "feature/current" {
		t.Fatalf("gitCurrentBranch() = %q, %v", branch, ok)
	}
	if branch, ok := gitCurrentBranch(t.TempDir()); ok || branch != "" {
		t.Fatalf("expected non-git root to have unknown branch, got %q, %v", branch, ok)
	}
}

func TestShowDiffVsMainUsesLightweightPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := makeRepoOnBranch(t, "main")
	runGitTestCmd(t, root, "checkout", "-b", "feature/lightweight-diff")
	for i := 0; i < 22; i++ {
		name := filepath.Join(root, fmt.Sprintf("pkg/file%02d.go", i))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte("package pkg\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGitTestCmd(t, root, "add", ".")
	runGitTestCmd(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "feature changes")

	stdout, _ := captureOutputAndError(t, func() {
		showDiffVsMain(root, limits.LargeRepoFileCount+1, true, config.ProjectConfig{})
	})

	if !strings.Contains(stdout, "Changes on branch 'feature/lightweight-diff' vs main") {
		t.Fatalf("expected branch diff header, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "pkg/file00.go") {
		t.Fatalf("expected first changed file in output, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "... and 2 more files") {
		t.Fatalf("expected truncation indicator, got:\n%s", stdout)
	}
}

func TestExtractFilePathAndEditHooks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "pkg", "types.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("package pkg\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeWatchState(t, root, watch.State{
		UpdatedAt: time.Now(),
		FileCount: 3,
		Importers: map[string][]string{
			"pkg/types.go": {"cmd/a.go", "cmd/b.go", "cmd/c.go"},
		},
		Imports: map[string][]string{
			"pkg/types.go":  {"shared/hub.go"},
			"cmd/a.go":      {"pkg/types.go"},
			"cmd/b.go":      {"pkg/types.go"},
			"cmd/c.go":      {"pkg/types.go"},
			"shared/hub.go": {"x.go", "y.go", "z.go"},
		},
	})

	withStdinInput(t, `{"file_path":"`+target+`"}`, func() {
		got, err := extractFilePathFromStdin()
		if err != nil {
			t.Fatalf("extractFilePathFromStdin() error: %v", err)
		}
		if got != target {
			t.Fatalf("extractFilePathFromStdin() = %q, want %q", got, target)
		}
	})

	withStdinInput(t, `garbage "file_path": "`+target+`"`, func() {
		got, err := extractFilePathFromStdin()
		if err != nil {
			t.Fatalf("expected regex fallback, got error %v", err)
		}
		if got != target {
			t.Fatalf("fallback extracted %q, want %q", got, target)
		}
	})

	checkOutput := func(fn func(string) error) {
		withStdinInput(t, `{"file_path":"`+target+`"}`, func() {
			out := captureOutput(func() {
				if err := fn(root); err != nil {
					t.Fatalf("hook returned error: %v", err)
				}
			})
			if !strings.Contains(out, "HUB FILE: pkg/types.go") {
				t.Fatalf("expected hub warning, got:\n%s", out)
			}
		})
	}
	checkOutput(hookPreEdit)
	checkOutput(hookPostEdit)
}

func TestCheckFileImportersAndRouteSuggestions(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "pkg", "types.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("package pkg\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeWatchState(t, root, watch.State{
		UpdatedAt: time.Now(),
		FileCount: 4,
		Importers: map[string][]string{
			"pkg/types.go":  {"a.go", "b.go", "c.go"},
			"shared/hub.go": {"d.go", "e.go", "f.go"},
		},
		Imports: map[string][]string{
			"pkg/types.go": {"shared/hub.go"},
		},
	})

	out := captureOutput(func() {
		if err := checkFileImporters(root, target); err != nil {
			t.Fatalf("checkFileImporters() error: %v", err)
		}
	})
	if !strings.Contains(out, "HUB FILE: pkg/types.go") {
		t.Fatalf("expected hub warning, got:\n%s", out)
	}
	if !strings.Contains(out, "Imports 1 hub(s): shared/hub.go") {
		t.Fatalf("expected hub import summary, got:\n%s", out)
	}

	cfg := config.ProjectConfig{
		Routing: config.RoutingConfig{
			Retrieval: config.RetrievalConfig{Strategy: "keyword", TopK: 2},
			Subsystems: []config.Subsystem{
				{
					ID:       "watching",
					Keywords: []string{"hook", "daemon", "events"},
					Docs:     []string{"docs/HOOKS.md"},
					Agents:   []string{"codemap-hook-triage"},
				},
			},
		},
	}
	out = captureOutput(func() {
		showRouteSuggestions("hook daemon events need triage", cfg, 2)
	})
	if !strings.Contains(out, "Suggested context routes") || !strings.Contains(out, "docs/HOOKS.md") {
		t.Fatalf("expected route suggestions, got:\n%s", out)
	}
}

func TestHookPromptSubmitShowsContextAndProgress(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root, config.ProjectConfig{
		Routing: config.RoutingConfig{
			Retrieval: config.RetrievalConfig{Strategy: "keyword", TopK: 2},
			Subsystems: []config.Subsystem{
				{
					ID:       "watching",
					Keywords: []string{"hook", "daemon", "events"},
					Docs:     []string{"docs/HOOKS.md"},
				},
			},
		},
	})
	writeWatchState(t, root, watch.State{
		UpdatedAt: time.Now(),
		FileCount: 5,
		Importers: map[string][]string{
			"pkg/types.go": {"a.go", "b.go", "c.go"},
		},
		RecentEvents: []watch.Event{
			{Path: "pkg/types.go", Op: "WRITE", IsHub: true},
			{Path: "cmd/run.go", Op: "WRITE"},
		},
	})

	withStdinInput(t, `{"prompt":"please inspect pkg/types.go because hook daemon events are noisy"}`, func() {
		out := captureOutput(func() {
			if err := hookPromptSubmit(root); err != nil {
				t.Fatalf("hookPromptSubmit() error: %v", err)
			}
		})
		checks := []string{
			"Context for mentioned files",
			"pkg/types.go is a HUB",
			"Suggested context routes",
			"watching",
			"Session so far: 2 files edited, 1 hub edits",
		}
		for _, check := range checks {
			if !strings.Contains(out, check) {
				t.Fatalf("expected %q in output, got:\n%s", check, out)
			}
		}
	})
}

func TestFindChildReposAndSessionStartVariants(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	t.Run("non git repo exits early", func(t *testing.T) {
		out := captureOutput(func() {
			if err := hookSessionStart(t.TempDir()); err != nil {
				t.Fatalf("hookSessionStart() error: %v", err)
			}
		})
		if !strings.Contains(out, "Not a git repository") {
			t.Fatalf("expected non-git notice, got:\n%s", out)
		}
	})

	t.Run("multi repo parent honors gitignore", func(t *testing.T) {
		root := t.TempDir()
		runGitTestCmd(t, root, "init")
		if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored-child\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"svc-a", "svc-b", "ignored-child"} {
			child := filepath.Join(root, name)
			if err := os.MkdirAll(child, 0o755); err != nil {
				t.Fatal(err)
			}
			runGitTestCmd(t, child, "init")
		}

		repos := findChildRepos(root)
		got := strings.Join(repos, ",")
		if len(repos) != 2 || !strings.Contains(got, "svc-a") || !strings.Contains(got, "svc-b") || strings.Contains(got, "ignored-child") {
			t.Fatalf("unexpected child repos: %v", repos)
		}

		stdout, _ := captureOutputAndError(t, func() {
			if err := hookSessionStart(root); err != nil {
				t.Fatalf("hookSessionStart() error: %v", err)
			}
		})
		if !strings.Contains(stdout, "Multi-Repo Project Context") {
			t.Fatalf("expected multi-repo output, got:\n%s", stdout)
		}
	})

	t.Run("git repo shows hubs and recent handoff", func(t *testing.T) {
		root := makeRepoOnBranch(t, "feature/session-start")
		writeWatchState(t, root, watch.State{
			UpdatedAt: time.Now(),
			FileCount: 5,
			Hubs:      []string{"pkg/types.go"},
			Importers: map[string][]string{
				"pkg/types.go": {"a.go", "b.go", "c.go"},
			},
		})
		if err := watch.WritePID(root); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { watch.RemovePID(root) })

		if err := handoff.WriteLatest(root, &handoff.Artifact{
			SchemaVersion: handoff.SchemaVersion,
			GeneratedAt:   time.Now(),
			Branch:        "feature/session-start",
			BaseRef:       "main",
			Delta: handoff.DeltaSnapshot{
				Changed: []handoff.FileStub{{Path: "main.go", Status: "modified"}},
			},
		}); err != nil {
			t.Fatal(err)
		}

		stdout, _ := captureOutputAndError(t, func() {
			if err := hookSessionStart(root); err != nil {
				t.Fatalf("hookSessionStart() error: %v", err)
			}
		})

		checks := []string{
			"Project Context",
			"High-impact files",
			"pkg/types.go",
			"Recent handoff",
			"feature/session-start",
		}
		for _, check := range checks {
			if !strings.Contains(stdout, check) {
				t.Fatalf("expected %q in output, got:\n%s", check, stdout)
			}
		}
		if strings.Contains(stdout, "Changes on branch") {
			t.Fatalf("expected recent handoff to suppress diff output, got:\n%s", stdout)
		}
	})
}

func TestHookSessionStopSummaryBranches(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	t.Run("event timeline branch writes handoff", func(t *testing.T) {
		root := makeRepoOnBranch(t, "feature/session-stop")
		writeStateOnly(t, root, watch.State{
			UpdatedAt: time.Now(),
			FileCount: 8,
			RecentEvents: []watch.Event{
				{Time: time.Now().Add(-2 * time.Minute), Op: "WRITE", Path: "main.go", Delta: 4},
				{Time: time.Now().Add(-time.Minute), Op: "WRITE", Path: "pkg/types.go", Delta: -1, IsHub: true},
			},
		})

		out := captureOutput(func() {
			if err := hookSessionStop(root); err != nil {
				t.Fatalf("hookSessionStop() error: %v", err)
			}
		})

		for _, check := range []string{"Session Summary", "Edit Timeline:", "Stats:", "Saved handoff"} {
			if !strings.Contains(out, check) {
				t.Fatalf("expected %q in output, got:\n%s", check, out)
			}
		}
		if _, err := os.Stat(handoff.LatestPath(root)); err != nil {
			t.Fatalf("expected handoff artifact to exist: %v", err)
		}
	})

	t.Run("fallback git diff branch lists modified files", func(t *testing.T) {
		root := makeRepoOnBranch(t, "main")
		if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		out := captureOutput(func() {
			if err := hookSessionStop(root); err != nil {
				t.Fatalf("hookSessionStop() error: %v", err)
			}
		})

		if !strings.Contains(out, "Files modified:") || !strings.Contains(out, "main.go") {
			t.Fatalf("expected modified file list, got:\n%s", out)
		}
	})
}
