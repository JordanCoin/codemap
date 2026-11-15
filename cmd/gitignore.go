package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"codemap/internal/gitexclude"
	"codemap/internal/projectpath"
)

// ignoreEntry keeps the runtime-state directory out of git. The trailing
// slash scopes the rule to the directory.
const ignoreEntry = gitexclude.Entry

// reportEnsureIgnored ensures the project's .codemap/ carries its own
// .gitignore (machine state ignored, config.json and skills/ committable) and
// reports the outcome to w (notice, warning, or nothing).
func reportEnsureIgnored(w io.Writer, root string) {
	dir := projectpath.CodemapDir(root)
	if _, err := os.Stat(dir); err != nil {
		return
	}
	wrote, err := gitexclude.EnsureStateIgnore(dir)
	if err != nil {
		fmt.Fprintf(w, "Warning: could not write %s: %v\n", filepath.Join(dir, ".gitignore"), err)
		return
	}
	if wrote {
		fmt.Fprintln(w, "Wrote .codemap/.gitignore (machine state ignored; config.json and skills/ can be committed)")
	}
	reportTrackedState(w, root)
}

// reportTrackedState names machine-state files git already tracks under
// .codemap/ (an ignore rule cannot untrack them) and the command that would.
// It prints only; it never runs git rm.
func reportTrackedState(w io.Writer, root string) {
	tracked := trackedMachineState(root)
	if len(tracked) == 0 {
		return
	}
	fmt.Fprintf(w, "%d codemap machine-state file(s) are committed; untrack them with:\n  %s\n", len(tracked), untrackCommand(tracked))
}

// trackedMachineState is gitexclude.TrackedStateFiles minus the user-authored
// files (config.json, skills/) that are meant to be committed.
func trackedMachineState(root string) []string {
	files, err := gitexclude.TrackedStateFiles(root)
	if err != nil {
		return nil
	}
	var state []string
	for _, f := range files {
		if f != ".codemap/config.json" && f != ".codemap/.gitignore" && !strings.HasPrefix(f, ".codemap/skills/") {
			state = append(state, f)
		}
	}
	return state
}

func untrackCommand(files []string) string {
	return "git rm --cached -- " + strings.Join(files, " ")
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
