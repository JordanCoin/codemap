package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
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
	fmt.Fprintf(w, "%d codemap machine-state file(s) are committed:\n%s", len(tracked), trackedStateDetail(tracked))
}

// trackedStateDetail lists tracked files as quoted data, one per line, then a
// paste-safe untrack command. File names are repo-controlled, so the command
// carries only names matching a known state file; anything else is listed
// for the user to handle by hand.
func trackedStateDetail(tracked []string) string {
	var b strings.Builder
	var safe []string
	for _, f := range tracked {
		fmt.Fprintf(&b, "  %q\n", f)
		if knownStateFile.MatchString(f) && !strings.Contains(f, "..") {
			safe = append(safe, f)
		}
	}
	if len(safe) > 0 {
		fmt.Fprintf(&b, "  untrack with: git rm --cached -- %s\n", strings.Join(safe, " "))
	}
	if len(safe) < len(tracked) {
		fmt.Fprintf(&b, "  %d unrecognised name(s) above are left out of the command; untrack them by hand if they are codemap state\n", len(tracked)-len(safe))
	}
	return b.String()
}

// knownStateFile matches the machine-state paths codemap writes, in the
// characters [A-Za-z0-9._/-] only.
var knownStateFile = regexp.MustCompile(`^\.codemap/(state\.json|status|hubs\.txt|agent_edits\.jsonl|events\.log|watch\.pid|sessions\.lock|topology-state\.json|handoff\.[A-Za-z0-9_-]+\.(json|log)|(sessions|projects|runtime)/[A-Za-z0-9._/-]+)$`)

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
