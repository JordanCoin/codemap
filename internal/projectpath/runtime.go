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
		result.RuntimeDir = dir
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
	for _, dir := range []string{projects, filepath.Join(projects, ProjectKey(selection.ProjectRoot))} {
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
// It never decides where machine state goes: that is always StateCacheDir.
func IsSetUp(projectRoot string) bool {
	info, err := os.Lstat(CodemapDir(projectRoot))
	return err == nil && info.IsDir()
}

// ErrNoStateDir reports that there is nowhere safe to keep machine state (no
// cache dir, not creatable, not writable, or unsafe). Every failure to get the
// private state dir wraps it; callers skip writes and treat state as absent.
var ErrNoStateDir = errors.New("no usable codemap state directory")

// StateCacheDir is the per-user root for machine state of every project:
// $XDG_CACHE_HOME/codemap on every OS when it is absolute, else the OS user
// cache directory. There is deliberately no shared-temp fallback.
func StateCacheDir() (string, bool) {
	base := os.Getenv("XDG_CACHE_HOME")
	if !filepath.IsAbs(base) {
		var err error
		if base, err = os.UserCacheDir(); err != nil || !filepath.IsAbs(base) {
			return "", false
		}
	}
	return filepath.Join(base, "codemap"), true
}

// userStateDir returns <StateCacheDir>/<ProjectKey>, marked with the project
// root, after creating it and its projects/<ProjectKey> runtime dir.
func userStateDir(projectRoot string) (string, error) {
	cacheDir, ok := StateCacheDir()
	if !ok {
		return "", ErrNoStateDir
	}
	dir, err := privateStateDir(cacheDir, ProjectKey(projectRoot))
	if err == nil {
		err = ensureProjectMarker(dir, CanonicalPath(projectRoot))
	}
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrNoStateDir, err)
	}
	return dir, nil
}

// privateStateDir creates <cacheDir>/<key>/projects/<key>, every level a real
// 0700 directory owned by this user. Only the OS cache dir itself may be
// created above that, never its parents, so a missing home stays missing.
// Links above the cache dir belong to the user or the OS and are resolved;
// from the codemap leaf down every level is Lstat-checked and links refused.
func privateStateDir(cacheDir, key string) (string, error) {
	base := filepath.Dir(cacheDir)
	if _, err := os.Stat(base); os.IsNotExist(err) {
		if err := os.Mkdir(base, 0o700); err != nil {
			return "", fmt.Errorf("create user cache %q: %w", base, err)
		}
	}
	dir := filepath.Join(CanonicalPath(base), filepath.Base(cacheDir))
	for _, part := range []string{"", key, "projects", key} {
		dir = filepath.Join(dir, part)
		if err := ensurePrivateDirectory(dir); err != nil {
			return "", err
		}
	}
	return filepath.Dir(filepath.Dir(dir)), nil
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
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		if _, writeErr := f.Write(payload); writeErr != nil {
			_ = f.Close()
			_ = os.Remove(path)
			return writeErr
		}
		if closeErr := f.Close(); closeErr != nil {
			_ = os.Remove(path)
			return closeErr
		}
		return nil
	}
	if !os.IsExist(err) {
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
