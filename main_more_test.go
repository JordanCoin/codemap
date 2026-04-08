package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"codemap/config"
	"codemap/handoff"
	"codemap/scanner"
	"codemap/watch"
)

type fakeWatchProcess struct {
	startErr  error
	started   bool
	stopped   bool
	fileCount int
	events    []watch.Event
}

func (f *fakeWatchProcess) Start() error {
	f.started = true
	return f.startErr
}

func (f *fakeWatchProcess) Stop() {
	f.stopped = true
}

func (f *fakeWatchProcess) FileCount() int {
	return f.fileCount
}

func (f *fakeWatchProcess) GetEvents(limit int) []watch.Event {
	if limit <= 0 || len(f.events) <= limit {
		return append([]watch.Event(nil), f.events...)
	}
	return append([]watch.Event(nil), f.events[len(f.events)-limit:]...)
}

func withMainRuntimeStubs(
	t *testing.T,
	watchFactory func(root string, verbose bool) (watchProcess, error),
	signalNotifier func(c chan<- os.Signal, sig ...os.Signal),
	cmdFactory func(name string, args ...string) *exec.Cmd,
	exePath func() (string, error),
	isRunning func(string) bool,
	stopWatch func(string) error,
	terminal func(*os.File) bool,
) {
	t.Helper()

	prevWatchFactory := newWatchProcess
	prevNotifier := notifySignals
	prevCmdFactory := execCommand
	prevExePath := executablePath
	prevIsRunning := watchIsRunning
	prevStopWatch := stopWatchDaemon
	prevTerminal := terminalChecker

	if watchFactory != nil {
		newWatchProcess = watchFactory
	}
	if signalNotifier != nil {
		notifySignals = signalNotifier
	}
	if cmdFactory != nil {
		execCommand = cmdFactory
	}
	if exePath != nil {
		executablePath = exePath
	}
	if isRunning != nil {
		watchIsRunning = isRunning
	}
	if stopWatch != nil {
		stopWatchDaemon = stopWatch
	}
	if terminal != nil {
		terminalChecker = terminal
	}

	t.Cleanup(func() {
		newWatchProcess = prevWatchFactory
		notifySignals = prevNotifier
		execCommand = prevCmdFactory
		executablePath = prevExePath
		watchIsRunning = prevIsRunning
		stopWatchDaemon = prevStopWatch
		terminalChecker = prevTerminal
	})
}

func captureMainStreams(t *testing.T, fn func()) (string, string) {
	t.Helper()

	oldOut := os.Stdout
	oldErr := os.Stderr
	outFile, err := os.CreateTemp("", "codemap-stdout-*")
	if err != nil {
		t.Fatal(err)
	}
	errFile, err := os.CreateTemp("", "codemap-stderr-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Remove(outFile.Name())
		_ = os.Remove(errFile.Name())
	}()

	func() {
		defer func() {
			_ = outFile.Close()
			_ = errFile.Close()
			os.Stdout = oldOut
			os.Stderr = oldErr
		}()
		os.Stdout = outFile
		os.Stderr = errFile
		fn()
	}()

	stdout, err := os.ReadFile(outFile.Name())
	if err != nil {
		t.Fatalf("read stdout capture: %v", err)
	}
	stderr, err := os.ReadFile(errFile.Name())
	if err != nil {
		t.Fatalf("read stderr capture: %v", err)
	}
	return string(stdout), string(stderr)
}

func runCodemapWithInput(input string, args ...string) (string, string, error) {
	cmd := exec.Command(codemapTestBinaryPath, args...)
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

func runGitMainTestCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
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

func writeImportersFixture(t *testing.T, root string) {
	t.Helper()

	files := map[string]string{
		"go.mod":             "module example.com/demo\n\ngo 1.22\n",
		"pkg/types/types.go": "package types\n\ntype Item struct{}\n",
		"a/a.go":             "package a\n\nimport _ \"example.com/demo/pkg/types\"\n",
		"b/b.go":             "package b\n\nimport _ \"example.com/demo/pkg/types\"\n",
		"c/c.go":             "package c\n\nimport _ \"example.com/demo/pkg/types\"\n",
		"main.go":            "package main\n\nimport _ \"example.com/demo/pkg/types\"\n",
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

func makeMainGitRepo(t *testing.T, branch string) string {
	t.Helper()

	root := t.TempDir()
	writeImportersFixture(t, root)
	runGitMainTestCmd(t, root, "init")
	runGitMainTestCmd(t, root, "add", ".")
	runGitMainTestCmd(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")
	runGitMainTestCmd(t, root, "branch", "-M", branch)
	return root
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
		runImportersMode(root, filepath.Join(root, "pkg", "types", "types.go"), false)
	})

	for _, check := range []string{"HUB FILE: pkg/types/types.go", "Imported by 4 files", "Dependents:"} {
		if !strings.Contains(stdout, check) {
			t.Fatalf("expected %q in output, got:\n%s", check, stdout)
		}
	}
}

func TestRunDepsModeJSONAndMainDispatchesDepsAndImporters(t *testing.T) {
	if !scanner.NewAstGrepAnalyzer().Available() {
		t.Skip("ast-grep not available")
	}

	root := t.TempDir()
	writeImportersFixture(t, root)

	stdout, _ := captureMainStreams(t, func() {
		runDepsMode(root, root, true, "main", map[string]bool{"a/a.go": true}, false)
	})

	var depsProject scanner.DepsProject
	if err := json.Unmarshal([]byte(stdout), &depsProject); err != nil {
		t.Fatalf("expected deps JSON output, got error %v with body:\n%s", err, stdout)
	}
	if depsProject.Mode != "deps" {
		t.Fatalf("deps mode = %q, want deps", depsProject.Mode)
	}
	if depsProject.DiffRef != "main" {
		t.Fatalf("deps diff_ref = %q, want main", depsProject.DiffRef)
	}
	if len(depsProject.Files) != 1 || depsProject.Files[0].Path != "a/a.go" {
		t.Fatalf("expected diff filter to keep only a/a.go, got %+v", depsProject.Files)
	}

	stdout = runMainWithArgs(t, []string{"codemap", "--deps", "--json", root})
	if err := json.Unmarshal([]byte(stdout), &depsProject); err != nil {
		t.Fatalf("expected main deps JSON output, got error %v with body:\n%s", err, stdout)
	}
	if depsProject.Mode != "deps" || len(depsProject.Files) == 0 {
		t.Fatalf("expected deps project output, got %+v", depsProject)
	}

	stdout = runMainWithArgs(t, []string{"codemap", "--importers", "main.go", root})
	if !strings.Contains(stdout, "File: main.go") {
		t.Fatalf("expected importers output for main.go, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Imports 1 hub(s): pkg/types/types.go") {
		t.Fatalf("expected hub import summary for main.go, got:\n%s", stdout)
	}

	stdout = runMainWithArgs(t, []string{"codemap", "--json", "--importers", "main.go", root})
	var importersReport scanner.ImportersReport
	if err := json.Unmarshal([]byte(stdout), &importersReport); err != nil {
		t.Fatalf("expected importers JSON output, got error %v with body:\n%s", err, stdout)
	}
	if importersReport.Mode != "importers" || importersReport.File != "main.go" {
		t.Fatalf("expected importers report for main.go, got %+v", importersReport)
	}
	if len(importersReport.Importers) != 0 {
		t.Fatalf("expected main.go to have no importers in fixture, got %+v", importersReport.Importers)
	}
	if len(importersReport.HubImports) != 1 || importersReport.HubImports[0] != "pkg/types/types.go" {
		t.Fatalf("expected hub import summary in JSON, got %+v", importersReport.HubImports)
	}
}

func TestRunHandoffSubcommandBuildAndDetailJSON(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := makeMainGitRepo(t, "feature/handoff-main")
	if err := os.WriteFile(filepath.Join(root, "pkg", "types", "types.go"), []byte("package types\n\ntype Item struct{ Value string }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeMainWatchState(t, root, watch.State{
		UpdatedAt: time.Now(),
		FileCount: 5,
		Importers: map[string][]string{
			"pkg/types/types.go": {"a/a.go", "b/b.go", "c/c.go", "main.go"},
		},
		Imports: map[string][]string{
			"main.go": {"pkg/types/types.go"},
		},
		RecentEvents: []watch.Event{
			{Time: time.Now().Add(-time.Minute), Op: "WRITE", Path: "pkg/types/types.go", Delta: 2, IsHub: true},
		},
	}, false)

	stdout, _ := captureMainStreams(t, func() {
		runHandoffSubcommand([]string{"--json", "--no-save", root})
	})

	var artifact handoff.Artifact
	if err := json.Unmarshal([]byte(stdout), &artifact); err != nil {
		t.Fatalf("expected handoff JSON output, got error %v with body:\n%s", err, stdout)
	}
	if artifact.Branch != "feature/handoff-main" {
		t.Fatalf("artifact branch = %q, want feature/handoff-main", artifact.Branch)
	}
	if len(artifact.Delta.Changed) == 0 || artifact.Delta.Changed[0].Path != "pkg/types/types.go" {
		t.Fatalf("expected changed type file in artifact, got %+v", artifact.Delta.Changed)
	}
	if _, err := os.Stat(handoff.LatestPath(root)); !os.IsNotExist(err) {
		t.Fatalf("expected --no-save to skip latest artifact write, got err=%v", err)
	}

	stdout, _ = captureMainStreams(t, func() {
		runHandoffSubcommand([]string{root})
	})
	for _, check := range []string{"# Handoff", "Saved:", "Prefix:", "Delta:", "Metrics:"} {
		if !strings.Contains(stdout, check) {
			t.Fatalf("expected %q in handoff output, got:\n%s", check, stdout)
		}
	}

	stdout, _ = captureMainStreams(t, func() {
		runHandoffSubcommand([]string{"--latest", "--detail", "pkg/types/types.go", "--json", root})
	})

	var detail handoff.FileDetail
	if err := json.Unmarshal([]byte(stdout), &detail); err != nil {
		t.Fatalf("expected handoff detail JSON output, got error %v with body:\n%s", err, stdout)
	}
	if detail.Path != "pkg/types/types.go" {
		t.Fatalf("detail path = %q, want pkg/types/types.go", detail.Path)
	}
	if len(detail.Importers) != 4 {
		t.Fatalf("expected 4 importers in detail, got %+v", detail.Importers)
	}
}

func TestRunWatchModeRunDaemonAndWatchStart(t *testing.T) {
	t.Run("watch mode prints summary after interrupt", func(t *testing.T) {
		fake := &fakeWatchProcess{
			fileCount: 7,
			events: []watch.Event{
				{Path: "main.go", Op: "WRITE"},
				{Path: "pkg/types.go", Op: "CREATE"},
			},
		}
		withMainRuntimeStubs(
			t,
			func(root string, verbose bool) (watchProcess, error) { return fake, nil },
			func(c chan<- os.Signal, sig ...os.Signal) { c <- os.Interrupt },
			nil,
			nil,
			nil,
			nil,
			nil,
		)

		stdout, _ := captureMainStreams(t, func() { runWatchMode(t.TempDir(), false) })
		for _, check := range []string{"codemap watch - Live code graph daemon", "Watching:", "Press Ctrl+C to stop", "Session summary:", "Files tracked: 7", "Events logged: 2"} {
			if !strings.Contains(stdout, check) {
				t.Fatalf("expected %q in watch mode output, got:\n%s", check, stdout)
			}
		}
		if !fake.started || !fake.stopped {
			t.Fatalf("expected fake watch process to start and stop, got %+v", fake)
		}
	})

	t.Run("daemon writes and removes pid around lifecycle", func(t *testing.T) {
		fake := &fakeWatchProcess{}
		root := t.TempDir()
		withMainRuntimeStubs(
			t,
			func(root string, verbose bool) (watchProcess, error) { return fake, nil },
			func(c chan<- os.Signal, sig ...os.Signal) { c <- syscall.SIGTERM },
			nil,
			nil,
			nil,
			nil,
			nil,
		)

		runDaemon(root)
		if !fake.started || !fake.stopped {
			t.Fatalf("expected fake daemon to start and stop, got %+v", fake)
		}
		if _, err := os.Stat(filepath.Join(root, ".codemap", "watch.pid")); !os.IsNotExist(err) {
			t.Fatalf("expected pid file to be removed after daemon stops, got err=%v", err)
		}
	})

	t.Run("watch start shells out to daemon entrypoint", func(t *testing.T) {
		root := t.TempDir()
		var gotName string
		var gotArgs []string
		withMainRuntimeStubs(
			t,
			nil,
			nil,
			func(name string, args ...string) *exec.Cmd {
				gotName = name
				gotArgs = append([]string(nil), args...)
				return exec.Command("sh", "-c", "exit 0")
			},
			func() (string, error) { return "/tmp/codemap-test", nil },
			func(string) bool { return false },
			nil,
			nil,
		)

		stdout, _ := captureMainStreams(t, func() { runWatchSubcommand("start", root) })
		if gotName != "/tmp/codemap-test" {
			t.Fatalf("watch start executable = %q, want /tmp/codemap-test", gotName)
		}
		absRoot, _ := filepath.Abs(root)
		wantArgs := []string{"watch", "daemon", absRoot}
		if strings.Join(gotArgs, "|") != strings.Join(wantArgs, "|") {
			t.Fatalf("watch start args = %v, want %v", gotArgs, wantArgs)
		}
		if !strings.Contains(stdout, "Watch daemon started (pid ") {
			t.Fatalf("expected start output, got:\n%s", stdout)
		}
	})
}

func TestCloneRepoUsesCommandAndCleansUpOnFailure(t *testing.T) {
	withMainRuntimeStubs(
		t,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		func(*os.File) bool { return false },
	)

	t.Run("success", func(t *testing.T) {
		var gotName string
		var gotArgs []string
		withMainRuntimeStubs(
			t,
			nil,
			nil,
			func(name string, args ...string) *exec.Cmd {
				gotName = name
				gotArgs = append([]string(nil), args...)
				dest := args[len(args)-1]
				return exec.Command("sh", "-c", `mkdir -p "$1/.git"; echo ok > "$1/README.md"`, "sh", dest)
			},
			nil,
			nil,
			nil,
			func(*os.File) bool { return false },
		)

		dir, err := cloneRepo("github.com/acme/codemap", "acme/codemap")
		if err != nil {
			t.Fatalf("cloneRepo() error: %v", err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		if gotName != "git" {
			t.Fatalf("cloneRepo command = %q, want git", gotName)
		}
		wantPrefix := []string{"clone", "--depth", "1", "--single-branch", "-q", "https://github.com/acme/codemap"}
		if strings.Join(gotArgs[:len(wantPrefix)], "|") != strings.Join(wantPrefix, "|") {
			t.Fatalf("cloneRepo args = %v, want prefix %v", gotArgs, wantPrefix)
		}
		if _, err := os.Stat(filepath.Join(dir, "README.md")); err != nil {
			t.Fatalf("expected cloned README to exist: %v", err)
		}
	})

	t.Run("failure removes temp dir", func(t *testing.T) {
		var failedDest string
		withMainRuntimeStubs(
			t,
			nil,
			nil,
			func(name string, args ...string) *exec.Cmd {
				failedDest = args[len(args)-1]
				return exec.Command("sh", "-c", "exit 1")
			},
			nil,
			nil,
			nil,
			func(*os.File) bool { return false },
		)

		dir, err := cloneRepo("gitlab.com/acme/codemap", "acme/codemap")
		if err == nil {
			t.Fatal("expected cloneRepo failure")
		}
		if dir != "" {
			t.Fatalf("expected empty dir on clone failure, got %q", dir)
		}
		if _, statErr := os.Stat(failedDest); !os.IsNotExist(statErr) {
			t.Fatalf("expected failed clone temp dir to be removed, got err=%v", statErr)
		}
	})
}

func TestMainWatchCloneAndDiffModes(t *testing.T) {
	t.Run("watch flag dispatches to watch mode", func(t *testing.T) {
		fake := &fakeWatchProcess{fileCount: 3, events: []watch.Event{{Path: "main.go", Op: "WRITE"}}}
		withMainRuntimeStubs(
			t,
			func(root string, verbose bool) (watchProcess, error) { return fake, nil },
			func(c chan<- os.Signal, sig ...os.Signal) { c <- os.Interrupt },
			nil,
			nil,
			nil,
			nil,
			nil,
		)

		stdout := runMainWithArgs(t, []string{"codemap", "--watch", t.TempDir()})
		if !strings.Contains(stdout, "codemap watch - Live code graph daemon") || !strings.Contains(stdout, "Events logged: 1") {
			t.Fatalf("expected watch mode output, got:\n%s", stdout)
		}
	})

	t.Run("github url path clones and renders json project", func(t *testing.T) {
		withMainRuntimeStubs(
			t,
			nil,
			nil,
			func(name string, args ...string) *exec.Cmd {
				dest := args[len(args)-1]
				return exec.Command("sh", "-c", `mkdir -p "$1"; printf 'package main\n' > "$1/main.go"`, "sh", dest)
			},
			nil,
			nil,
			nil,
			func(*os.File) bool { return false },
		)

		stdout := runMainWithArgs(t, []string{"codemap", "--json", "github.com/acme/codemap"})
		var project scanner.Project
		if err := json.Unmarshal([]byte(stdout), &project); err != nil {
			t.Fatalf("expected cloned project JSON output, got error %v with body:\n%s", err, stdout)
		}
		if project.Name != "acme/codemap" {
			t.Fatalf("project name = %q, want acme/codemap", project.Name)
		}
		if project.RemoteURL != "github.com/acme/codemap" {
			t.Fatalf("project remote URL = %q, want github.com/acme/codemap", project.RemoteURL)
		}
		if len(project.Files) != 1 || project.Files[0].Path != "main.go" {
			t.Fatalf("expected cloned project files to include main.go, got %+v", project.Files)
		}
	})

	t.Run("diff json includes changed file annotations", func(t *testing.T) {
		if _, err := exec.LookPath("git"); err != nil {
			t.Skip("git not available")
		}

		root := makeMainGitRepo(t, "main")
		if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc changed() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		stdout := runMainWithArgs(t, []string{"codemap", "--json", "--diff", "--ref", "HEAD", root})
		var project scanner.Project
		if err := json.Unmarshal([]byte(stdout), &project); err != nil {
			t.Fatalf("expected diff project JSON output, got error %v with body:\n%s", err, stdout)
		}
		if project.DiffRef != "HEAD" {
			t.Fatalf("project diff_ref = %q, want HEAD", project.DiffRef)
		}
		if len(project.Files) != 1 || project.Files[0].Path != "main.go" {
			t.Fatalf("expected only changed main.go in diff output, got %+v", project.Files)
		}
		if project.Files[0].Added == 0 && project.Files[0].Removed == 0 {
			t.Fatalf("expected diff annotations on changed file, got %+v", project.Files[0])
		}
	})
}

func TestRunDepsModeRenderedOutputAndMainTreeModes(t *testing.T) {
	if !scanner.NewAstGrepAnalyzer().Available() {
		t.Skip("ast-grep not available")
	}

	root := t.TempDir()
	writeImportersFixture(t, root)

	stdout, _ := captureMainStreams(t, func() {
		runDepsMode(root, root, false, "main", nil, false)
	})
	if !strings.Contains(stdout, "Dependency Flow") {
		t.Fatalf("expected rendered dependency graph output, got:\n%s", stdout)
	}

	stdout = runMainWithArgs(t, []string{"codemap", root})
	if !strings.Contains(stdout, "Files:") {
		t.Fatalf("expected tree mode output, got:\n%s", stdout)
	}

	stdout = runMainWithArgs(t, []string{"codemap", "--skyline", root})
	if strings.TrimSpace(stdout) == "" {
		t.Fatal("expected skyline output")
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
