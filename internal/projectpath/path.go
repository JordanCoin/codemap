// Package projectpath separates the analyzed project from its Codemap setup.
package projectpath

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Source identifies how Codemap selected setup and runtime storage.
type Source string

const (
	SourceExplicit       Source = "explicit"
	SourceLinkedWorktree Source = "linked-worktree"
	SourceProject        Source = "project"
)

// Selection contains the roots Codemap uses for one analyzed project.
type Selection struct {
	ProjectRoot string
	SetupRoot   string
	RuntimeRoot string
	Source      Source
}

var configuredSetupRoot struct {
	sync.RWMutex
	path string
}

// Select resolves setup policy and mutable runtime storage for one project.
func Select(projectRoot string) (Selection, error) {
	root, err := canonicalProjectRoot(projectRoot)
	if err != nil {
		return Selection{}, fmt.Errorf("resolve analyzed project %q: %w", projectRoot, err)
	}
	if explicit := ConfiguredSetupRoot(); explicit != "" {
		explicit = filepath.Clean(explicit)
		return Selection{ProjectRoot: root, SetupRoot: explicit, RuntimeRoot: explicit, Source: SourceExplicit}, nil
	}

	primary, found, err := linkedWorktreeSetupRoot(root)
	if err != nil {
		return Selection{}, fmt.Errorf("resolve linked worktree setup for analyzed project %q: %w", root, err)
	}
	if err := validateRuntimeStorage(root); err != nil {
		return Selection{}, err
	}
	if found {
		return Selection{ProjectRoot: root, SetupRoot: primary, RuntimeRoot: root, Source: SourceLinkedWorktree}, nil
	}
	return Selection{ProjectRoot: root, SetupRoot: root, RuntimeRoot: root, Source: SourceProject}, nil
}

func validateRuntimeStorage(projectRoot string) error {
	dir := filepath.Join(projectRoot, ".codemap")
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("access runtime Codemap storage %q for analyzed project %q: %w", dir, projectRoot, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("unsafe Codemap storage %q for analyzed project %q: expected a real directory", dir, projectRoot)
	}
	return nil
}

// SetupRoot returns the configured or automatically discovered setup root.
func SetupRoot(projectRoot string) string {
	if explicit := ConfiguredSetupRoot(); explicit != "" {
		return filepath.Clean(explicit)
	}
	selection, err := Select(projectRoot)
	if err == nil {
		return selection.SetupRoot
	}
	return filepath.Clean(projectRoot)
}

// ConfiguredSetupRoot returns the invocation-wide override, if any.
func ConfiguredSetupRoot() string {
	configuredSetupRoot.RLock()
	defer configuredSetupRoot.RUnlock()
	return configuredSetupRoot.path
}

// SetSetupRoot configures the validated setup root for this process.
func SetSetupRoot(root string) {
	configuredSetupRoot.Lock()
	defer configuredSetupRoot.Unlock()
	root = strings.TrimSpace(root)
	if root == "" {
		configuredSetupRoot.path = ""
		return
	}
	configuredSetupRoot.path = filepath.Clean(root)
}

// ResetSetupRoot clears invocation-scoped setup state.
func ResetSetupRoot() {
	SetSetupRoot("")
}

// PrependSetupRootArgs preserves setup selection across Codemap subprocesses.
func PrependSetupRootArgs(args ...string) []string {
	root := ConfiguredSetupRoot()
	result := make([]string, 0, len(args)+2)
	if root != "" {
		result = append(result, "--setup-root", root)
	}
	return append(result, args...)
}

// CodemapDir returns the .codemap directory containing reusable project policy.
func CodemapDir(projectRoot string) string {
	return filepath.Join(SetupRoot(projectRoot), ".codemap")
}

// RuntimeRoot returns the root for mutable state associated with a project.
func RuntimeRoot(projectRoot string) string {
	if explicit := ConfiguredSetupRoot(); explicit != "" {
		return filepath.Clean(explicit)
	}
	selection, err := Select(projectRoot)
	if err == nil {
		return selection.RuntimeRoot
	}
	return filepath.Clean(projectRoot)
}

// RuntimeCodemapDir returns the .codemap directory for mutable project state.
func RuntimeCodemapDir(projectRoot string) string {
	return filepath.Join(RuntimeRoot(projectRoot), ".codemap")
}

func canonicalProjectRoot(root string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	canonicalRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(canonicalRoot)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("expected a directory")
	}
	for current := canonicalRoot; ; current = filepath.Dir(current) {
		marker := filepath.Join(current, ".git")
		markerInfo, markerErr := os.Lstat(marker)
		if markerErr == nil {
			if markerInfo.IsDir() || markerInfo.Mode().IsRegular() {
				return current, nil
			}
			return "", fmt.Errorf("invalid Git marker %q: expected a directory or regular gitfile", marker)
		}
		if !os.IsNotExist(markerErr) {
			return "", markerErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			return canonicalRoot, nil
		}
	}
}

func linkedWorktreeSetupRoot(projectRoot string) (string, bool, error) {
	gitFile := filepath.Join(projectRoot, ".git")
	info, err := os.Lstat(gitFile)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if info.IsDir() {
		return "", false, nil
	}
	if !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("invalid Git marker %q: expected a directory or regular gitfile", gitFile)
	}

	gitDirValue, err := readMetadataPath(gitFile, "gitdir:")
	if err != nil {
		return "", false, err
	}
	gitDir := resolveMetadataPath(filepath.Dir(gitFile), gitDirValue)
	if err := requireRealDirectory(gitDir); err != nil {
		return "", false, fmt.Errorf("invalid gitdir %q: %w", gitDir, err)
	}
	gitDir, err = filepath.EvalSymlinks(gitDir)
	if err != nil {
		return "", false, fmt.Errorf("resolve gitdir %q: %w", gitDir, err)
	}

	commonFile := filepath.Join(gitDir, "commondir")
	if _, err := os.Lstat(commonFile); os.IsNotExist(err) {
		if isStandardSubmoduleGitDir(gitDir) {
			return "", false, nil
		}
		return "", false, err
	} else if err != nil {
		return "", false, err
	}
	commonValue, err := readMetadataPath(commonFile, "")
	if err != nil {
		return "", false, err
	}
	commonDir := resolveMetadataPath(gitDir, commonValue)
	if filepath.Base(commonDir) != ".git" {
		return "", false, fmt.Errorf("commondir %q is not a normal primary .git directory", commonDir)
	}
	if err := requireRealDirectory(commonDir); err != nil {
		return "", false, fmt.Errorf("invalid commondir %q: %w", commonDir, err)
	}
	commonDir, err = filepath.EvalSymlinks(commonDir)
	if err != nil {
		return "", false, fmt.Errorf("resolve commondir %q: %w", commonDir, err)
	}
	if !pathWithin(filepath.Join(commonDir, "worktrees"), gitDir) {
		return "", false, fmt.Errorf("gitdir %q escapes standard worktree metadata %q", gitDir, filepath.Join(commonDir, "worktrees"))
	}

	primary := filepath.Dir(commonDir)
	setupDir := filepath.Join(primary, ".codemap")
	setupInfo, err := os.Lstat(setupDir)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("access primary setup %q for analyzed project %q: %w", setupDir, projectRoot, err)
	}
	if !setupInfo.IsDir() {
		return "", false, fmt.Errorf("unsafe Codemap storage %q for analyzed project %q: expected a real directory", setupDir, projectRoot)
	}
	setup, err := os.Open(setupDir)
	if err != nil {
		return "", false, fmt.Errorf("access primary setup %q for analyzed project %q: %w", setupDir, projectRoot, err)
	}
	_, readErr := setup.Readdirnames(1)
	closeErr := setup.Close()
	if readErr != nil && readErr != io.EOF {
		return "", false, fmt.Errorf("access primary setup %q for analyzed project %q: %w", setupDir, projectRoot, readErr)
	}
	if closeErr != nil {
		return "", false, fmt.Errorf("access primary setup %q for analyzed project %q: %w", setupDir, projectRoot, closeErr)
	}
	return primary, true, nil
}

func isStandardSubmoduleGitDir(gitDir string) bool {
	modulesRoot := ""
	for current := gitDir; ; current = filepath.Dir(current) {
		if filepath.Base(current) == "modules" && filepath.Base(filepath.Dir(current)) == ".git" {
			modulesRoot = current
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false
		}
	}
	rel, err := filepath.Rel(modulesRoot, gitDir)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	head, err := os.Lstat(filepath.Join(gitDir, "HEAD"))
	if err != nil || !head.Mode().IsRegular() {
		return false
	}
	return requireRealDirectory(filepath.Join(gitDir, "objects")) == nil
}

func readMetadataPath(path, prefix string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("invalid metadata %q: expected a regular file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	value, err := parseMetadataPath(data, prefix)
	if err != nil {
		return "", fmt.Errorf("invalid metadata %q: %w", path, err)
	}
	return value, nil
}

func parseMetadataPath(data []byte, prefix string) (string, error) {
	value := strings.TrimSpace(string(data))
	if strings.ContainsAny(value, "\r\n\x00") {
		return "", fmt.Errorf("expected one path")
	}
	if prefix != "" {
		if !strings.HasPrefix(value, prefix) {
			return "", fmt.Errorf("expected %s path", prefix)
		}
		value = strings.TrimSpace(strings.TrimPrefix(value, prefix))
	}
	if value == "" {
		return "", fmt.Errorf("empty path")
	}
	return value, nil
}

func resolveMetadataPath(base, value string) string {
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Clean(filepath.Join(base, value))
}

func requireRealDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("expected a real directory")
	}
	return nil
}

func pathWithin(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != "." && rel != ".." && filepath.Dir(rel) == "." && !filepath.IsAbs(rel)
}
