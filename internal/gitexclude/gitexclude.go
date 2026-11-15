// Package gitexclude keeps codemap's machine state out of `git status`
// without touching the repo's own ignore files: an in-repo .codemap/ carries
// its own .gitignore that leaves only config.json and skills/ committable.
package gitexclude

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"codemap/internal/runtimefile"
)

// Entry is the ignore rule written for codemap's state directory. The
// trailing slash scopes the rule to the directory.
const Entry = ".codemap/"

// stateDirName is the directory Entry refers to.
const stateDirName = ".codemap"

// IsWorkTree reports whether root sits inside a git working tree.
func IsWorkTree(root string) bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = root
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// InfoExcludePath resolves <git-common-dir>/info/exclude for root, so every
// worktree of a repo shares one local exclude file.
func InfoExcludePath(root string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--git-common-dir")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --git-common-dir: %w", err)
	}
	commonDir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(root, commonDir)
	}
	return filepath.Join(commonDir, "info", "exclude"), nil
}

// CheckIgnored reports whether git would ignore path at root and, if so, the
// <source>:<line>:<pattern> rule that matched, across every ignore source
// (tracked .gitignore, local exclude, global excludes, parent repos).
func CheckIgnored(root, path string) (bool, string, error) {
	cmd := exec.Command("git", "check-ignore", "-v", path)
	cmd.Dir = root
	out, err := cmd.Output()
	if err == nil {
		line := strings.TrimSpace(string(out))
		if i := strings.IndexByte(line, '\t'); i >= 0 {
			line = line[:i]
		}
		return true, line, nil
	}
	// Exit code 1 means "not ignored"; anything else is a real error.
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
		return false, "", nil
	}
	return false, "", fmt.Errorf("git check-ignore: %w", err)
}

// LocalEntry reports whether root's info/exclude already lists Entry, and
// the path of that file.
func LocalEntry(root string) (string, bool, error) {
	target, err := InfoExcludePath(root)
	if err != nil {
		return "", false, err
	}
	data, err := os.ReadFile(target)
	if os.IsNotExist(err) {
		return target, false, nil
	}
	if err != nil {
		return "", false, err
	}
	return target, HasLine(data, Entry), nil
}

// StateIgnore is the .codemap/.gitignore codemap writes: machine state stays
// out of git while config.json and skills/ stay committable.
const StateIgnore = `# Written by codemap. Machine state stays out of git; config and skills can be committed.
*
!.gitignore
!config.json
!skills/
!skills/**
`

// EnsureStateIgnore writes stateDir/.gitignore with StateIgnore when the file
// is missing; a user's copy is never touched. It returns whether it wrote.
func EnsureStateIgnore(stateDir string) (bool, error) {
	path := filepath.Join(stateDir, ".gitignore")
	if _, err := os.Lstat(path); err == nil || !os.IsNotExist(err) {
		return false, err
	}
	if err := runtimefile.WriteAtomic(path, []byte(StateIgnore), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// MkdirAll creates dir like os.MkdirAll and, when dir is or sits inside a
// `.codemap` directory, ensures that directory carries StateIgnore. Every
// in-repo codemap state write goes through here, so machine state never
// shows in `git status`. The ignore write is best-effort: a failure there
// never fails the directory creation.
func MkdirAll(dir string, perm os.FileMode) error {
	if err := os.MkdirAll(dir, perm); err != nil {
		return err
	}
	if stateDir, ok := stateDirOf(dir); ok {
		_, _ = EnsureStateIgnore(stateDir)
	}
	return nil
}

// stateDirOf returns the nearest path on or above dir whose base is
// `.codemap`.
func stateDirOf(dir string) (string, bool) {
	dir = filepath.Clean(dir)
	for current := dir; ; current = filepath.Dir(current) {
		if filepath.Base(current) == stateDirName {
			return current, true
		}
		if parent := filepath.Dir(current); parent == current {
			return "", false
		}
	}
}

// TrackedStateFiles returns the paths under .codemap/ that git tracks at
// root (`git ls-files -- .codemap`), which is the state the ignore rule can
// no longer hide.
func TrackedStateFiles(root string) ([]string, error) {
	cmd := exec.Command("git", "ls-files", "-z", "--", stateDirName)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	var files []string
	for _, entry := range bytes.Split(out, []byte{0}) {
		if len(entry) > 0 {
			files = append(files, string(entry))
		}
	}
	return files, nil
}

// AppendEntry adds entry on its own line to the ignore file at path,
// creating the file if needed. No-op when entry is already listed; never
// glues onto a partial last line. Atomic write, so a symlinked exclude is
// replaced rather than written through.
func AppendEntry(path, entry string) error {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if HasLine(existing, entry) {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	var buf bytes.Buffer
	buf.Write(existing)
	if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		buf.WriteByte('\n')
	}
	buf.WriteString(entry)
	buf.WriteByte('\n')

	return writeFileAtomic(path, buf.Bytes(), 0644)
}

// HasLine reports whether data lists entry as a standalone, uncommented
// line.
func HasLine(data []byte, entry string) bool {
	for _, line := range bytes.Split(data, []byte("\n")) {
		if strings.TrimSpace(string(line)) == entry {
			return true
		}
	}
	return false
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
