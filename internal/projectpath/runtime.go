package projectpath

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// RuntimeSelection separates reusable setup policy from mutable project state.
type RuntimeSelection struct {
	ProjectRoot string
	PolicyDir   string
	RuntimeDir  string
	ProjectDir  string // this project's state: RuntimeDir, or RuntimeDir/projects/<key> under --setup-root
	Source      Source
}

type projectMarker struct {
	CanonicalRoot string `json:"canonical_root"`
}

// SelectRuntime resolves and validates mutable runtime storage for one project.
func SelectRuntime(projectRoot string) (RuntimeSelection, error) {
	selection, err := Select(projectRoot)
	if err != nil {
		return RuntimeSelection{}, err
	}
	policyDir := filepath.Join(selection.SetupRoot, ".codemap")
	result := RuntimeSelection{
		ProjectRoot: selection.ProjectRoot,
		PolicyDir:   policyDir,
		Source:      selection.Source,
	}
	if selection.Source != SourceExplicit {
		dir, err := userStateDir(selection.ProjectRoot)
		if err != nil {
			return RuntimeSelection{}, err
		}
		result.RuntimeDir, result.ProjectDir = dir, dir
		return result, nil
	}

	digest := sha256.Sum256([]byte(selection.ProjectRoot))
	runtimeRoot := filepath.Join(policyDir, "runtime")
	result.RuntimeDir = filepath.Join(runtimeRoot, hex.EncodeToString(digest[:]))
	for _, dir := range []string{policyDir, runtimeRoot, result.RuntimeDir} {
		if err := ensureRealDirectory(dir); err != nil {
			return RuntimeSelection{}, err
		}
	}
	projects := filepath.Join(result.RuntimeDir, "projects")
	result.ProjectDir = filepath.Join(projects, ProjectKey(selection.ProjectRoot))
	for _, dir := range []string{projects, result.ProjectDir} {
		if err := ensurePrivateDirectory(dir); err != nil {
			return RuntimeSelection{}, err
		}
	}
	if err := ensureProjectMarker(result.RuntimeDir, selection.ProjectRoot); err != nil {
		return RuntimeSelection{}, err
	}
	return result, nil
}

// IsSetUp reports whether the project's policy directory (<setup root>/.codemap)
// exists. Only explicit commands (setup, config init, skill init) create it.
// It never decides where machine state goes: that is always UserDir/state.
func IsSetUp(projectRoot string) bool {
	info, err := os.Lstat(CodemapDir(projectRoot))
	return err == nil && info.IsDir()
}

// ErrNoStateDir reports that there is nowhere safe to keep machine state (no
// home dir, not creatable, not writable, or unsafe). Every failure to get the
// private state dir wraps it; callers skip writes and treat state as absent.
var ErrNoStateDir = errors.New("no usable codemap state directory")

// UserDir is the per-user codemap base, ~/.codemap: the daemon registry
// (daemons/) and every project's machine state (state/<key>/) live under one
// root, so every process of this user agrees on both whatever its
// environment. It is not a cache: the OS may purge those, and a handoff is
// not disposable. There is deliberately no shared-temp fallback.
func UserDir() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return "", false
	}
	return filepath.Join(home, ".codemap"), true
}

// EnsureUserSubdir creates UserDir/<names...>, returning its canonical path.
// It creates ~/.codemap 0700 when missing but never a missing home. A link at
// ~/.codemap is the user's and is resolved; from <name> down every level is
// Lstat-checked, links refused, and it must be a 0700 dir this user owns.
func EnsureUserSubdir(names ...string) (string, error) {
	base, ok := UserDir()
	if !ok {
		return "", ErrNoStateDir
	}
	if _, err := os.Stat(base); os.IsNotExist(err) {
		if err := os.Mkdir(base, 0o700); err != nil && !os.IsExist(err) {
			return "", fmt.Errorf("%w: create %q: %w", ErrNoStateDir, base, err)
		}
	}
	dir := CanonicalPath(base)
	for _, name := range names {
		dir = filepath.Join(dir, name)
		if err := ensurePrivateDirectory(dir); err != nil {
			return "", fmt.Errorf("%w: %w", ErrNoStateDir, err)
		}
	}
	return dir, nil
}

// userStateDir returns ~/.codemap/state/<ProjectKey>, marked with the
// project root.
func userStateDir(projectRoot string) (string, error) {
	dir, err := EnsureUserSubdir("state", ProjectKey(projectRoot))
	if err != nil {
		return "", err
	}
	if err := ensureProjectMarker(dir, CanonicalPath(projectRoot)); err != nil {
		return "", fmt.Errorf("%w: %w", ErrNoStateDir, err)
	}
	return dir, nil
}

func ensureRealDirectory(path string) error {
	return ensureDirectory(path, 0o755)
}

// ensurePrivateDirectory is ensureRealDirectory for per-user state: created
// 0700, owned by this user, and narrowed to 0700 when it was wider.
func ensurePrivateDirectory(path string) error {
	if err := ensureDirectory(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !ownedByCurrentUser(info) {
		return fmt.Errorf("unsafe runtime directory %q: not owned by the current user", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return os.Chmod(path, 0o700)
	}
	return nil
}

func ensureDirectory(path string, perm os.FileMode) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if err := os.Mkdir(path, perm); err != nil && !os.IsExist(err) {
			return fmt.Errorf("create runtime directory %q: %w", path, err)
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return fmt.Errorf("inspect runtime directory %q: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("unsafe runtime directory %q: expected a real directory", path)
	}
	return nil
}

func ensureProjectMarker(runtimeDir, canonicalRoot string) error {
	path := filepath.Join(runtimeDir, "project.json")
	payload, err := json.Marshal(projectMarker{CanonicalRoot: canonicalRoot})
	if err != nil {
		return err
	}
	// Written aside and hard-linked into place: the link is exclusive and
	// atomic, so a concurrent reader never sees an empty marker.
	f, err := os.CreateTemp(runtimeDir, ".project-*.json")
	if err != nil {
		return fmt.Errorf("create runtime identity %q: %w", path, err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	_, err = f.Write(payload)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Link(tmp, path); err == nil {
		return nil
	} else if !os.IsExist(err) {
		return fmt.Errorf("create runtime identity %q: %w", path, err)
	}
	info, statErr := os.Lstat(path)
	if statErr != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("unsafe runtime identity %q", path)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		return fmt.Errorf("read runtime identity %q: %w", path, readErr)
	}
	var marker projectMarker
	if json.Unmarshal(data, &marker) != nil || marker.CanonicalRoot != canonicalRoot {
		return fmt.Errorf("runtime identity mismatch in %q", path)
	}
	return nil
}
