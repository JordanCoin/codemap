package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"codemap/analysis"
	"codemap/config"
	"codemap/handoff"
	"codemap/internal/projectpath"
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

func TestApplyGlobalRootOptions(t *testing.T) {
	launchDir := t.TempDir()
	projectRoot := filepath.Join(launchDir, "worktree")
	setupRoot := filepath.Join(launchDir, "original")
	for _, root := range []string{projectRoot, setupRoot} {
		if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	projectNested := filepath.Join(projectRoot, "pkg")
	setupNested := filepath.Join(setupRoot, "cmd")
	for _, dir := range []string{projectNested, setupNested} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(launchDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDir) })
	projectpath.ResetSetupRoot()
	t.Cleanup(projectpath.ResetSetupRoot)

	args, projectRootExplicit, err := applyGlobalRootOptions([]string{
		"context", "--project-root", projectNested,
		"--setup-root", setupNested, "--compact",
	})
	if err != nil {
		t.Fatalf("applyGlobalRootOptions() error: %v", err)
	}
	if got := strings.Join(args, "|"); got != "context|--compact" {
		t.Fatalf("args = %q, want context|--compact", got)
	}
	if !projectRootExplicit {
		t.Fatal("expected explicit project root")
	}
	gotDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	wantDir, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if gotDir != wantDir {
		t.Fatalf("working directory = %q, want %q", gotDir, wantDir)
	}
	wantSetup, err := filepath.EvalSymlinks(setupRoot)
	if err != nil {
		t.Fatal(err)
	}
	if got := projectpath.SetupRoot(projectRoot); got != wantSetup {
		t.Fatalf("setup root = %q, want %q", got, wantSetup)
	}
}

func TestApplyGlobalRootOptionsDirectoryKeepsAutomaticLinkedSelection(t *testing.T) {
	launchDir := t.TempDir()
	primary := filepath.Join(launchDir, "primary")
	gitDir := filepath.Join(primary, ".git", "worktrees", "agent")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(primary, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(launchDir, "linked")
	if err := os.MkdirAll(filepath.Join(linked, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(linked, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDir) })
	projectpath.ResetSetupRoot()
	t.Cleanup(projectpath.ResetSetupRoot)

	if _, _, err := applyGlobalRootOptions([]string{"-C", filepath.Join(linked, "pkg"), "context"}); err != nil {
		t.Fatalf("applyGlobalRootOptions() error: %v", err)
	}
	if got := projectpath.ConfiguredSetupRoot(); got != "" {
		t.Fatalf("ConfiguredSetupRoot() = %q, want no explicit override", got)
	}
	selection, err := projectpath.Select(linked)
	if err != nil {
		t.Fatalf("Select() error: %v", err)
	}
	primary, _ = filepath.EvalSymlinks(primary)
	if selection.SetupRoot != primary || selection.Source != projectpath.SourceLinkedWorktree {
		t.Fatalf("Select() = %#v, want automatic setup %q", selection, primary)
	}
}

func TestApplyGlobalRootOptionsRejectsMalformedLinkedMetadataWithoutFlags(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("not a gitdir\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDir) })

	if _, _, err := applyGlobalRootOptions([]string{"context"}); err == nil || !strings.Contains(err.Error(), "resolve linked worktree setup") {
		t.Fatalf("applyGlobalRootOptions() error = %v, want bounded linked-worktree error", err)
	}
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

func TestMainWatchHelperProcess(t *testing.T) {
	if os.Getenv("CODEMAP_MAIN_WATCH_HELPER") != "1" {
		return
	}
	time.Sleep(time.Minute)
}

func TestRunWatchStartWaitsForChildReadinessFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	withMainRuntimeStubs(t, nil, nil, func(string, ...string) *exec.Cmd {
		return exec.Command("sh", "-c", `printf '{"error":"claim rejected"}' > "$CODEMAP_WATCH_READINESS_FILE"`)
	}, func() (string, error) { return os.Args[0], nil }, nil, nil, nil)

	err := runWatchSubcommand("start", root)
	if err == nil || !strings.Contains(err.Error(), "claim rejected") {
		t.Fatalf("runWatchSubcommand(start) error = %v, want child readiness failure", err)
	}
	if _, statErr := os.Stat(filepath.Join(projectpath.ProjectRuntimeDir(root), "watch.pid")); !os.IsNotExist(statErr) {
		t.Fatalf("readiness failure left daemon PID behind: %v", statErr)
	}
}

func TestRunDaemonPublishesInitializationFailure(t *testing.T) {
	root := t.TempDir()
	readyPath := filepath.Join(t.TempDir(), "ready.json")
	t.Setenv(watchReadinessEnv, readyPath)
	withMainRuntimeStubs(t, func(string, bool) (watchProcess, error) {
		return &fakeWatchProcess{startErr: fmt.Errorf("watch init rejected")}, nil
	}, nil, nil, nil, nil, nil, nil)

	if err := runDaemon(root); err == nil || !strings.Contains(err.Error(), "watch init rejected") {
		t.Fatalf("runDaemon() error = %v, want initialization failure", err)
	}
	if err := waitWatchReadiness(readyPath, time.Second); err == nil || !strings.Contains(err.Error(), "watch init rejected") {
		t.Fatalf("waitWatchReadiness() error = %v, want published initialization failure", err)
	}
}

func TestRunDaemonPublishesTransitionReleaseFailure(t *testing.T) {
	root := t.TempDir()
	readyPath := filepath.Join(t.TempDir(), "ready.json")
	t.Setenv(watchReadinessEnv, readyPath)
	previousRelease := releaseWatchTransition
	releaseWatchTransition = func(*watch.Transition) error { return errors.New("release rejected") }
	t.Cleanup(func() { releaseWatchTransition = previousRelease })
	withMainRuntimeStubs(t, func(string, bool) (watchProcess, error) {
		return &fakeWatchProcess{}, nil
	}, nil, nil, nil, nil, nil, nil)

	if err := runDaemon(root); err == nil || !strings.Contains(err.Error(), "release rejected") {
		t.Fatalf("runDaemon() error = %v, want transition release failure", err)
	}
	if err := waitWatchReadiness(readyPath, time.Second); err == nil || !strings.Contains(err.Error(), "release rejected") {
		t.Fatalf("waitWatchReadiness() error = %v, want published release failure", err)
	}
}

func writeMainWatchState(t *testing.T, root string, state watch.State, running bool) {
	t.Helper()

	if err := os.MkdirAll(projectpath.ProjectRuntimeDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectpath.ProjectRuntimeDir(root), "state.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if running {
		canonical, err := filepath.EvalSymlinks(root)
		if err != nil {
			t.Fatal(err)
		}
		process := exec.Command(os.Args[0], "-test.run=TestMainWatchHelperProcess", "--", "watch", "daemon", canonical)
		process.Env = append(os.Environ(), "CODEMAP_MAIN_WATCH_HELPER=1")
		if err := process.Start(); err != nil {
			t.Fatal(err)
		}
		if err := watch.WriteProcessPID(root, process.Process.Pid); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = process.Process.Kill()
			_, _ = process.Process.Wait()
			watch.RemovePID(root)
		})
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

func writeCLIFilterPrecedenceFixture(t *testing.T, root string) {
	t.Helper()

	files := map[string]string{
		"go.mod":               "module example.com/precedence\n\ngo 1.22\n",
		"pkg/shared/shared.go": "package shared\n\nfunc Value() int { return 1 }\n",
		"a/a.go":               "package a\n\nimport \"example.com/precedence/pkg/shared\"\n\nfunc Use() int { return shared.Value() }\n",
		"c/c.go":               "package c\n\nimport \"example.com/precedence/pkg/shared\"\n\nfunc Use() int { return shared.Value() }\n",
		"ts/ignored.ts":        "import { Value } from '../pkg/shared/shared';\n\nexport const use = Value;\n",
		".codemap/config.json": "{\n  \"only\": [\"ts\"],\n  \"exclude\": [\"a\"]\n}\n",
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

func analysisPaths(analyses []scanner.FileAnalysis) []string {
	paths := make([]string, 0, len(analyses))
	for _, analysis := range analyses {
		paths = append(paths, filepath.ToSlash(analysis.Path))
	}
	return paths
}

func requirePaths(t *testing.T, paths []string, present, absent []string) {
	t.Helper()
	joined := "\n" + strings.Join(paths, "\n") + "\n"
	for _, path := range present {
		if !strings.Contains(joined, "\n"+path+"\n") {
			t.Fatalf("expected %q in paths %v", path, paths)
		}
	}
	for _, path := range absent {
		if strings.Contains(joined, "\n"+path+"\n") {
			t.Fatalf("did not expect %q in paths %v", path, paths)
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

func TestRunWatchSubcommandUsesNearestGitRootFromNestedDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeMainWatchState(t, root, watch.State{
		UpdatedAt: time.Now(),
		FileCount: 4,
		Hubs:      []string{"pkg/types.go"},
	}, true)

	nested := filepath.Join(root, "pkg", "feature")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	stdout, _ := captureMainStreams(t, func() { runWatchSubcommand("status", nested) })
	for _, check := range []string{"Watch daemon running", "Files: 4", "Hubs: 1"} {
		if !strings.Contains(stdout, check) {
			t.Fatalf("expected %q in nested watch status output, got:\n%s", check, stdout)
		}
	}
}

func TestRunWatchSubcommandReturnsRuntimeRejection(t *testing.T) {
	root := t.TempDir()
	setup := t.TempDir()
	projectpath.SetSetupRoot(setup)
	t.Cleanup(projectpath.ResetSetupRoot)
	selection, err := projectpath.SelectRuntime(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(selection.RuntimeDir, "project.json"), []byte(`{"canonical_root":"/other"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runWatchSubcommand("start", root); err == nil {
		t.Fatal("watch start accepted a rejected runtime identity")
	}
}

func TestRunWatchStartPreservesSetupRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	setupRoot := t.TempDir()
	projectpath.SetSetupRoot(setupRoot)
	t.Cleanup(projectpath.ResetSetupRoot)

	var gotArgs []string
	withMainRuntimeStubs(
		t,
		nil,
		nil,
		func(_ string, args ...string) *exec.Cmd {
			gotArgs = append([]string(nil), args...)
			return exec.Command("sh", "-c", `printf '{}' > "$CODEMAP_WATCH_READINESS_FILE"`)
		},
		func() (string, error) { return "/tmp/codemap", nil },
		func(string) bool { return false },
		nil,
		nil,
	)

	captureMainStreams(t, func() { runWatchSubcommand("start", root) })
	wantRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--setup-root", setupRoot, "watch", "daemon", wantRoot}
	if strings.Join(gotArgs, "|") != strings.Join(want, "|") {
		t.Fatalf("watch daemon args = %v, want %v", gotArgs, want)
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
		runImportersMode(root, filepath.Join(root, "pkg", "types", "types.go"), false, scanner.Filters{})
	})

	for _, check := range []string{"HUB FILE: pkg/types/types.go", "Imported by 4 files", "Dependents:"} {
		if !strings.Contains(stdout, check) {
			t.Fatalf("expected %q in output, got:\n%s", check, stdout)
		}
	}
}

func TestResolveImportersProjectRoot(t *testing.T) {
	repo := makeMainGitRepo(t, "main")
	absoluteFile := filepath.Join(repo, "main.go")
	canonicalRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("infers repository from absolute importer", func(t *testing.T) {
		gotRoot, gotFile, err := resolveImportersInvocation("", absoluteFile, false)
		if err != nil {
			t.Fatalf("resolveImportersInvocation() error: %v", err)
		}
		if gotRoot != canonicalRepo {
			t.Fatalf("root = %q, want %q", gotRoot, canonicalRepo)
		}
		canonicalFile, err := filepath.EvalSymlinks(absoluteFile)
		if err != nil {
			t.Fatal(err)
		}
		if gotFile != canonicalFile {
			t.Fatalf("file = %q, want %q", gotFile, canonicalFile)
		}
	})

	t.Run("explicit roots win", func(t *testing.T) {
		for _, test := range []struct {
			name                string
			positionalRoot      string
			projectRootExplicit bool
			want                string
		}{
			{name: "positional", positionalRoot: "/explicit/positional", want: "/explicit/positional"},
			{name: "project flag", projectRootExplicit: true, want: ""},
		} {
			t.Run(test.name, func(t *testing.T) {
				gotRoot, gotFile, err := resolveImportersInvocation(test.positionalRoot, absoluteFile, test.projectRootExplicit)
				if err != nil {
					t.Fatalf("resolveImportersInvocation() error: %v", err)
				}
				if gotRoot != test.want {
					t.Fatalf("root = %q, want %q", gotRoot, test.want)
				}
				// The explicit root wins, but the absolute importer is still
				// canonicalized so its relative path matches the canonical
				// project root (e.g. macOS /tmp -> /private/tmp).
				canonicalFile, err := filepath.EvalSymlinks(absoluteFile)
				if err != nil {
					t.Fatal(err)
				}
				if gotFile != canonicalFile {
					t.Fatalf("file = %q, want canonical %q", gotFile, canonicalFile)
				}
			})
		}
	})

	t.Run("absolute importer outside repository fails", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "outside.go")
		if err := os.WriteFile(file, []byte("package outside\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := resolveImportersInvocation("", file, false); err == nil ||
			!strings.Contains(err.Error(), "is not inside a Git repository") {
			t.Fatalf("resolveImportersInvocation() error = %v, want repository error", err)
		}
	})
}

func TestAbsoluteImporterInfersProjectRootFromUnrelatedDirectory(t *testing.T) {
	if !scanner.NewAstGrepAnalyzer().Available() {
		t.Skip("ast-grep not available")
	}

	repo := makeMainGitRepo(t, "main")
	canonicalRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	out, err := runRootOptionsBinary(
		t.TempDir(),
		"--json",
		"--importers", filepath.Join(repo, "main.go"),
	)
	if err != nil {
		t.Fatalf("codemap failed: %v\n%s", err, out)
	}

	var report scanner.ImportersReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("decode importers report: %v\n%s", err, out)
	}
	if report.Root != canonicalRepo {
		t.Fatalf("report root = %q, want %q", report.Root, canonicalRepo)
	}
	if report.File != "main.go" {
		t.Fatalf("report file = %q, want main.go", report.File)
	}
}

// TestExplicitRootImportersReportUsesCanonicalRelativePath ensures an
// absolute importer with a differently spelled root still reports a
// canonical root-relative path.
func TestExplicitRootImportersReportUsesCanonicalRelativePath(t *testing.T) {
	if !scanner.NewAstGrepAnalyzer().Available() {
		t.Skip("ast-grep not available")
	}

	repo := makeMainGitRepo(t, "main")
	canonicalRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	out, err := runRootOptionsBinary(
		t.TempDir(),
		"--json",
		"--importers", filepath.Join(repo, "main.go"),
		canonicalRepo,
	)
	if err != nil {
		t.Fatalf("codemap failed: %v\n%s", err, out)
	}

	var report scanner.ImportersReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("decode importers report: %v\n%s", err, out)
	}
	if report.Root != canonicalRepo {
		t.Fatalf("report root = %q, want %q", report.Root, canonicalRepo)
	}
	if report.File != "main.go" {
		t.Fatalf("report file = %q, want main.go (canonical-relative)", report.File)
	}
}

func TestExplicitRootImportersReportCanonicalizesSymlinkedRoot(t *testing.T) {
	if !scanner.NewAstGrepAnalyzer().Available() {
		t.Skip("ast-grep not available")
	}

	repo := makeMainGitRepo(t, "main")
	aliasParent := t.TempDir()
	alias := filepath.Join(aliasParent, "repo")
	if err := os.Symlink(repo, alias); err != nil {
		t.Fatal(err)
	}
	canonicalRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	out, err := runRootOptionsBinary(
		t.TempDir(),
		"--json",
		"--importers", filepath.Join(repo, "main.go"),
		alias,
	)
	if err != nil {
		t.Fatalf("codemap failed: %v\n%s", err, out)
	}

	var report scanner.ImportersReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("decode importers report: %v\n%s", err, out)
	}
	if report.Root != canonicalRepo || report.File != "main.go" {
		t.Fatalf("report = root %q, file %q; want root %q and main.go", report.Root, report.File, canonicalRepo)
	}
}

func TestRunDepsModeJSONAndMainDispatchesDepsAndImporters(t *testing.T) {
	if !scanner.NewAstGrepAnalyzer().Available() {
		t.Skip("ast-grep not available")
	}

	root := t.TempDir()
	writeImportersFixture(t, root)
	if err := os.WriteFile(filepath.Join(root, "lib.rs"), []byte("pub struct Value;\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, _ := captureMainStreams(t, func() {
		runDepsMode(root, root, true, "main", map[string]bool{"a/a.go": true}, false, scanner.Filters{})
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
	if depsProject.Mode != "deps" || len(depsProject.Files) == 0 || depsProject.Coverage.Status != analysis.CoveragePartial {
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

func TestBinaryDependencyModesHonorCLIFiltersOverConfig(t *testing.T) {
	if !scanner.NewAstGrepAnalyzer().Available() {
		t.Skip("ast-grep not available")
	}

	root := t.TempDir()
	writeCLIFilterPrecedenceFixture(t, root)

	tests := []struct {
		name  string
		input string
		args  []string
		check func(t *testing.T, output string)
	}{
		{
			name: "deps",
			args: []string{"--deps", "--json", "--only", "go", "--exclude", "c", root},
			check: func(t *testing.T, output string) {
				t.Helper()
				var project scanner.DepsProject
				if err := json.Unmarshal([]byte(output), &project); err != nil {
					t.Fatalf("decode deps JSON: %v\n%s", err, output)
				}
				requirePaths(t, analysisPaths(project.Files),
					[]string{"a/a.go", "pkg/shared/shared.go"},
					[]string{"c/c.go", "ts/ignored.ts"})
			},
		},
		{
			name: "importers",
			args: []string{"--importers", "pkg/shared/shared.go", "--json", "--only", "go", "--exclude", "c", root},
			check: func(t *testing.T, output string) {
				t.Helper()
				var report scanner.ImportersReport
				if err := json.Unmarshal([]byte(output), &report); err != nil {
					t.Fatalf("decode importers JSON: %v\n%s", err, output)
				}
				requirePaths(t, report.Importers, []string{"a/a.go"}, []string{"c/c.go", "ts/ignored.ts"})
			},
		},
		{
			name:  "deps stdin",
			input: `{"files":[{"path":"go/main.go","content":"package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(1) }\n"},{"path":"ts/ignored.ts","content":"import { ignored } from \"example\";\n\nexport const value = ignored;\n"}]}`,
			args:  []string{"--deps", "--stdin", "--json", "--only", "go"},
			check: func(t *testing.T, output string) {
				t.Helper()
				var project scanner.DepsProject
				if err := json.Unmarshal([]byte(output), &project); err != nil {
					t.Fatalf("decode stdin deps JSON: %v\n%s", err, output)
				}
				requirePaths(t, analysisPaths(project.Files), []string{"go/main.go"}, []string{"ts/ignored.ts"})
				if len(project.Files) != 1 || len(project.Files[0].Imports) == 0 {
					t.Fatalf("expected parsed Go import, got %+v", project.Files)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output, stderr, err := runCodemapWithInput(test.input, test.args...)
			if err != nil {
				t.Fatalf("codemap %s failed: %v\nstderr=%s", test.name, err, stderr)
			}
			test.check(t, output)
		})
	}
}

func TestRunDepsModeFromStdinHonorsFilters(t *testing.T) {
	if !scanner.NewAstGrepAnalyzer().Available() {
		t.Skip("ast-grep not available")
	}

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = reader
	t.Cleanup(func() {
		os.Stdin = oldStdin
		_ = reader.Close()
	})
	manifest := `{"files":[{"path":"go.mod","content":"module example.com/stdin\n\nrequire (\n\texample.com/external v1.0.0\n)\n"},{"path":"go/main.go","content":"package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(1) }\n"},{"path":"ts/ignored.ts","content":"import { ignored } from \"example\";\n\nexport const value = ignored;\n"}]}`
	if _, err := writer.WriteString(manifest); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	stdout, _ := captureMainStreams(t, func() {
		runDepsMode("stdin-root", "stdin-root", true, "main", nil, true, scanner.Filters{Only: []string{"go"}})
	})
	var project scanner.DepsProject
	if err := json.Unmarshal([]byte(stdout), &project); err != nil {
		t.Fatalf("decode stdin deps JSON: %v\n%s", err, stdout)
	}
	requirePaths(t, analysisPaths(project.Files), []string{"go/main.go"}, []string{"ts/ignored.ts"})
	if len(project.Files) != 1 || len(project.Files[0].Imports) == 0 {
		t.Fatalf("expected parsed Go import, got %+v", project.Files)
	}
	if !strings.Contains(strings.Join(project.ExternalDeps["go"], "\n"), "example.com/external") {
		t.Fatalf("expected manifest external dependency, got %+v", project.ExternalDeps)
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
		if _, err := os.Stat(filepath.Join(projectpath.ProjectRuntimeDir(root), "watch.pid")); !os.IsNotExist(err) {
			t.Fatalf("expected pid file to be removed after daemon stops, got err=%v", err)
		}
	})

	t.Run("watch start shells out to daemon entrypoint", func(t *testing.T) {
		root := t.TempDir()
		projectpath.ResetSetupRoot()
		t.Cleanup(projectpath.ResetSetupRoot)
		var gotName string
		var gotArgs []string
		withMainRuntimeStubs(
			t,
			nil,
			nil,
			func(name string, args ...string) *exec.Cmd {
				gotName = name
				gotArgs = append([]string(nil), args...)
				return exec.Command("sh", "-c", `printf '{}' > "$CODEMAP_WATCH_READINESS_FILE"`)
			},
			func() (string, error) { return "/tmp/codemap-test", nil },
			func(string) bool { return false },
			nil,
			nil,
		)

		var startErr error
		stdout, stderr := captureMainStreams(t, func() { startErr = runWatchSubcommand("start", root) })
		if startErr != nil {
			t.Fatalf("watch start failed: %v\nstderr:\n%s", startErr, stderr)
		}
		if gotName != "/tmp/codemap-test" {
			t.Fatalf("watch start executable = %q, want /tmp/codemap-test", gotName)
		}
		absRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			t.Fatal(err)
		}
		wantArgs := []string{"watch", "daemon", absRoot}
		if strings.Join(gotArgs, "|") != strings.Join(wantArgs, "|") {
			t.Fatalf("watch start args = %v, want %v", gotArgs, wantArgs)
		}
		if !strings.Contains(stdout, "Watch daemon started (pid ") {
			t.Fatalf("expected start output, got:\n%s", stdout)
		}
		if strings.Contains(stdout, "pid -1") {
			t.Fatalf("start output used released process PID: %s", stdout)
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
		runDepsMode(root, root, false, "main", nil, false, scanner.Filters{})
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

func TestRunWatchSubcommandStopForeignPID(t *testing.T) {
	root := t.TempDir()

	origRunning := watchIsRunning
	origStop := stopWatchDaemon
	defer func() {
		watchIsRunning = origRunning
		stopWatchDaemon = origStop
	}()
	watchIsRunning = func(string) bool { return true }
	stopWatchDaemon = func(string) error { return watch.ErrForeignDaemonPID }

	stdout, _ := captureMainStreams(t, func() { runWatchSubcommand("stop", root) })
	if !strings.Contains(stdout, "cleared stale PID file") {
		t.Fatalf("expected stale-PID message on foreign PID, got:\n%s", stdout)
	}
	if strings.Contains(stdout, "Watch daemon stopped") {
		t.Fatalf("should not claim the daemon was stopped for a foreign PID:\n%s", stdout)
	}
}

// Regression for PR #105: an empty stdin manifest must produce an empty deps
// answer instead of panicking on a nil graph's coverage.
func TestRunDepsModeEmptyStdinManifestDoesNotPanic(t *testing.T) {
	for _, jsonMode := range []bool{true, false} {
		name := "text"
		if jsonMode {
			name = "json"
		}
		t.Run(name, func(t *testing.T) {
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			oldStdin := os.Stdin
			os.Stdin = reader
			t.Cleanup(func() {
				os.Stdin = oldStdin
				_ = reader.Close()
			})
			if _, err := writer.WriteString(`{"root":"stdin-root","files":[]}`); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}

			stdout, _ := captureMainStreams(t, func() {
				runDepsMode("stdin-root", "stdin-root", jsonMode, "main", nil, true, scanner.Filters{})
			})
			if jsonMode {
				var project scanner.DepsProject
				if err := json.Unmarshal([]byte(stdout), &project); err != nil {
					t.Fatalf("decode empty stdin deps JSON: %v\n%s", err, stdout)
				}
				if project.Mode != "deps" || len(project.Files) != 0 {
					t.Fatalf("expected empty deps project, got %+v", project)
				}
			} else if !strings.Contains(stdout, "No source files found.") {
				t.Fatalf("expected empty rendered deps, got:\n%s", stdout)
			}
		})
	}
}

func TestSafeStdinManifestPath(t *testing.T) {
	for _, accepted := range []string{"go.mod", "src/main.go", "a/b/c.ts", "./go.mod", "a/../b/x.go"} {
		got, ok := safeStdinManifestPath(accepted)
		if !ok {
			t.Fatalf("safeStdinManifestPath(%q) rejected, want accept", accepted)
		}
		if got == "" {
			t.Fatalf("safeStdinManifestPath(%q) = empty path", accepted)
		}
	}
	for _, rejected := range []string{"", "../x", "a/../../x", "/abs/path", ".."} {
		if _, ok := safeStdinManifestPath(rejected); ok {
			t.Fatalf("safeStdinManifestPath(%q) accepted, want reject", rejected)
		}
	}
}

// A --stdin manifest must never write outside the private temp directory:
// parent traversal and absolute paths are rejected before any file is written,
// so a hostile manifest cannot touch arbitrary paths on disk.
func TestRunDepsFromStdinRejectsEscapingPaths(t *testing.T) {
	for _, path := range []string{"../outside.go", "a/../../outside.go", "/etc/outside.go", ".."} {
		t.Run(path, func(t *testing.T) {
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			oldStdin := os.Stdin
			os.Stdin = reader
			t.Cleanup(func() {
				os.Stdin = oldStdin
				_ = reader.Close()
			})
			manifest := fmt.Sprintf(`{"root":"stdin-root","files":[{"path":%q,"content":"package main\n"},{"path":"go.mod","content":"module example.com/stdin\n"}]}`, path)
			if _, err := writer.WriteString(manifest); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := runDepsFromStdin(scanner.Filters{}); err == nil {
				t.Fatalf("expected error for manifest path %q", path)
			}
		})
	}
}
