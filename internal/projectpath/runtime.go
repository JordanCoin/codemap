package projectpath

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// RuntimeSelection separates reusable setup policy from mutable project state.
type RuntimeSelection struct {
	ProjectRoot string
	PolicyDir   string
	RuntimeDir  string
	LegacyDir   string
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
		RuntimeDir:  filepath.Join(selection.RuntimeRoot, ".codemap"),
		LegacyDir:   policyDir,
		Source:      selection.Source,
	}
	if selection.Source != SourceExplicit {
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
	if err := ensureProjectMarker(result.RuntimeDir, selection.ProjectRoot); err != nil {
		return RuntimeSelection{}, err
	}
	return result, nil
}

func ensureRealDirectory(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if err := os.Mkdir(path, 0o755); err != nil && !os.IsExist(err) {
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
