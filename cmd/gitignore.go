package cmd

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"codemap/internal/gitexclude"
)

// ignoreEntry keeps the runtime-state directory out of git. The trailing
// slash scopes the rule to the directory.
const ignoreEntry = gitexclude.Entry

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
	return gitexclude.EnsureIgnored(root)
}

// localCodemapIgnore reports whether root's info/exclude already lists
// ignoreEntry.
func localCodemapIgnore(root string) (string, bool, error) {
	return gitexclude.LocalEntry(root)
}

// isGitWorkTree reports whether root sits inside a git working tree.
func isGitWorkTree(root string) bool {
	return gitexclude.IsWorkTree(root)
}

// gitCheckIgnoreVerbose reports whether git would ignore path at root and, if
// so, the <source>:<line>:<pattern> rule that matched.
func gitCheckIgnoreVerbose(root, path string) (bool, string, error) {
	return gitexclude.CheckIgnored(root, path)
}

// infoExcludePath resolves <git-common-dir>/info/exclude for root.
func infoExcludePath(root string) (string, error) {
	return gitexclude.InfoExcludePath(root)
}

// appendIgnoreEntry adds entry on its own line to the ignore file at path.
func appendIgnoreEntry(path, entry string) error {
	return gitexclude.AppendEntry(path, entry)
}

// hasIgnoreLine reports whether data lists entry as a standalone, uncommented
// line.
func hasIgnoreLine(data []byte, entry string) bool {
	return gitexclude.HasLine(data, entry)
}
