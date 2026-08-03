package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestResolveNearestGitRoot(t *testing.T) {
	t.Run("nested directory resolves repository root", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		nested := filepath.Join(root, "pkg", "feature")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}

		got, found, err := ResolveNearestGitRoot(nested)
		if err != nil {
			t.Fatalf("ResolveNearestGitRoot() error: %v", err)
		}
		if !found {
			t.Fatal("expected repository root to be found")
		}
		want := root
		if got != want {
			t.Fatalf("ResolveNearestGitRoot() = %q, want %q", got, want)
		}
	})

	t.Run("missing descendant resolves repository root", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		missing := filepath.Join(root, "future", "feature")

		got, found, err := ResolveNearestGitRoot(missing)
		if err != nil {
			t.Fatalf("ResolveNearestGitRoot() error: %v", err)
		}
		if !found || got != root {
			t.Fatalf("ResolveNearestGitRoot() = %q, %t; want %q, true", got, found, root)
		}
	})

	t.Run("missing non repository preserves absolute input", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "future", "feature")

		got, found, err := ResolveNearestGitRoot(missing)
		if err != nil {
			t.Fatalf("ResolveNearestGitRoot() error: %v", err)
		}
		if found || got != missing {
			t.Fatalf("ResolveNearestGitRoot() = %q, %t; want %q, false", got, found, missing)
		}
	})

	t.Run("git worktree file resolves repository root", func(t *testing.T) {
		root := t.TempDir()
		gitDir := filepath.Join(t.TempDir(), "worktrees", "example")
		if err := os.MkdirAll(gitDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		nested := filepath.Join(root, "pkg", "feature")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}

		got, found, err := ResolveNearestGitRoot(nested)
		if err != nil {
			t.Fatalf("ResolveNearestGitRoot() error: %v", err)
		}
		if !found {
			t.Fatal("expected worktree root to be found")
		}
		want := root
		if got != want {
			t.Fatalf("ResolveNearestGitRoot() = %q, want %q", got, want)
		}
	})

	t.Run("symlinked nested directory preserves repository alias", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("directory symlinks may require elevated privileges")
		}
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		nested := filepath.Join(root, "pkg", "feature")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(t.TempDir(), "repo-link")
		if err := os.Symlink(root, link); err != nil {
			t.Fatal(err)
		}

		got, found, err := ResolveNearestGitRoot(filepath.Join(link, "pkg", "..", "pkg", "feature"))
		if err != nil {
			t.Fatalf("ResolveNearestGitRoot() error: %v", err)
		}
		want := link
		if !found || got != want {
			t.Fatalf("ResolveNearestGitRoot() = %q, %t; want %q, true", got, found, want)
		}
	})

	t.Run("repository-local symlink to external directory does not inherit repository", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("directory symlinks may require elevated privileges")
		}
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		external := t.TempDir()
		link := filepath.Join(root, "external")
		if err := os.Symlink(external, link); err != nil {
			t.Fatal(err)
		}

		for _, input := range []string{link, filepath.Join(link, "future", "feature")} {
			got, found, err := ResolveNearestGitRoot(input)
			if err != nil {
				t.Fatalf("ResolveNearestGitRoot(%q) error: %v", input, err)
			}
			if found || got != input {
				t.Fatalf("ResolveNearestGitRoot(%q) = %q, %t; want %q, false", input, got, found, input)
			}
		}
	})

	t.Run("repository-local symlink into another repository uses physical repository", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("directory symlinks may require elevated privileges")
		}
		outer := t.TempDir()
		if err := os.Mkdir(filepath.Join(outer, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		other := t.TempDir()
		if err := os.Mkdir(filepath.Join(other, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		nested := filepath.Join(other, "pkg")
		if err := os.Mkdir(nested, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(outer, "other-pkg")
		if err := os.Symlink(nested, link); err != nil {
			t.Fatal(err)
		}

		for _, input := range []string{link, filepath.Join(link, "future", "feature")} {
			got, found, err := ResolveNearestGitRoot(input)
			if err != nil {
				t.Fatalf("ResolveNearestGitRoot(%q) error: %v", input, err)
			}
			physicalOther, err := filepath.EvalSymlinks(other)
			if err != nil {
				t.Fatal(err)
			}
			if !found || got != physicalOther {
				t.Fatalf("ResolveNearestGitRoot(%q) = %q, %t; want physical root %q, true", input, got, found, physicalOther)
			}
		}
	})

	t.Run("macOS tmp spelling is preserved", func(t *testing.T) {
		if runtime.GOOS != "darwin" {
			t.Skip("macOS exposes /tmp through /private/tmp")
		}
		root, err := os.MkdirTemp("/tmp", "codemap-root-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(root) })
		if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}

		got, found, err := ResolveNearestGitRoot(root)
		if err != nil {
			t.Fatalf("ResolveNearestGitRoot() error: %v", err)
		}
		if !found || got != root {
			t.Fatalf("ResolveNearestGitRoot() = %q, %t; want %q, true", got, found, root)
		}
	})

	t.Run("regular file input is rejected", func(t *testing.T) {
		root := t.TempDir()
		file := filepath.Join(root, "input")
		if err := os.WriteFile(file, []byte("not a directory"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ResolveNearestGitRoot(file); err == nil {
			t.Fatal("expected regular file input to be rejected")
		}
	})

	t.Run("symlink to file input is rejected", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlinks may require elevated privileges")
		}
		root := t.TempDir()
		target := filepath.Join(root, "target")
		if err := os.WriteFile(target, []byte("not a directory"), 0o644); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(root, "link")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ResolveNearestGitRoot(link); err == nil {
			t.Fatal("expected symlink to a file to be rejected")
		}
	})

	t.Run("dangling symlink input is rejected", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlinks may require elevated privileges")
		}
		root := t.TempDir()
		link := filepath.Join(root, "link")
		if err := os.Symlink(filepath.Join(root, "missing"), link); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ResolveNearestGitRoot(link); err == nil {
			t.Fatal("expected dangling symlink to be rejected")
		}
	})

	t.Run("missing descendant below dangling symlink is rejected", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlinks may require elevated privileges")
		}
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(root, "link")
		if err := os.Symlink(filepath.Join(root, "missing"), link); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ResolveNearestGitRoot(filepath.Join(link, "child")); err == nil {
			t.Fatal("expected a missing descendant below a dangling symlink to be rejected")
		}
	})

	t.Run("device file input is rejected", func(t *testing.T) {
		if _, err := os.Stat(os.DevNull); err != nil {
			t.Skipf("device file unavailable: %v", err)
		}
		if _, _, err := ResolveNearestGitRoot(os.DevNull); err == nil {
			t.Fatal("expected device file input to be rejected")
		}
	})

	t.Run("malformed gitfile is rejected", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, ".git"), []byte("not a gitdir\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ResolveNearestGitRoot(root); err == nil {
			t.Fatal("expected malformed .git file to be rejected")
		}
	})

	t.Run("relative gitfile resolves repository root", func(t *testing.T) {
		root := t.TempDir()
		gitDir := filepath.Join(root, "metadata")
		if err := os.MkdirAll(gitDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: metadata\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, found, err := ResolveNearestGitRoot(root)
		if err != nil {
			t.Fatalf("ResolveNearestGitRoot() error: %v", err)
		}
		if want := root; !found || got != want {
			t.Fatalf("ResolveNearestGitRoot() = %q, %t; want %q, true", got, found, want)
		}
	})

	t.Run("gitfile with missing target is rejected", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: missing\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ResolveNearestGitRoot(root); err == nil {
			t.Fatal("expected .git file with missing target to be rejected")
		}
	})

	t.Run("symlinked git marker is rejected", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlinks may require elevated privileges")
		}
		root := t.TempDir()
		target := filepath.Join(t.TempDir(), "gitdir")
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(root, ".git")); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ResolveNearestGitRoot(root); err == nil {
			t.Fatal("expected symlinked .git marker to be rejected")
		}
	})

	t.Run("repository root remains unchanged", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}

		got, found, err := ResolveNearestGitRoot(root)
		if err != nil {
			t.Fatalf("ResolveNearestGitRoot() error: %v", err)
		}
		if !found {
			t.Fatal("expected repository root to be found")
		}
		want := root
		if got != want {
			t.Fatalf("ResolveNearestGitRoot() = %q, want %q", got, want)
		}
	})

	t.Run("non repository falls back to absolute path", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "nested")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}

		got, found, err := ResolveNearestGitRoot(root)
		if err != nil {
			t.Fatalf("ResolveNearestGitRoot() error: %v", err)
		}
		if found {
			t.Fatal("expected no repository root to be found")
		}
		want := root
		if got != want {
			t.Fatalf("ResolveNearestGitRoot() = %q, want %q", got, want)
		}
	})
}

func TestParseGlobalRootOptions(t *testing.T) {
	t.Run("extracts options around a subcommand", func(t *testing.T) {
		opts, args, err := ParseGlobalRootOptions([]string{
			"-C", "worktree/pkg", "context", "--compact",
			"--setup-root=original/cmd",
		})
		if err != nil {
			t.Fatalf("ParseGlobalRootOptions() error: %v", err)
		}
		if opts.Directory != "worktree/pkg" {
			t.Fatalf("Directory = %q, want worktree/pkg", opts.Directory)
		}
		if opts.SetupRoot != "original/cmd" {
			t.Fatalf("SetupRoot = %q, want original/cmd", opts.SetupRoot)
		}
		wantArgs := []string{"context", "--compact"}
		if !reflect.DeepEqual(args, wantArgs) {
			t.Fatalf("args = %#v, want %#v", args, wantArgs)
		}
	})

	t.Run("project root is the long form of C", func(t *testing.T) {
		opts, args, err := ParseGlobalRootOptions([]string{"context", "--project-root", "worktree/pkg"})
		if err != nil {
			t.Fatalf("ParseGlobalRootOptions() error: %v", err)
		}
		if opts.Directory != "worktree/pkg" {
			t.Fatalf("Directory = %q, want worktree/pkg", opts.Directory)
		}
		if !reflect.DeepEqual(args, []string{"context"}) {
			t.Fatalf("args = %#v, want context", args)
		}
	})

	t.Run("stops extracting at double dash", func(t *testing.T) {
		opts, args, err := ParseGlobalRootOptions([]string{"context", "--", "-C", "literal"})
		if err != nil {
			t.Fatalf("ParseGlobalRootOptions() error: %v", err)
		}
		if opts.Active() {
			t.Fatalf("options unexpectedly active: %#v", opts)
		}
		wantArgs := []string{"context", "--", "-C", "literal"}
		if !reflect.DeepEqual(args, wantArgs) {
			t.Fatalf("args = %#v, want %#v", args, wantArgs)
		}
	})

	for _, args := range [][]string{{"-C"}, {"--project-root"}, {"--project-root="}, {"--setup-root"}, {"--setup-root="}} {
		args := args
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			if _, _, err := ParseGlobalRootOptions(args); err == nil {
				t.Fatalf("ParseGlobalRootOptions(%#v) unexpectedly succeeded", args)
			}
		})
	}

	for _, args := range [][]string{
		{"-C", "--setup-root", "repo"},
		{"--project-root", "-C", "repo"},
		{"--setup-root", "--project-root", "repo"},
	} {
		args := args
		t.Run("missing_before_"+strings.Join(args, "_"), func(t *testing.T) {
			if _, _, err := ParseGlobalRootOptions(args); err == nil {
				t.Fatalf("ParseGlobalRootOptions(%#v) unexpectedly succeeded", args)
			}
		})
	}
}

func TestResolveGlobalRoots(t *testing.T) {
	launchDir := t.TempDir()
	projectRoot := filepath.Join(launchDir, "worktree")
	setupRoot := filepath.Join(launchDir, "original")
	for _, root := range []string{projectRoot, setupRoot} {
		if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	projectNested := filepath.Join(projectRoot, "pkg", "feature")
	setupNested := filepath.Join(setupRoot, "cmd")
	for _, dir := range []string{projectNested, setupNested} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("recovers both explicit nested roots", func(t *testing.T) {
		roots, err := ResolveGlobalRoots(GlobalRootOptions{
			Directory: projectNested,
			SetupRoot: setupNested,
		}, launchDir)
		if err != nil {
			t.Fatalf("ResolveGlobalRoots() error: %v", err)
		}
		if roots.Project != projectRoot {
			t.Fatalf("Project = %q, want %q", roots.Project, projectRoot)
		}
		if roots.Setup != setupRoot {
			t.Fatalf("Setup = %q, want %q", roots.Setup, setupRoot)
		}
	})

	t.Run("setup root alone recovers project from launch directory", func(t *testing.T) {
		roots, err := ResolveGlobalRoots(GlobalRootOptions{SetupRoot: setupNested}, projectNested)
		if err != nil {
			t.Fatalf("ResolveGlobalRoots() error: %v", err)
		}
		if roots.Project != projectRoot {
			t.Fatalf("Project = %q, want %q", roots.Project, projectRoot)
		}
		if roots.Setup != setupRoot {
			t.Fatalf("Setup = %q, want %q", roots.Setup, setupRoot)
		}
	})

	t.Run("setup root alone preserves non repository project", func(t *testing.T) {
		project := t.TempDir()
		roots, err := ResolveGlobalRoots(GlobalRootOptions{SetupRoot: setupNested}, project)
		if err != nil {
			t.Fatalf("ResolveGlobalRoots() error: %v", err)
		}
		if roots.Project != project {
			t.Fatalf("Project = %q, want %q", roots.Project, project)
		}
		if roots.Setup != setupRoot {
			t.Fatalf("Setup = %q, want %q", roots.Setup, setupRoot)
		}
	})

	t.Run("codemap directory resolves to setup repository", func(t *testing.T) {
		codemapDir := filepath.Join(setupRoot, ".codemap")
		if err := os.Mkdir(codemapDir, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove(codemapDir) })

		roots, err := ResolveGlobalRoots(GlobalRootOptions{
			Directory: projectNested,
			SetupRoot: codemapDir,
		}, launchDir)
		if err != nil {
			t.Fatalf("ResolveGlobalRoots() error: %v", err)
		}
		if roots.Setup != setupRoot {
			t.Fatalf("Setup = %q, want %q", roots.Setup, setupRoot)
		}
	})

	t.Run("directory alone shares recovered project setup", func(t *testing.T) {
		roots, err := ResolveGlobalRoots(GlobalRootOptions{Directory: projectNested}, launchDir)
		if err != nil {
			t.Fatalf("ResolveGlobalRoots() error: %v", err)
		}
		if roots.Project != projectRoot || roots.Setup != projectRoot {
			t.Fatalf("roots = %#v, want project and setup %q", roots, projectRoot)
		}
	})

	t.Run("relative setup root resolves after directory", func(t *testing.T) {
		relSetup, err := filepath.Rel(projectRoot, setupNested)
		if err != nil {
			t.Fatal(err)
		}
		roots, err := ResolveGlobalRoots(GlobalRootOptions{
			Directory: projectNested,
			SetupRoot: relSetup,
		}, launchDir)
		if err != nil {
			t.Fatalf("ResolveGlobalRoots() error: %v", err)
		}
		if roots.Setup != setupRoot {
			t.Fatalf("Setup = %q, want %q", roots.Setup, setupRoot)
		}
	})

	t.Run("explicit non repository project is rejected", func(t *testing.T) {
		if _, err := ResolveGlobalRoots(GlobalRootOptions{Directory: t.TempDir()}, launchDir); err == nil {
			t.Fatal("expected explicit non-repository project to be rejected")
		}
	})

	t.Run("explicit non repository setup is rejected", func(t *testing.T) {
		if _, err := ResolveGlobalRoots(GlobalRootOptions{
			Directory: projectNested,
			SetupRoot: t.TempDir(),
		}, launchDir); err == nil {
			t.Fatal("expected explicit non-repository setup to be rejected")
		}
	})

	t.Run("symlinked codemap storage is rejected", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlinks may require elevated privileges")
		}
		target := t.TempDir()
		if err := os.Symlink(target, filepath.Join(setupRoot, ".codemap")); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove(filepath.Join(setupRoot, ".codemap")) })
		if _, err := ResolveGlobalRoots(GlobalRootOptions{
			Directory: projectNested,
			SetupRoot: setupNested,
		}, launchDir); err == nil {
			t.Fatal("expected symlinked .codemap storage to be rejected")
		}
	})

	t.Run("non directory codemap storage is rejected", func(t *testing.T) {
		marker := filepath.Join(setupRoot, ".codemap")
		if err := os.WriteFile(marker, []byte("not a directory"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove(marker) })
		if _, err := ResolveGlobalRoots(GlobalRootOptions{
			Directory: projectNested,
			SetupRoot: setupNested,
		}, launchDir); err == nil {
			t.Fatal("expected non-directory .codemap storage to be rejected")
		}
	})
}
