package cmd

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ignoreMode selects where ensureCodemapIgnored writes a rule when one is
// needed.
type ignoreMode int

const (
	// ignoreTracked writes ".codemap/" to the repo's tracked .gitignore so the
	// whole team (and every future clone) ignores it. This creates a diff.
	ignoreTracked ignoreMode = iota
	// ignoreLocal writes ".codemap/" to .git/info/exclude, which is local to
	// this clone and never tracked — no diff, no surprise for collaborators.
	ignoreLocal
)

// ignoreEntry is the rule we add. The trailing slash scopes it to the
// directory, matching how codemap's own repo ignores its runtime state.
const ignoreEntry = ".codemap/"

// reportEnsureIgnored ensures .codemap/ is ignored for root and reports the
// outcome to w: a one-line notice when a rule was added, a warning when the
// check/write failed, and nothing when git already ignored it (or root is not
// a git repo). Both "config init" and "setup" funnel through here.
func reportEnsureIgnored(w io.Writer, root string, mode ignoreMode) {
	wrote, err := ensureCodemapIgnored(root, mode)
	if err != nil {
		fmt.Fprintf(w, "Warning: could not update ignore rules: %v\n", err)
		return
	}
	if wrote == "" {
		return
	}
	rel := wrote
	if r, relErr := filepath.Rel(root, wrote); relErr == nil {
		rel = r
	}
	fmt.Fprintf(w, "Added %s to %s\n", ignoreEntry, rel)
}

// ensureCodemapIgnored makes sure the .codemap/ directory at the given git root
// is ignored by git. It is a no-op when git already ignores .codemap through
// any mechanism (a global exclude, an existing .gitignore, a parent rule), and
// a silent no-op when root is not inside a git working tree.
//
// It returns the path of the file it wrote to, or "" when nothing was written.
func ensureCodemapIgnored(root string, mode ignoreMode) (string, error) {
	if !isGitWorkTree(root) {
		return "", nil
	}

	ignored, err := gitCheckIgnore(root, ignoreEntry)
	if err != nil {
		return "", err
	}
	if ignored {
		return "", nil
	}

	var target string
	if mode == ignoreLocal {
		target, err = infoExcludePath(root)
		if err != nil {
			return "", err
		}
	} else {
		target = filepath.Join(root, ".gitignore")
	}

	if err := appendIgnoreEntry(target, ignoreEntry); err != nil {
		return "", err
	}
	return target, nil
}

// isGitWorkTree reports whether root sits inside a git working tree.
func isGitWorkTree(root string) bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = root
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// gitCheckIgnore reports whether git would ignore the given path at root.
func gitCheckIgnore(root, path string) (bool, error) {
	cmd := exec.Command("git", "check-ignore", "-q", path)
	cmd.Dir = root
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	// Exit code 1 means "not ignored"; anything else is a real error.
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("git check-ignore: %w", err)
}

// infoExcludePath resolves <git-common-dir>/info/exclude for root, so all
// worktrees of a repo share one local exclude file.
func infoExcludePath(root string) (string, error) {
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

// appendIgnoreEntry adds entry as its own line in the ignore file at path,
// creating the file (and its parent dir) if needed. It leaves the file
// untouched if entry is already present verbatim, and guarantees the entry
// lands on a fresh line even when the file lacks a trailing newline.
func appendIgnoreEntry(path, entry string) error {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if hasIgnoreLine(existing, entry) {
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

	return os.WriteFile(path, buf.Bytes(), 0644)
}

// hasIgnoreLine reports whether data already contains entry as a standalone,
// non-comment line.
func hasIgnoreLine(data []byte, entry string) bool {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == entry {
			return true
		}
	}
	return false
}
