package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"codemap/config"
	"codemap/scanner"
)

func TestInitProjectConfigCreatesAutoDetectedConfig(t *testing.T) {
	root := t.TempDir()
	mustWriteConfigFixture(t, filepath.Join(root, "pkg", "main.go"), "package main\n")
	mustWriteConfigFixture(t, filepath.Join(root, "pkg", "worker.go"), "package main\n")
	mustWriteConfigFixture(t, filepath.Join(root, "pkg", "extra.go"), "package main\n")
	mustWriteConfigFixture(t, filepath.Join(root, "web", "app.ts"), "export const app = 1\n")
	mustWriteConfigFixture(t, filepath.Join(root, "web", "view.ts"), "export const view = 1\n")
	mustWriteConfigFixture(t, filepath.Join(root, "scripts", "seed.py"), "print('ok')\n")
	mustWriteConfigFixture(t, filepath.Join(root, "README.md"), "docs\n")
	mustWriteConfigFixture(t, filepath.Join(root, "assets", "logo.png"), "png\n")

	result, err := initProjectConfig(root)
	if err != nil {
		t.Fatalf("initProjectConfig returned error: %v", err)
	}

	if got, want := strings.Join(result.TopExts, ","), "go,ts,py"; got != want {
		t.Fatalf("TopExts = %q, want %q", got, want)
	}
	if result.TotalFiles != 8 {
		t.Fatalf("TotalFiles = %d, want 8", result.TotalFiles)
	}
	if result.MatchedFiles != 6 {
		t.Fatalf("MatchedFiles = %d, want 6", result.MatchedFiles)
	}

	data, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg config.ProjectConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if got, want := strings.Join(cfg.Only, ","), "go,ts,py"; got != want {
		t.Fatalf("config only = %q, want %q", got, want)
	}
}

func TestInitProjectConfigReturnsErrConfigExists(t *testing.T) {
	root := t.TempDir()
	cfgPath := config.ConfigPath(root)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := initProjectConfig(root)
	if !errors.Is(err, errConfigExists) {
		t.Fatalf("expected errConfigExists, got %v", err)
	}
}

func TestRunConfigInitAndShow(t *testing.T) {
	root := t.TempDir()
	mustWriteConfigFixture(t, filepath.Join(root, "cmd", "main.go"), "package main\n")
	mustWriteConfigFixture(t, filepath.Join(root, "cmd", "worker.go"), "package main\n")

	initOut := captureOutput(func() { RunConfig("init", root) })
	if !strings.Contains(initOut, "Created ") || !strings.Contains(initOut, "only: go") {
		t.Fatalf("unexpected init output:\n%s", initOut)
	}

	showOut := captureOutput(func() { RunConfig("show", root) })
	checks := []string{"Config:", "only:", "go"}
	for _, check := range checks {
		if !strings.Contains(showOut, check) {
			t.Fatalf("expected show output to contain %q, got:\n%s", check, showOut)
		}
	}
}

func TestConfigShowNoConfigFile(t *testing.T) {
	out := captureOutput(func() { configShow(t.TempDir()) })
	if !strings.Contains(out, "No config file found.") {
		t.Fatalf("expected missing-config guidance, got:\n%s", out)
	}
}

func TestConfigShowMalformedConfig(t *testing.T) {
	root := t.TempDir()
	cfgPath := config.ConfigPath(root)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureOutput(func() { configShow(root) })
	if !strings.Contains(out, "malformed or unreadable") {
		t.Fatalf("expected malformed guidance, got:\n%s", out)
	}
	if !strings.Contains(out, "rerun 'codemap config init'") {
		t.Fatalf("expected recreate guidance, got:\n%s", out)
	}
}

func TestConfigShowBoilerplateConfigSuggestsTuning(t *testing.T) {
	root := t.TempDir()
	cfgPath := config.ConfigPath(root)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(`{"only":["rs","toml"],"depth":4}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureOutput(func() { configShow(root) })
	if !strings.Contains(out, "looks like a bootstrap") {
		t.Fatalf("expected bootstrap note, got:\n%s", out)
	}
	if !strings.Contains(out, "config-setup") {
		t.Fatalf("expected config-setup guidance, got:\n%s", out)
	}
}

func mustWriteConfigFixture(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestInitProjectConfigSkipsNoiseExtensions(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"main.go":       "package main\n",
		"util.go":       "package main\n",
		"a/one.log":     "log line\n",
		"a/two.log":     "log line\n",
		"a/three.log":   "log line\n",
		"b/daemon.pid":  "123\n",
		"b/backup.bak":  "old\n",
		"b/scratch.tmp": "x\n",
		"b/build.out":   "x\n",
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

	result, err := initProjectConfig(root)
	if err != nil {
		t.Fatalf("initProjectConfig: %v", err)
	}
	for _, ext := range result.TopExts {
		switch ext {
		case "log", "pid", "bak", "tmp", "out":
			t.Fatalf("noise extension %q auto-included in config: %v", ext, result.TopExts)
		}
	}
	if len(result.TopExts) != 1 || result.TopExts[0] != "go" {
		t.Fatalf("TopExts = %v, want [go]", result.TopExts)
	}
}

func TestInitProjectConfigExcludesPresentNoise(t *testing.T) {
	root := t.TempDir()
	mustWriteConfigFixture(t, filepath.Join(root, "App", "main.swift"), "import UIKit\n")
	mustWriteConfigFixture(t, filepath.Join(root, "App", "view.swift"), "import UIKit\n")
	mustWriteConfigFixture(t, filepath.Join(root, "Carthage", "Build", "Alamofire.swift"), "import Foundation\n")
	for i := 0; i < 20; i++ {
		mustWriteConfigFixture(t, filepath.Join(root, "App", "Assets.xcassets", "icon"+strings.Repeat("x", i)+".png"), "png\n")
	}

	result, err := initProjectConfig(root)
	if err != nil {
		t.Fatalf("initProjectConfig: %v", err)
	}
	if got, want := strings.Join(result.Exclude, ","), "Carthage,.xcassets,.png"; got != want {
		t.Fatalf("Exclude = %q, want %q", got, want)
	}
	if got := config.AssessSetup(root).State; got != config.SetupStateReady {
		t.Fatalf("state after init = %q, want ready", got)
	}
}

// TestEnsureAutoConfigStaysOutOfTheRepo: the auto-detected config lives in
// the state dir, is computed once, recomputed when unreadable, loses to an
// in-repo config, and is never written outside a git checkout.
func TestEnsureAutoConfigStaysOutOfTheRepo(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteConfigFixture(t, filepath.Join(repo, "main.go"), "package main\n")
	if !EnsureAutoConfig(repo) {
		t.Fatal("expected an auto config for a repo without config.json")
	}
	if _, err := os.Stat(filepath.Join(repo, ".codemap")); !os.IsNotExist(err) {
		t.Fatalf(".codemap created by the auto config: %v", err)
	}
	auto := config.AutoConfigPath(repo)
	info, err := os.Stat(auto)
	if err != nil {
		t.Fatalf("auto config %s: %v", auto, err)
	}
	// Windows has no Unix permission bits; the 0600 check is Unix-only.
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("auto config %s mode %v; want 0600", auto, info.Mode().Perm())
	}
	if got := strings.Join(config.Load(repo).Only, ","); got != "go" {
		t.Fatalf("Load().Only = %q, want go from the auto config", got)
	}
	if EnsureAutoConfig(repo) {
		t.Fatal("second call must not recompute a readable auto config")
	}
	if err := os.WriteFile(auto, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !EnsureAutoConfig(repo) {
		t.Fatal("an unreadable auto config must be recomputed")
	}

	mustWriteConfigFixture(t, config.ConfigPath(repo), `{"only":["md"]}`)
	if EnsureAutoConfig(repo) {
		t.Fatal("an in-repo config.json must stop the auto config")
	}
	if got := strings.Join(config.Load(repo).Only, ","); got != "md" {
		t.Fatalf("Load().Only = %q, want the in-repo config to win", got)
	}

	plain := t.TempDir()
	mustWriteConfigFixture(t, filepath.Join(plain, "main.go"), "package main\n")
	if EnsureAutoConfig(plain) {
		t.Fatal("must not compute a config outside a repo")
	}
}

// Hooks never run a scan to compute the auto config: with none stored they
// use defaults, and the tree the session-start subprocess runs stores it.
func TestHooksNeverScanForTheAutoConfig(t *testing.T) {
	root := makeRepoOnBranch(t, "main")
	mustWriteConfigFixture(t, filepath.Join(root, "main.go"), "package main\n")
	scans := 0
	orig := scanForConfig
	t.Cleanup(func() { scanForConfig = orig })
	scanForConfig = func(root string) ([]scanner.FileInfo, error) { scans++; return orig(root) }
	withHookRuntimeStubs(t, func() (string, error) { return "", errors.New("no subprocess in this test") }, nil, func(string) bool { return true }, func(time.Duration) {})

	edit := `{"session_id":"s","tool_input":{"file_path":"` + filepath.ToSlash(filepath.Join(root, "main.go")) + `"}}`
	for _, step := range []struct{ hook, stdin string }{
		{"session-start", `{"session_id":"s"}`},
		{"prompt-submit", `{"session_id":"s","prompt":"fix main.go"}`},
		{"pre-edit", edit},
		{"post-edit", edit},
		{"pre-compact", `{"session_id":"s"}`},
		{"session-stop", `{"session_id":"s"}`},
	} {
		withStdinInput(t, step.stdin, func() { captureOutput(func() { _ = RunHook(step.hook, root) }) })
	}
	if scans != 0 {
		t.Fatalf("hooks ran %d config scan(s)", scans)
	}
	if _, err := os.Stat(config.AutoConfigPath(root)); !os.IsNotExist(err) {
		t.Fatalf("a hook stored an auto config: %v", err)
	}
}

// The tree re-derives the auto config from its own scan, so a repo that
// gained Go files after a Python-only start shows them on the next tree.
func TestRefreshAutoConfigFollowsTheFileMix(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	runGitTestCmd(t, root, "init", "-q")
	mustWriteConfigFixture(t, filepath.Join(root, "tool.py"), "print(1)\n")
	listed := RefreshAutoConfig(root)
	if got := strings.Join(config.Load(root).Only, ","); got != "py" {
		t.Fatalf("only = %q, want py", got)
	}
	if note := AutoFilterNote(config.Load(root), listed); note != "" {
		t.Fatalf("a filter that hides nothing printed %q", note)
	}
	mustWriteConfigFixture(t, filepath.Join(root, "pkg", "a.go"), "package pkg\n")
	mustWriteConfigFixture(t, filepath.Join(root, "pkg", "b.go"), "package pkg\n")
	mustWriteConfigFixture(t, filepath.Join(root, "README.md"), "# r\n")
	listed = RefreshAutoConfig(root)
	if got := config.Load(root).Only; !slices.Contains(got, "go") {
		t.Fatalf("stored only = %v, want go added", got)
	}
	if note := AutoFilterNote(config.Load(root), listed); !strings.HasPrefix(note, "auto filter: only go,py") {
		t.Fatalf("note = %q", note)
	}
}

// Without git the stored auto config is kept, never re-derived from a walk.
func TestRefreshAutoConfigWithoutGitKeepsTheStoredCopy(t *testing.T) {
	root := t.TempDir()
	mustWriteConfigFixture(t, filepath.Join(root, ".git", "HEAD"), "garbage")
	mustWriteConfigFixture(t, filepath.Join(root, "a.py"), "x = 1\n")
	if !writeAutoConfig(root, config.ProjectConfig{Only: []string{"go"}, Mode: "auto"}) {
		t.Fatal("could not store the auto config")
	}
	if listed := RefreshAutoConfig(root); listed != nil {
		t.Fatalf("listed %d files from a broken git dir", len(listed))
	}
	if got := strings.Join(config.Load(root).Only, ","); got != "go" {
		t.Fatalf("stored only = %q, want go kept", got)
	}
}

// TestEnsureAutoConfigKeepsFreshCloneClean: the auto config leaves a fresh
// clone with nothing new, not even ignored files.
func TestEnsureAutoConfigKeepsFreshCloneClean(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	src := makeRepoOnBranch(t, "main")
	clone := filepath.Join(t.TempDir(), "clone")
	runGitTestCmd(t, src, "clone", "-q", src, clone)

	if !EnsureAutoConfig(clone) {
		t.Fatal("expected an auto config in a fresh clone")
	}
	status := exec.Command("git", "status", "--porcelain", "--ignored", "--untracked-files=all")
	status.Dir = clone
	out, err := status.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "" {
		t.Fatalf("git status after the auto config = %q, want clean", got)
	}
}

func TestInitProjectConfigKeepsMinoritySourceExtension(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 6; i++ {
		n := strconv.Itoa(i)
		mustWriteConfigFixture(t, filepath.Join(root, "src", "mod"+n+".js"), "export const x = 1\n")
		mustWriteConfigFixture(t, filepath.Join(root, "tools", "t"+n+".py"), "print('ok')\n")
		mustWriteConfigFixture(t, filepath.Join(root, "conf", "c"+n+".yaml"), "a: 1\n")
		mustWriteConfigFixture(t, filepath.Join(root, "bin", "s"+n+".sh"), "echo ok\n")
		mustWriteConfigFixture(t, filepath.Join(root, "conf", "c"+n+".toml"), "a = 1\n")
	}
	mustWriteConfigFixture(t, filepath.Join(root, "scripts", "build.mjs"), "export default 1\n")
	mustWriteConfigFixture(t, filepath.Join(root, "scripts", "release.mjs"), "export default 2\n")
	mustWriteConfigFixture(t, filepath.Join(root, "notes", "a.rst"), "notes\n")

	result, err := initProjectConfig(root)
	if err != nil {
		t.Fatalf("initProjectConfig: %v", err)
	}
	if !slices.Contains(result.TopExts, "mjs") {
		t.Fatalf("TopExts = %v, want it to include mjs", result.TopExts)
	}
	if slices.Contains(result.TopExts, "rst") {
		t.Fatalf("TopExts = %v, non-source minority extension rst should stay cut", result.TopExts)
	}
	if result.MatchedFiles != 32 {
		t.Fatalf("MatchedFiles = %d, want 32", result.MatchedFiles)
	}
}

// Six unparsed extensions tie for four slots; without a name tiebreak the
// map order decides which survive, so `only` changed between runs.
func TestInitProjectConfigBreaksCountTiesByName(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 3; i++ {
		mustWriteConfigFixture(t, filepath.Join(root, "src", fmt.Sprintf("m%d.js", i)), "export const x = 1\n")
	}
	for _, ext := range []string{"yaml", "toml", "ini", "html", "css", "cfg"} {
		mustWriteConfigFixture(t, filepath.Join(root, "conf", "a."+ext), "x\n")
	}

	result, err := initProjectConfig(root)
	if err != nil {
		t.Fatalf("initProjectConfig: %v", err)
	}
	if got, want := strings.Join(result.TopExts, ","), "js,cfg,css,html,ini"; got != want {
		t.Fatalf("TopExts = %q, want %q", got, want)
	}
}
