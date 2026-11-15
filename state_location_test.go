package main

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

// TestSetUpRepoKeepsStateInRepo: a repo with .codemap/ keeps its state there
// exactly as before, and the user cache stays empty.
func TestSetUpRepoKeepsStateInRepo(t *testing.T) {
	root := stateFixture(t)
	if err := os.Mkdir(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()

	runEveryEntryPoint(t, root, cache)

	if _, err := os.Stat(filepath.Join(root, ".codemap", "agent_edits.jsonl")); err != nil {
		t.Fatalf("post-edit record not in .codemap/: %v", err)
	}
	if findFile(t, filepath.Join(root, ".codemap", "projects"), "state.json") == "" {
		t.Fatal("session-start state not under .codemap/projects/")
	}
	if entries, _ := os.ReadDir(filepath.Join(cache, "codemap")); len(entries) != 0 {
		t.Fatalf("set-up repo wrote into the user cache: %v", entries)
	}
}
