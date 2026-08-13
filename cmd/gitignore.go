package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ignoreEntry keeps the runtime-state directory out of git. The trailing
// slash scopes the rule to the directory.
const ignoreEntry = ".codemap/"

// reportEnsureIgnored ensures .codemap/ is ignored for root and reports the
// outcome to w (notice, warning, or nothing).
func reportEnsureIgnored(w io.Writer, root string) {
	wrote, err := ensureCodemapIgnored(root)
	if err != nil {
		fmt.Fprintf(w, "Warning: could not update ignore rules: %v\n", err)
		return
	}
	if wrote == "" {
		return
	}
	rel := wrote
	if r, relErr := filepath.Rel(root, wrote); relErr == nil && !strings.HasPrefix(r, "..") {
		rel = r
	}
	fmt.Fprintf(w, "Added %s to %s\n", ignoreEntry, rel)
}

// ensureCodemapIgnored ensures .codemap/ is ignored for root by writing
// ignoreEntry to the clone-local info/exclude. It returns the file written,
// or "" when the entry already exists or root is not a git work tree.
func ensureCodemapIgnored(root string) (string, error) {
	if !isGitWorkTree(root) {
		return "", nil
	}
	target, present, err := localCodemapIgnore(root)
	if err != nil {
		return "", err
	}
	if present {
		return "", nil
	}
	if err := appendIgnoreEntry(target, ignoreEntry); err != nil {
		return "", err
	}
	return target, nil
}

// localCodemapIgnore reports whether root's info/exclude already lists
// ignoreEntry.
func localCodemapIgnore(root string) (string, bool, error) {
	target, err := infoExcludePath(root)
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
	return target, hasIgnoreLine(data, ignoreEntry), nil
}

// isGitWorkTree reports whether root sits inside a git working tree.
func isGitWorkTree(root string) bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = root
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// gitCheckIgnoreVerbose reports whether git would ignore path at root and, if
// so, the <source>:<line>:<pattern> rule that matched, across every ignore
// source (tracked .gitignore, local exclude, global excludes, parent repos).
func gitCheckIgnoreVerbose(root, path string) (bool, string, error) {
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

// infoExcludePath resolves <git-common-dir>/info/exclude for root, so every
// worktree of a repo shares one local exclude file.
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

// appendIgnoreEntry adds entry on its own line to the ignore file at path,
// creating the file if needed. No-op when entry is already listed; never
// glues onto a partial last line. Atomic write, so a symlinked exclude is
// replaced rather than written through.
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

	return writeFileAtomic(path, buf.Bytes(), 0644)
}

// hasIgnoreLine reports whether data lists entry as a standalone, uncommented
// line.
func hasIgnoreLine(data []byte, entry string) bool {
	for _, line := range bytes.Split(data, []byte("\n")) {
		if strings.TrimSpace(string(line)) == entry {
			return true
		}
	}
	return false
}
