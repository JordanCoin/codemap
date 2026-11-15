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
// .gitignore (it hides machine state older versions left there; config.json
// and skills/ stay committable) and reports the outcome to w. Only setup,
// config init and skill init call it.
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
		fmt.Fprintln(w, "Wrote .codemap/.gitignore (config.json and skills/ can be committed; anything else there is ignored)")
	}
	reportLegacyState(w, root)
}

// reportLegacyState names machine state older versions left under .codemap/:
// files git tracks (an ignore rule cannot untrack them) with the command that
// would, then untracked leftovers, which nothing reads any more. It prints
// only; it never runs git rm or deletes anything.
func reportLegacyState(w io.Writer, root string) {
	if tracked := trackedMachineState(root); len(tracked) > 0 {
		fmt.Fprintf(w, "%d codemap machine-state file(s) are committed:\n%s", len(tracked), trackedStateDetail(tracked))
	}
	left, unknown := leftoverMachineState(root)
	if len(left) > 0 {
		fmt.Fprintf(w, "%d codemap machine-state file(s) under .codemap/ are no longer used and are safe to delete:\n%s", len(left), nameList(left))
	}
	if len(unknown) > 0 {
		fmt.Fprintf(w, "%d unrecognised file(s) in .codemap/ (not touched by codemap):\n%s", len(unknown), nameList(unknown))
	}
}

// leftoverListMax bounds the leftover list; the count above it is exact.
const leftoverListMax = 20

// leftoverMachineState splits untracked files under .codemap/ other than
// config.json, .gitignore and skills/ into names an older codemap wrote
// (state from before it moved to the per-user state dir) and anything else,
// which may be the user's. Symlinks are listed, never followed.
func leftoverMachineState(root string) (left, unknown []string) {
	dir := projectpath.CodemapDir(root)
	tracked := map[string]bool{}
	files, _ := gitexclude.TrackedStateFiles(root)
	for _, f := range files {
		tracked[f] = true
	}
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(filepath.Dir(dir), path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		switch {
		case userAuthored(rel):
		case isKnownStateFile(rel):
			if !tracked[rel] { // tracked ones are reported with the untrack command
				left = append(left, rel)
			}
		default:
			unknown = append(unknown, rel)
		}
		return nil
	})
	return left, unknown
}

func userAuthored(rel string) bool {
	return rel == ".codemap/config.json" || rel == ".codemap/.gitignore" || strings.HasPrefix(rel, ".codemap/skills/")
}

// nameList prints file names one per line, each byte outside [A-Za-z0-9._/-]
// as \xNN, so a hostile name can neither run when pasted nor fake a line.
func nameList(names []string) string {
	var b strings.Builder
	b.WriteString("  (file names, not commands)\n")
	for i, name := range names {
		if i == leftoverListMax {
			fmt.Fprintf(&b, "  ... %d more\n", len(names)-i)
			break
		}
		b.WriteString("  ")
		for j := 0; j < len(name); j++ {
			if c := name[j]; isPasteSafe(c) {
				b.WriteByte(c)
			} else {
				fmt.Fprintf(&b, `\x%02x`, c)
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func isPasteSafe(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("._/-", c) >= 0
}

// trackedStateDetail lists tracked state files as data, one per line, then a
// paste-safe untrack command. Names are repo-controlled, so the command
// carries only those matching knownStateFile, whatever the caller passed.
func trackedStateDetail(tracked []string) string {
	var safe []string
	for _, f := range tracked {
		if isKnownStateFile(f) {
			safe = append(safe, f)
		}
	}
	detail := nameList(tracked)
	if len(safe) > 0 {
		detail += fmt.Sprintf("  untrack with: git rm --cached -- %s\n", strings.Join(safe, " "))
	}
	return detail
}

func isKnownStateFile(rel string) bool {
	return knownStateFile.MatchString(rel) && !strings.Contains(rel, "..")
}

// knownStateFile matches the machine-state paths codemap writes, in the
// characters [A-Za-z0-9._/-] only.
var knownStateFile = regexp.MustCompile(`^\.codemap/(state\.json|status|hubs\.txt|agent_edits\.jsonl|events\.log|watch\.pid|watch\.transition|project\.json|sessions\.lock|topology-state\.json|handoff\.[A-Za-z0-9_-]+\.(json|log)|(sessions|projects|runtime)/[A-Za-z0-9._/-]+)$`)

// trackedMachineState is the tracked files under .codemap/ with a known
// machine-state name. Other tracked files (config.json, skills/, or anything
// codemap does not recognise) are the user's.
func trackedMachineState(root string) []string {
	files, err := gitexclude.TrackedStateFiles(root)
	if err != nil {
		return nil
	}
	var state []string
	for _, f := range files {
		if isKnownStateFile(f) {
			state = append(state, f)
		}
	}
	return state
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
