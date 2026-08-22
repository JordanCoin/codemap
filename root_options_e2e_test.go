package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGlobalRootOptionsEndToEnd(t *testing.T) {
	root := t.TempDir()
	projectRoot := filepath.Join(root, "worktree")
	setupRoot := filepath.Join(root, "original")
	for _, repo := range []string{projectRoot, setupRoot} {
		if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	projectNested := filepath.Join(projectRoot, "pkg", "feature")
	setupNested := filepath.Join(setupRoot, "cmd")
	if err := os.MkdirAll(projectNested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(setupNested, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestSkill(t, setupRoot, "setup-only")

	t.Run("linked worktree inherits primary skill without setup flag", func(t *testing.T) {
		gitDir := filepath.Join(setupRoot, ".git", "worktrees", "automatic")
		if err := os.MkdirAll(gitDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(gitDir, "commondir"), []byte("../..\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		linked := filepath.Join(root, "automatic-linked")
		nested := filepath.Join(linked, "nested")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(linked, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		out, err := runRootOptionsBinary(nested, "-C", ".", "skill", "list")
		if err != nil {
			t.Fatalf("codemap failed: %v\n%s", err, out)
		}
		if !strings.Contains(out, "setup-only") {
			t.Fatalf("primary skill missing from automatic linked worktree:\n%s", out)
		}
	})

	t.Run("independent repository does not inherit another setup", func(t *testing.T) {
		out, err := runRootOptionsBinary(projectNested, "-C", ".", "skill", "list")
		if err != nil {
			t.Fatalf("codemap failed: %v\n%s", err, out)
		}
		if strings.Contains(out, "setup-only") {
			t.Fatalf("independent repository inherited unrelated setup:\n%s", out)
		}
	})

	t.Run("relative setup root reuses original storage", func(t *testing.T) {
		relSetup, err := filepath.Rel(projectRoot, setupNested)
		if err != nil {
			t.Fatal(err)
		}
		out, err := runRootOptionsBinary(projectNested,
			"-C", ".",
			"--setup-root", relSetup,
			"skill", "list",
		)
		if err != nil {
			t.Fatalf("codemap failed: %v\n%s", err, out)
		}
		if !strings.Contains(out, "setup-only") {
			t.Fatalf("setup-root skill missing from output:\n%s", out)
		}
	})

	t.Run("inherited setup root environment is ignored", func(t *testing.T) {
		hostileRoot := filepath.Join(root, "hostile")
		if err := os.MkdirAll(filepath.Join(hostileRoot, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeTestSkill(t, hostileRoot, "hostile-only")

		command := exec.Command(codemapTestBinaryPath,
			"--project-root", projectNested,
			"skill", "list",
		)
		command.Env = append(os.Environ(), "CODEMAP_SETUP_ROOT="+hostileRoot)
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("codemap failed: %v\n%s", err, out)
		}
		if strings.Contains(string(out), "hostile-only") {
			t.Fatalf("inherited environment redirected setup storage:\n%s", out)
		}
	})

	t.Run("project commands reject malformed per-command roots", func(t *testing.T) {
		malformedRoot := filepath.Join(root, "malformed-command-root")
		if err := os.MkdirAll(malformedRoot, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(malformedRoot, ".git"), []byte("not a gitdir\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		commands := map[string][]string{
			"analysis":     {malformedRoot},
			"blast radius": {"blast-radius", malformedRoot},
			"config":       {"config", "show", malformedRoot},
			"context":      {"context", malformedRoot},
			"handoff":      {"handoff", "--latest", malformedRoot},
			"hook":         {"hook", "pre-edit", malformedRoot},
			"watch":        {"watch", "status", malformedRoot},
		}
		for name, args := range commands {
			t.Run(name, func(t *testing.T) {
				out, err := runRootOptionsBinary(projectNested, args...)
				if err == nil {
					t.Fatalf("codemap unexpectedly accepted malformed project metadata:\n%s", out)
				}
				if !strings.Contains(out, "resolve linked worktree setup") && !strings.Contains(out, "invalid Git marker") {
					t.Fatalf("unexpected rejection output:\n%s", out)
				}
			})
		}
	})

	t.Run("symlinked codemap storage is rejected", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlinks may require elevated privileges")
		}
		unsafeRoot := filepath.Join(root, "unsafe")
		if err := os.MkdirAll(filepath.Join(unsafeRoot, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(t.TempDir(), filepath.Join(unsafeRoot, ".codemap")); err != nil {
			t.Fatal(err)
		}

		out, err := runRootOptionsBinary(projectNested,
			"--setup-root", unsafeRoot,
			"skill", "list",
		)
		if err == nil {
			t.Fatalf("codemap unexpectedly accepted symlinked storage:\n%s", out)
		}
		if !strings.Contains(out, "unsafe Codemap storage") {
			t.Fatalf("unexpected rejection output:\n%s", out)
		}
	})
}

func TestStandardSubmoduleUsesProjectLocalSetup(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	super := filepath.Join(root, "super")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(super, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitFixtureCommand(t, source, "init", "-q")
	runGitFixtureCommand(t, source, "-c", "user.name=Codemap Test", "-c", "user.email=codemap@example.invalid", "commit", "--allow-empty", "-q", "-m", "initial")
	runGitFixtureCommand(t, super, "init", "-q")
	runGitFixtureCommand(t, super, "-c", "user.name=Codemap Test", "-c", "user.email=codemap@example.invalid", "commit", "--allow-empty", "-q", "-m", "initial")
	runGitFixtureCommand(t, super, "-c", "protocol.file.allow=always", "submodule", "add", "-q", source, "child")

	submodule := filepath.Join(super, "child")
	writeTestSkill(t, submodule, "submodule-local")
	out, err := runRootOptionsBinary(super, "-C", submodule, "skill", "list")
	if err != nil {
		t.Fatalf("codemap rejected standard submodule gitfile: %v\n%s", err, out)
	}
	if !strings.Contains(out, "submodule-local") {
		t.Fatalf("submodule-local skill missing from output:\n%s", out)
	}
}

func writeTestSkill(t *testing.T, root, name string) {
	t.Helper()
	dir := filepath.Join(root, ".codemap", "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: root option fixture\n---\n\n# Fixture\n"
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runRootOptionsBinary(dir string, args ...string) (string, error) {
	command := exec.Command(codemapTestBinaryPath, args...)
	command.Dir = dir
	out, err := command.CombinedOutput()
	return string(out), err
}

func runGitFixtureCommand(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	command.Env = append(os.Environ(),
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=commit.gpgsign",
		"GIT_CONFIG_VALUE_0=false",
	)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}
