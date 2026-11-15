package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"codemap/limits"
)

// stateFixture is a git repo with a few files and one commit.
func stateFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range map[string]string{
		"go.mod":     "module example.com/state\n\ngo 1.22\n",
		"main.go":    "package main\n\nimport \"example.com/state/pkg\"\n\nfunc main() { pkg.X() }\n",
		"pkg/pkg.go": "package pkg\n\nfunc X() {}\n",
		"docs/x.md":  "# x\n",
		"cmd/a/a.go": "package a\n\nimport \"example.com/state/pkg\"\n\nvar _ = pkg.X\n",
		"cmd/b/b.go": "package b\n\nimport \"example.com/state/pkg\"\n\nvar _ = pkg.X\n",
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGitMainTestCmd(t, root, "init", "-q")
	runGitMainTestCmd(t, root, "add", ".")
	runGitMainTestCmd(t, root, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "init")
	return root
}

// runEveryEntryPoint runs the analysis commands and every hook against root
// with the user cache pointed at cache, then stops the daemon session-start
// started.
func runEveryEntryPoint(t *testing.T, root, cache string) {
	t.Helper()
	run := func(stdin string, args ...string) {
		t.Helper()
		cmd := exec.Command(codemapTestBinaryPath, args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cache, "CODEMAP_HOOK_TIMEOUT=30s")
		cmd.Stdin = strings.NewReader(stdin)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("codemap %v: %v\n%s", args, err, stderr.String())
		}
	}
	t.Cleanup(func() { run("", "watch", "stop", root) })
	edit := `{"session_id":"exit-test","tool_input":{"file_path":"` + filepath.ToSlash(filepath.Join(root, "main.go")) + `"}}`
	run("", ".")
	run("", "--deps", ".")
	run("", "--importers", "pkg/pkg.go", ".")
	run("", "find", "x", ".")
	run(`{"session_id":"exit-test"}`, "hook", "session-start")
	run(`{"session_id":"exit-test","prompt":"refactor pkg/pkg.go"}`, "hook", "prompt-submit")
	run(edit, "hook", "pre-edit")
	run(edit, "hook", "post-edit")
	run(`{"cwd":"`+filepath.ToSlash(root)+`"}`, "hook", "subagent-start")
	run(`{"session_id":"exit-test"}`, "hook", "pre-compact")
	run(`{"session_id":"exit-test"}`, "hook", "session-stop")
}

func findFile(t *testing.T, dir, name string) string {
	t.Helper()
	found := ""
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == name {
			found = path
		}
		return nil
	})
	return found
}

// TestUnsetUpRepoKeepsNothingInTheRepo is the exit test for #184: every entry
// point in a repo that never ran setup leaves the checkout untouched and
// keeps machine state in the user cache.
func TestUnsetUpRepoKeepsNothingInTheRepo(t *testing.T) {
	root := stateFixture(t)
	cache := t.TempDir()
	exclude := filepath.Join(root, ".git", "info", "exclude")
	before, _ := os.ReadFile(exclude)

	runEveryEntryPoint(t, root, cache)

	if _, err := os.Stat(filepath.Join(root, ".codemap")); !os.IsNotExist(err) {
		t.Fatalf(".codemap/ created in an un-set-up repo: %v", err)
	}
	if after, _ := os.ReadFile(exclude); !bytes.Equal(after, before) {
		t.Fatalf(".git/info/exclude changed:\nbefore %q\nafter  %q", before, after)
	}
	status := exec.Command("git", "status", "--porcelain", "--ignored")
	status.Dir = root
	out, err := status.Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("git status --porcelain --ignored not empty:\n%s", out)
	}
	if findFile(t, cache, "agent_edits.jsonl") == "" {
		t.Fatalf("post-edit record not under the cache dir %s", cache)
	}
}

// TestUnsetUpRepoWithoutCacheDirWritesNothing: with no user cache directory
// (no HOME, no XDG_CACHE_HOME) state is unavailable, never put in a shared
// temp dir, and every hook still exits 0.
func TestUnsetUpRepoWithoutCacheDirWritesNothing(t *testing.T) {
	root := stateFixture(t)
	edit := `{"session_id":"s","tool_input":{"file_path":"` + filepath.ToSlash(filepath.Join(root, "main.go")) + `"}}`
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HOME=") && !strings.HasPrefix(kv, "XDG_CACHE_HOME=") && !strings.HasPrefix(kv, "LOCALAPPDATA=") {
			env = append(env, kv)
		}
	}
	for _, step := range []struct{ stdin, hook string }{
		{`{"session_id":"s"}`, "session-start"},
		{`{"session_id":"s","prompt":"fix pkg/pkg.go"}`, "prompt-submit"},
		{edit, "pre-edit"},
		{edit, "post-edit"},
		{`{"cwd":"` + filepath.ToSlash(root) + `"}`, "subagent-start"},
		{`{"session_id":"s"}`, "pre-compact"},
		{`{"session_id":"s"}`, "session-stop"},
	} {
		cmd := exec.Command(codemapTestBinaryPath, "hook", step.hook)
		cmd.Dir = root
		cmd.Env = env
		cmd.Stdin = strings.NewReader(step.stdin)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("hook %s without a cache dir: %v\n%s", step.hook, err, out)
		}
	}
	stop := exec.Command(codemapTestBinaryPath, "watch", "stop", root)
	stop.Env = env
	_ = stop.Run()
	if _, err := os.Stat(filepath.Join(root, ".codemap")); !os.IsNotExist(err) {
		t.Fatalf(".codemap/ created without a cache dir: %v", err)
	}
	if matches, _ := filepath.Glob(filepath.Join(os.TempDir(), "codemap", "*")); len(matches) != 0 {
		t.Fatalf("state fell back to the shared temp dir: %v", matches)
	}
}

// codemapIn runs the test binary in dir with env appended and returns
// combined output.
func codemapIn(t *testing.T, dir string, env []string, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(codemapTestBinaryPath, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// setUpFixture runs `codemap setup` without hooks, MCP or skills, so the only
// thing it may add to the repo is .codemap/.
func setUpFixture(t *testing.T, root, cache string) {
	t.Helper()
	if out, err := codemapIn(t, root, []string{"XDG_CACHE_HOME=" + cache}, "", "setup", "--no-hooks", "--no-mcp", "--no-skills", root); err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
}

func gitStatus(t *testing.T, dir string, args ...string) string {
	t.Helper()
	status := exec.Command("git", append([]string{"status", "--porcelain"}, args...)...)
	status.Dir = dir
	out, err := status.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func dirNames(t *testing.T, dir string) string {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return strings.Join(names, ",")
}

// TestSetUpRepoKeepsOnlyConfigInTheRepo: after setup and every entry point,
// .codemap/ holds only what the user authors and all state is in the cache.
func TestSetUpRepoKeepsOnlyConfigInTheRepo(t *testing.T) {
	root := stateFixture(t)
	cache := t.TempDir()
	setUpFixture(t, root, cache)

	runEveryEntryPoint(t, root, cache)

	if got, want := gitStatus(t, root, "--untracked-files=all"), "?? .codemap/.gitignore\n?? .codemap/config.json"; got != want {
		t.Fatalf("git status --porcelain = %q, want %q", got, want)
	}
	if got := dirNames(t, filepath.Join(root, ".codemap")); got != ".gitignore,config.json" {
		t.Fatalf(".codemap/ holds %q, want only .gitignore and config.json", got)
	}
	for _, name := range []string{"agent_edits.jsonl", "state.json", "hubs.txt"} {
		if findFile(t, cache, name) == "" {
			t.Fatalf("%s not under the cache dir", name)
		}
	}
	if runtime.GOOS == "windows" {
		return
	}
	// V3: every state dir is 0700 and every state file 0600.
	_ = filepath.WalkDir(filepath.Join(cache, "codemap"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			t.Fatal(err)
		}
		info, _ := d.Info()
		if want := map[bool]os.FileMode{true: 0o700, false: 0o600}[d.IsDir()]; info.Mode().Perm() != want {
			t.Errorf("%s: mode %v, want %v", path, info.Mode().Perm(), want)
		}
		return nil
	})
}

// V5: removing .codemap/ does not move state, so nothing stale comes back.
func TestRemovingCodemapDirKeepsStateWhereItIs(t *testing.T) {
	root := stateFixture(t)
	cache := t.TempDir()
	setUpFixture(t, root, cache)
	env := []string{"XDG_CACHE_HOME=" + cache}
	edit := func(file string) {
		t.Helper()
		stdin := `{"session_id":"s","tool_input":{"file_path":"` + filepath.ToSlash(filepath.Join(root, file)) + `"}}`
		if out, err := codemapIn(t, root, env, stdin, "hook", "post-edit"); err != nil {
			t.Fatalf("post-edit: %v\n%s", err, out)
		}
	}
	edit("main.go")
	before := findFile(t, cache, "agent_edits.jsonl")
	if err := os.RemoveAll(filepath.Join(root, ".codemap")); err != nil {
		t.Fatal(err)
	}
	edit("pkg/pkg.go")
	if after := findFile(t, cache, "agent_edits.jsonl"); after != before || before == "" {
		t.Fatalf("agent edits moved from %q to %q", before, after)
	}
	data, _ := os.ReadFile(before)
	if !strings.Contains(string(data), "main.go") || !strings.Contains(string(data), "pkg/pkg.go") {
		t.Fatalf("one log should hold both edits:\n%s", data)
	}
	if _, err := os.Stat(filepath.Join(root, ".codemap")); !os.IsNotExist(err) {
		t.Fatalf(".codemap/ came back: %v", err)
	}
}

// Linked worktrees never get a .codemap/, whether the primary is set up or
// not, and deleting the primary's .codemap/ does not make one appear.
func TestLinkedWorktreeKeepsNothingInTheWorktree(t *testing.T) {
	for _, setUp := range []bool{true, false} {
		name := "primary not set up"
		if setUp {
			name = "primary set up"
		}
		t.Run(name, func(t *testing.T) {
			primary := stateFixture(t)
			cache := t.TempDir()
			if setUp {
				setUpFixture(t, primary, cache)
			}
			worktree := filepath.Join(t.TempDir(), "wt")
			runGitMainTestCmd(t, primary, "worktree", "add", "-q", worktree, "-b", "wt")
			t.Cleanup(func() {
				run := exec.Command("git", "worktree", "remove", "--force", worktree)
				run.Dir = primary
				_ = run.Run()
			})

			check := func() {
				t.Helper()
				if got := gitStatus(t, worktree, "--ignored"); got != "" {
					t.Fatalf("worktree git status --porcelain --ignored = %q, want empty", got)
				}
				if _, err := os.Lstat(filepath.Join(worktree, ".codemap")); !os.IsNotExist(err) {
					t.Fatalf("worktree got a .codemap/: %v", err)
				}
			}
			runEveryEntryPoint(t, worktree, cache)
			check()
			if setUp {
				if err := os.RemoveAll(filepath.Join(primary, ".codemap")); err != nil {
					t.Fatal(err)
				}
				runEveryEntryPoint(t, worktree, cache)
				check()
				if _, err := os.Lstat(filepath.Join(primary, ".codemap")); !os.IsNotExist(err) {
					t.Fatalf("primary .codemap/ recreated: %v", err)
				}
			}
		})
	}
}

// Finding 4: with no in-repo config, the auto-detected `only` keeps a few Go
// files among thousands of data files under the large-repo cutoff.
func TestAutoConfigKeepsHubsInADataHeavyRepo(t *testing.T) {
	root := stateFixture(t)
	for i := 0; i <= limits.LargeRepoFileCount; i++ {
		path := filepath.Join(root, "data", fmt.Sprintf("d%03d", i/100), fmt.Sprintf("r%d.json", i))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := codemapIn(t, root, []string{"XDG_CACHE_HOME=" + t.TempDir()}, "", ".")
	if err != nil {
		t.Fatalf("codemap .: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Hubs") || !strings.Contains(out, "pkg") || strings.Contains(out, "graph limit") {
		t.Fatalf("no hubs from the auto config:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(root, ".codemap")); !os.IsNotExist(err) {
		t.Fatalf("auto config wrote .codemap/: %v", err)
	}
}

// daemonPIDs lists live `watch daemon <root>` processes.
func daemonPIDs(t *testing.T, root string) []string {
	t.Helper()
	out, _ := exec.Command("pgrep", "-f", "watch daemon "+root).Output()
	return strings.Fields(string(out))
}

// Finding 1: setup while a daemon runs does not split state, so status still
// sees it and start does not launch a second.
func TestSetupWhileDaemonRunsKeepsOneDaemon(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pgrep")
	}
	root := projectpathCanonical(t, stateFixture(t))
	env := []string{"XDG_CACHE_HOME=" + t.TempDir()}
	t.Cleanup(func() { _, _ = codemapIn(t, root, env, "", "watch", "stop", root) })
	if out, err := codemapIn(t, root, env, "", "watch", "start", root); err != nil {
		t.Fatalf("watch start: %v\n%s", err, out)
	}
	before := daemonPIDs(t, root)
	if len(before) != 1 {
		t.Fatalf("daemons after start = %v", before)
	}
	if out, err := codemapIn(t, root, env, "", "setup", "--no-hooks", "--no-mcp", "--no-skills", root); err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	if out, _ := codemapIn(t, root, env, "", "watch", "status", root); !strings.Contains(out, "Watch daemon running") {
		t.Fatalf("status after setup: %s", out)
	}
	if out, _ := codemapIn(t, root, env, "", "watch", "start", root); !strings.Contains(out, "already running") {
		t.Fatalf("start after setup: %s", out)
	}
	if after := daemonPIDs(t, root); strings.Join(after, ",") != strings.Join(before, ",") {
		t.Fatalf("daemons %v became %v", before, after)
	}
}

// Finding 1 across an upgrade: a live registered daemon whose state is not
// in the current state dir is replaced, never joined by a second, and
// `watch stop` stops whatever is registered.
func TestRegisteredDaemonWithEmptyStateDirGetsNoSecond(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pgrep")
	}
	root := projectpathCanonical(t, stateFixture(t))
	oldEnv := []string{"XDG_CACHE_HOME=" + t.TempDir()}
	newEnv := []string{"XDG_CACHE_HOME=" + t.TempDir()}
	t.Cleanup(func() {
		_, _ = codemapIn(t, root, newEnv, "", "watch", "stop", root)
		_, _ = codemapIn(t, root, oldEnv, "", "watch", "stop", root)
	})
	if out, err := codemapIn(t, root, oldEnv, "", "watch", "start", root); err != nil {
		t.Fatalf("watch start: %v\n%s", err, out)
	}
	old := daemonPIDs(t, root)
	if len(old) != 1 {
		t.Fatalf("daemons after start = %v", old)
	}
	out, err := codemapIn(t, root, newEnv, "", "watch", "start", root)
	if err != nil || !strings.Contains(out, "older codemap") {
		t.Fatalf("start with an empty state dir: %v\n%s", err, out)
	}
	now := daemonPIDs(t, root)
	if len(now) != 1 || now[0] == old[0] {
		t.Fatalf("daemons %v then %v, want the registered one replaced by exactly one", old, now)
	}
	if out, err := codemapIn(t, root, newEnv, "", "watch", "stop", root); err != nil {
		t.Fatalf("watch stop: %v\n%s", err, out)
	}
	if left := daemonPIDs(t, root); len(left) != 0 {
		t.Fatalf("daemons left after stop: %v", left)
	}
}

func projectpathCanonical(t *testing.T, path string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

// V1, V2: no usable state dir (missing home, read-only home, a planted link)
// never fails a hook or prints the error; codemap handoff reports one line.
func TestNoUsableStateDirIsQuiet(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions and links")
	}
	readOnly := t.TempDir()
	if err := os.Chmod(readOnly, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0o755) })
	linkCache := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(linkCache, "codemap")); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "gone")
	cases := map[string][]string{
		"missing home":   {"HOME=" + missing, "XDG_CACHE_HOME=", "CODEMAP_DAEMON_REGISTRY="},
		"read-only home": {"HOME=" + readOnly, "XDG_CACHE_HOME="},
		"planted link":   {"XDG_CACHE_HOME=" + linkCache},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			root := stateFixture(t)
			edit := `{"session_id":"s","tool_input":{"file_path":"` + filepath.ToSlash(filepath.Join(root, "main.go")) + `"}}`
			for _, step := range []struct{ stdin, hook string }{
				{`{"session_id":"s"}`, "session-start"},
				{edit, "post-edit"},
				{`{"cwd":"` + filepath.ToSlash(root) + `"}`, "subagent-start"},
				{`{"session_id":"s"}`, "pre-compact"},
				{`{"session_id":"s"}`, "session-stop"},
			} {
				out, err := codemapIn(t, root, env, step.stdin, "hook", step.hook)
				if err != nil || strings.Contains(out, "permission denied") || strings.Contains(out, "unsafe runtime") {
					t.Fatalf("hook %s: %v\n%s", step.hook, err, out)
				}
			}
			out, err := codemapIn(t, root, env, "", "handoff", ".")
			if err == nil || strings.Count(strings.TrimSpace(out), "\n") > 0 {
				t.Fatalf("handoff should fail with one line, got %v:\n%s", err, out)
			}
			if _, err := os.Lstat(missing); !os.IsNotExist(err) {
				t.Fatalf("a missing home was created: %v", err)
			}
			if entries, _ := os.ReadDir(readOnly); len(entries) != 0 {
				t.Fatalf("wrote into the read-only home: %v", entries)
			}
		})
	}
}

// V4: a hook timeout that ends the process before the render still leaves
// the background render running, which fills the cache and drops its lock.
func TestSubagentStartTimeoutStillFillsTheCache(t *testing.T) {
	root := stateFixture(t)
	cache := t.TempDir()
	out, err := codemapIn(t, root, []string{"XDG_CACHE_HOME=" + cache, "CODEMAP_HOOK_TIMEOUT=1ms"}, `{"cwd":"`+filepath.ToSlash(root)+`"}`, "hook", "subagent-start")
	if err != nil || out != "" {
		t.Fatalf("timed-out subagent-start: %v %q", err, out)
	}
	deadline := time.Now().Add(20 * time.Second)
	for findFile(t, cache, "subagent-start.json") == "" || findFile(t, cache, "subagent-start.lock") != "" {
		if time.Now().After(deadline) {
			t.Fatal("background render never filled the cache or left its lock")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
