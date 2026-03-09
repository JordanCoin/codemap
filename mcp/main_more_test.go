package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codemap/scanner"
	"codemap/watch"
)

func withWatcherRegistry(t *testing.T) {
	t.Helper()
	watchersMu.Lock()
	watchers = make(map[string]*watch.Daemon)
	watchersMu.Unlock()
	t.Cleanup(func() {
		watchersMu.Lock()
		for path, daemon := range watchers {
			if daemon != nil {
				daemon.Stop()
			}
			delete(watchers, path)
		}
		watchers = make(map[string]*watch.Daemon)
		watchersMu.Unlock()
	})
}

func runGitMCPTestCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}

func makeMCPGitRepo(t *testing.T, branch string) string {
	t.Helper()

	root := t.TempDir()
	runGitMCPTestCmd(t, root, "init")
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitMCPTestCmd(t, root, "add", ".")
	runGitMCPTestCmd(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")
	runGitMCPTestCmd(t, root, "branch", "-M", branch)
	return root
}

func writeMCPImportersFixture(t *testing.T, root string) {
	t.Helper()

	files := map[string]string{
		"go.mod":             "module example.com/mcpdemo\n\ngo 1.22\n",
		"pkg/types/types.go": "package types\n\ntype Item struct{}\n",
		"a/a.go":             "package a\n\nimport _ \"example.com/mcpdemo/pkg/types\"\n",
		"b/b.go":             "package b\n\nimport _ \"example.com/mcpdemo/pkg/types\"\n",
		"c/c.go":             "package c\n\nimport _ \"example.com/mcpdemo/pkg/types\"\n",
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

func TestHandleGetDependenciesAndDiff(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	if !scanner.NewAstGrepAnalyzer().Available() {
		t.Skip("ast-grep not available")
	}

	root := makeMCPGitRepo(t, "main")
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, _, err := handleGetDiff(context.Background(), nil, DiffInput{Path: root, Ref: "main"})
	if err != nil {
		t.Fatalf("handleGetDiff error: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "main.go") {
		t.Fatalf("expected diff output to mention changed file, got:\n%s", out)
	}

	res, _, err = handleGetDependencies(context.Background(), nil, PathInput{Path: root})
	if err != nil {
		t.Fatalf("handleGetDependencies error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected dependencies success result, got error:\n%s", resultText(t, res))
	}
	if !strings.Contains(resultText(t, res), "Dependency Flow") {
		t.Fatalf("expected dependency output, got:\n%s", resultText(t, res))
	}
}

func TestHandleWatchLifecycleAndActivity(t *testing.T) {
	withWatcherRegistry(t)

	startRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(startRoot, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	startRes, _, err := handleStartWatch(context.Background(), nil, WatchInput{Path: startRoot})
	if err != nil {
		t.Fatalf("handleStartWatch error: %v", err)
	}
	startOut := resultText(t, startRes)
	if !strings.Contains(startOut, "Live watcher started for:") {
		t.Fatalf("unexpected start output:\n%s", startOut)
	}

	againRes, _, err := handleStartWatch(context.Background(), nil, WatchInput{Path: startRoot})
	if err != nil {
		t.Fatalf("handleStartWatch second call error: %v", err)
	}
	if !strings.Contains(resultText(t, againRes), "Already watching:") {
		t.Fatalf("expected already-watching response, got:\n%s", resultText(t, againRes))
	}

	stopRes, _, err := handleStopWatch(context.Background(), nil, WatchInput{Path: startRoot})
	if err != nil {
		t.Fatalf("handleStopWatch error: %v", err)
	}
	if !strings.Contains(resultText(t, stopRes), "Watcher stopped for:") {
		t.Fatalf("unexpected stop output:\n%s", resultText(t, stopRes))
	}

	activityRoot := t.TempDir()
	daemon, err := watch.NewDaemon(activityRoot, false)
	if err != nil {
		t.Fatalf("watch.NewDaemon error: %v", err)
	}
	absActivityRoot, _ := filepath.Abs(activityRoot)
	watchersMu.Lock()
	watchers[absActivityRoot] = daemon
	watchersMu.Unlock()

	graph := daemon.GetGraph()
	graph.Files["main.go"] = &scanner.FileInfo{Path: "main.go"}
	graph.Files["pkg/types.go"] = &scanner.FileInfo{Path: "pkg/types.go"}

	noActivityRes, _, err := handleGetActivity(context.Background(), nil, WatchActivityInput{Path: activityRoot, Minutes: 60})
	if err != nil {
		t.Fatalf("handleGetActivity no-activity error: %v", err)
	}
	if !strings.Contains(resultText(t, noActivityRes), "No activity in the last 60 minutes.") {
		t.Fatalf("expected no-activity response, got:\n%s", resultText(t, noActivityRes))
	}

	graph.Events = []watch.Event{
		{Time: time.Now().Add(-5 * time.Minute), Op: "WRITE", Path: "main.go", Delta: 4, Dirty: true},
		{Time: time.Now().Add(-2 * time.Minute), Op: "CREATE", Path: "pkg/types.go", Delta: 7},
		{Time: time.Now().Add(-time.Minute), Op: "WRITE", Path: "main.go", Delta: -1, Dirty: true},
	}

	activityRes, _, err := handleGetActivity(context.Background(), nil, WatchActivityInput{Path: activityRoot, Minutes: 60})
	if err != nil {
		t.Fatalf("handleGetActivity activity error: %v", err)
	}
	activityOut := resultText(t, activityRes)
	for _, check := range []string{"HOT FILES", "main.go", "SESSION SUMMARY", "RECENT TIMELINE"} {
		if !strings.Contains(activityOut, check) {
			t.Fatalf("expected %q in activity output, got:\n%s", check, activityOut)
		}
	}

	stopRes, _, err = handleStopWatch(context.Background(), nil, WatchInput{Path: activityRoot})
	if err != nil {
		t.Fatalf("handleStopWatch error: %v", err)
	}
	if !strings.Contains(resultText(t, stopRes), "Watcher stopped for:") {
		t.Fatalf("unexpected stop output:\n%s", resultText(t, stopRes))
	}

	missingRes, _, err := handleStopWatch(context.Background(), nil, WatchInput{Path: activityRoot})
	if err != nil {
		t.Fatalf("handleStopWatch missing error: %v", err)
	}
	if !strings.Contains(resultText(t, missingRes), "No active watcher for:") {
		t.Fatalf("unexpected missing stop output:\n%s", resultText(t, missingRes))
	}

	afterStopRes, _, err := handleGetActivity(context.Background(), nil, WatchActivityInput{Path: activityRoot})
	if err != nil {
		t.Fatalf("handleGetActivity after stop error: %v", err)
	}
	if !afterStopRes.IsError || !strings.Contains(resultText(t, afterStopRes), "Use start_watch first.") {
		t.Fatalf("expected missing watcher error, got:\n%s", resultText(t, afterStopRes))
	}
}

func TestHandleGraphContextHandlers(t *testing.T) {
	if !scanner.NewAstGrepAnalyzer().Available() {
		t.Skip("ast-grep not available")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	writeMCPImportersFixture(t, root)

	importersRes, _, err := handleGetImporters(context.Background(), nil, ImportersInput{
		Path: root,
		File: "pkg/types/types.go",
	})
	if err != nil {
		t.Fatalf("handleGetImporters error: %v", err)
	}
	importersOut := resultText(t, importersRes)
	if !strings.Contains(importersOut, "3 files import 'pkg/types/types.go'") || !strings.Contains(importersOut, "HUB FILE") {
		t.Fatalf("unexpected importers output:\n%s", importersOut)
	}

	hubsRes, _, err := handleGetHubs(context.Background(), nil, PathInput{Path: root})
	if err != nil {
		t.Fatalf("handleGetHubs error: %v", err)
	}
	hubsOut := resultText(t, hubsRes)
	if !strings.Contains(hubsOut, "Hub Files") || !strings.Contains(hubsOut, "pkg/types/types.go") {
		t.Fatalf("unexpected hubs output:\n%s", hubsOut)
	}

	ctxRes, _, err := handleGetFileContext(context.Background(), nil, ImportersInput{
		Path: root,
		File: "pkg/types/types.go",
	})
	if err != nil {
		t.Fatalf("handleGetFileContext error: %v", err)
	}
	ctxOut := resultText(t, ctxRes)
	for _, check := range []string{"HUB FILE", "IMPORTED BY (3 files)", "CONNECTED:"} {
		if !strings.Contains(ctxOut, check) {
			t.Fatalf("expected %q in file context output, got:\n%s", check, ctxOut)
		}
	}
}
