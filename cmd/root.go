package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"codemap/internal/projectpath"
)

// GlobalRootOptions are invocation-wide roots extracted before command parsing.
type GlobalRootOptions struct {
	Directory string
	SetupRoot string
}

// Active reports whether this invocation overrides either root.
func (o GlobalRootOptions) Active() bool {
	return o.Directory != "" || o.SetupRoot != ""
}

// InvocationRoots separates the repository being analyzed from the repository
// whose Codemap setup and state are reused.
type InvocationRoots struct {
	Project string
	Setup   string
	Runtime string
	Source  projectpath.Source
}

// ParseGlobalRootOptions extracts root options wherever they appear before --.
func ParseGlobalRootOptions(args []string) (GlobalRootOptions, []string, error) {
	var opts GlobalRootOptions
	remaining := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			remaining = append(remaining, args[i:]...)
			break
		}

		switch {
		case arg == "-C" || arg == "--project-root":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" || isGlobalRootOption(args[i+1]) {
				return GlobalRootOptions{}, nil, fmt.Errorf("%s requires a path", arg)
			}
			i++
			opts.Directory = args[i]
		case strings.HasPrefix(arg, "--project-root="):
			opts.Directory = strings.TrimPrefix(arg, "--project-root=")
			if strings.TrimSpace(opts.Directory) == "" {
				return GlobalRootOptions{}, nil, fmt.Errorf("--project-root requires a path")
			}
		case arg == "--setup-root":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" || isGlobalRootOption(args[i+1]) {
				return GlobalRootOptions{}, nil, fmt.Errorf("--setup-root requires a path")
			}
			i++
			opts.SetupRoot = args[i]
		case strings.HasPrefix(arg, "--setup-root="):
			opts.SetupRoot = strings.TrimPrefix(arg, "--setup-root=")
			if strings.TrimSpace(opts.SetupRoot) == "" {
				return GlobalRootOptions{}, nil, fmt.Errorf("--setup-root requires a path")
			}
		default:
			remaining = append(remaining, arg)
		}
	}

	return opts, remaining, nil
}

func isGlobalRootOption(arg string) bool {
	return arg == "-C" || arg == "--project-root" || arg == "--setup-root" ||
		strings.HasPrefix(arg, "--project-root=") || strings.HasPrefix(arg, "--setup-root=")
}

// ResolveGlobalRoots resolves both inputs with nearest-repository recovery.
// Relative setup roots are interpreted after -C, from the recovered project.
func ResolveGlobalRoots(opts GlobalRootOptions, launchDir string) (InvocationRoots, error) {
	projectInput := opts.Directory
	if projectInput == "" {
		projectInput = launchDir
	} else if !filepath.IsAbs(projectInput) {
		projectInput = filepath.Join(launchDir, projectInput)
	}

	projectRoot, projectFound, err := ResolveNearestGitRoot(projectInput)
	if err != nil {
		return InvocationRoots{}, fmt.Errorf("resolve project root: %w", err)
	}
	if opts.Directory != "" && !projectFound {
		return InvocationRoots{}, fmt.Errorf("resolve project root: %q is not inside a Git repository", projectInput)
	}
	projectSelection, err := projectpath.Select(projectRoot)
	if err != nil {
		return InvocationRoots{}, fmt.Errorf("resolve project root: %w", err)
	}

	setupRoot := projectRoot
	runtimeRoot := projectRoot
	source := projectpath.SourceProject
	if opts.SetupRoot != "" {
		setupInput := opts.SetupRoot
		if !filepath.IsAbs(setupInput) {
			setupInput = filepath.Join(projectRoot, setupInput)
		}
		var setupFound bool
		setupRoot, setupFound, err = ResolveNearestGitRoot(setupInput)
		if err != nil {
			return InvocationRoots{}, fmt.Errorf("resolve setup root: %w", err)
		}
		if !setupFound {
			return InvocationRoots{}, fmt.Errorf("resolve setup root: %q is not inside a Git repository", setupInput)
		}
		runtimeRoot = setupRoot
		source = projectpath.SourceExplicit
	} else if projectSelection.Source == projectpath.SourceLinkedWorktree {
		// Linked worktrees resolve to the physical primary and worktree roots.
		projectRoot = projectSelection.ProjectRoot
		setupRoot = projectSelection.SetupRoot
		runtimeRoot = projectSelection.RuntimeRoot
		source = projectpath.SourceLinkedWorktree
	}
	if err := validateCodemapStorageRoot(setupRoot); err != nil {
		return InvocationRoots{}, fmt.Errorf("resolve setup root: %w", err)
	}

	return InvocationRoots{Project: projectRoot, Setup: setupRoot, Runtime: runtimeRoot, Source: source}, nil
}

func validateCodemapStorageRoot(root string) error {
	dir := filepath.Join(root, ".codemap")
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("unsafe Codemap storage %q: expected a real directory", dir)
	}
	return nil
}

// ValidateProjectPath returns the caller's absolute path only after automatic
// project selection succeeds. Keeping the exact path preserves commands that
// intentionally analyze a repository subdirectory.
func ValidateProjectPath(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if _, err := projectpath.Select(absPath); err != nil {
		return "", err
	}
	return absPath, nil
}

// ResolveNearestGitRoot returns the nearest ancestor directory that contains a
// .git entry. It accepts missing descendants and both .git directories and
// .git files used by linked worktrees. When no repository root exists, it
// returns the absolute input path and found=false without resolving symlinks.
func ResolveNearestGitRoot(path string) (resolved string, found bool, err error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", false, err
	}
	absPath = filepath.Clean(absPath)
	var existingPath string
	for current := absPath; ; current = filepath.Dir(current) {
		info, statErr := os.Stat(current)
		if statErr == nil {
			if !info.IsDir() {
				return "", false, fmt.Errorf("%q is not a directory", path)
			}
			existingPath = current
			break
		}
		if !os.IsNotExist(statErr) {
			return "", false, statErr
		}
		if _, lstatErr := os.Lstat(current); lstatErr == nil {
			return "", false, fmt.Errorf("%q is not a directory", path)
		} else if !os.IsNotExist(lstatErr) {
			return "", false, lstatErr
		}
		if filepath.Dir(current) == current {
			break
		}
	}

	physicalPath, err := filepath.EvalSymlinks(existingPath)
	if err != nil {
		return "", false, err
	}
	physicalPath = filepath.Clean(physicalPath)
	for current := physicalPath; ; current = filepath.Dir(current) {
		valid, err := validGitMarker(current)
		if err != nil {
			return "", false, err
		}
		if valid {
			return logicalRootForPhysical(existingPath, physicalPath, current), true, nil
		}

		parent := filepath.Dir(current)
		if parent == current {
			return absPath, false, nil
		}
	}
}

func logicalRootForPhysical(logicalPath, physicalPath, physicalRoot string) string {
	rel, err := filepath.Rel(physicalRoot, physicalPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return physicalRoot
	}
	logicalRoot := logicalPath
	for remaining := rel; remaining != "."; remaining = filepath.Dir(remaining) {
		logicalRoot = filepath.Dir(logicalRoot)
	}
	resolved, err := filepath.EvalSymlinks(logicalRoot)
	if err == nil && filepath.Clean(resolved) == physicalRoot {
		return logicalRoot
	}
	return physicalRoot
}

func validGitMarker(root string) (bool, error) {
	marker := filepath.Join(root, ".git")
	info, err := os.Lstat(marker)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.IsDir() {
		return true, nil
	}
	if info.Mode().IsRegular() {
		data, err := os.ReadFile(marker)
		if err != nil {
			return false, err
		}
		gitDir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
		gitDir = strings.TrimSpace(gitDir)
		if !ok || gitDir == "" {
			return false, fmt.Errorf("invalid Git marker %q: expected gitdir target", marker)
		}
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(root, gitDir)
		}
		target, err := os.Stat(gitDir)
		if err != nil {
			return false, fmt.Errorf("invalid Git marker %q: %w", marker, err)
		}
		if !target.IsDir() {
			return false, fmt.Errorf("invalid Git marker %q: gitdir target is not a directory", marker)
		}
		return true, nil
	}
	return false, fmt.Errorf("invalid Git marker %q: expected a directory or regular gitfile", marker)
}
