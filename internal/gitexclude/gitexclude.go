// Package gitexclude keeps codemap's own state directory out of `git status`
// without touching a tracked .gitignore: the rule goes to the clone-local
// <git-common-dir>/info/exclude, which linked worktrees share.
package gitexclude

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// EnsureIgnored ensures Entry is listed in root's clone-local info/exclude.
// It returns the file written, or "" when the entry already exists there or
// root is not a git work tree. It records the local rule even when a tracked
// .gitignore already covers it, so `codemap setup` leaves a rule it owns.
func EnsureIgnored(root string) (string, error) {
	if !IsWorkTree(root) {
		return "", nil
	}
	target, present, err := LocalEntry(root)
	if err != nil {
		return "", err
	}
	if present {
		return "", nil
	}
	if err := AppendEntry(target, Entry); err != nil {
		return "", err
	}
	return target, nil
}

// EnsureIgnoredIfNeeded is the quiet variant used when codemap creates the
// state directory on its own (issue #184): it writes Entry to info/exclude
// only when git does not already ignore .codemap/ by any rule, so a repo
// whose tracked .gitignore covers it gains no redundant local rule. It
// returns the file written, or "" when nothing was needed or root is not a
// git work tree.
func EnsureIgnoredIfNeeded(root string) (string, error) {
	if !IsWorkTree(root) {
		return "", nil
	}
	ignored, _, err := CheckIgnored(root, Entry)
	if err != nil {
		return "", err
	}
	if ignored {
		return "", nil
	}
	return EnsureIgnored(root)
}

// MkdirAll creates dir like os.MkdirAll and, when doing so creates a
// `.codemap` directory (dir itself or an ancestor of it) that did not exist
// before, ensures git ignores it via EnsureIgnoredIfNeeded on the directory
// that contains it. Every codemap state write that may create `.codemap/`
// goes through here so a fresh checkout's `git status` stays clean after
// any command. The ignore write is best-effort: a failure there never fails
// the directory creation, and nothing runs when `.codemap` already exists.
func MkdirAll(dir string, perm os.FileMode) error {
	stateDir, hasStateDir := stateDirOf(dir)
	existed := true
	if hasStateDir {
		_, err := os.Lstat(stateDir)
		existed = err == nil
	}
	if err := os.MkdirAll(dir, perm); err != nil {
		return err
	}
	if hasStateDir && !existed {
		_, _ = EnsureIgnoredIfNeeded(filepath.Dir(stateDir))
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
