package scanner

import (
	"codemap/analysis"
	"fmt"
	"path"
	"sort"
	"strings"
)

// unresolvedImportTally counts imports that name something inside the
// project and resolved to no file, per language. Each one is a missing edge,
// and a missing edge must be declared or an empty answer cannot be told from
// a checked one (#183). Only TypeScript, JavaScript and Python are tallied:
// their specifiers can be told apart from third-party packages by shape.
//
// ponytail: "project-looking" is a shape test, not a resolution. Relative
// specifiers and tsconfig-alias matches are certain; a Python absolute import
// counts only when its first segment is a top-level directory or module (or
// one under src/). An src-layout package imported through another root, or
// a TS baseUrl absolute import, is a known false negative: it stays silent
// rather than flagging clean repos.
type unresolvedImportTally struct {
	topLevel map[string]bool
	seen     map[string]int      // display language -> project-looking imports
	missing  map[string]int      // display language -> those that resolved to nothing
	examples map[string][]string // display language -> up to three "file -> specifier"
	issues   []analysis.Issue    // every miss, one issue each, for the contract
}

func newUnresolvedImportTally(idx *fileIndex) *unresolvedImportTally {
	t := &unresolvedImportTally{topLevel: map[string]bool{}, seen: map[string]int{}, missing: map[string]int{}, examples: map[string][]string{}}
	for dir := range idx.byDir {
		if dir == "" {
			continue
		}
		parts := strings.SplitN(strings.ReplaceAll(dir, "\\", "/"), "/", 3)
		t.topLevel[parts[0]] = true
		if parts[0] == "src" && len(parts) > 1 {
			t.topLevel[parts[1]] = true
		}
	}
	for file := range idx.byExact {
		file = strings.ReplaceAll(file, "\\", "/")
		if dir, base := path.Split(file); dir == "" || dir == "src/" {
			t.topLevel[strings.TrimSuffix(base, path.Ext(base))] = true
		}
	}
	return t
}

func (t *unresolvedImportTally) record(language, from, imp string, aliases map[string][]string, resolved bool) {
	imp = strings.Trim(imp, "\"'`")
	if !t.projectLooking(language, imp, aliases) {
		return
	}
	display := LangDisplay[language]
	t.seen[display]++
	if !resolved {
		t.missing[display]++
		if len(t.examples[display]) < 3 {
			t.examples[display] = append(t.examples[display], from+" -> "+imp)
		}
		// No Line: ImportReference.Line is ast-grep's 0-based start and no
		// surface converts it; a line here would be off by one (#137).
		t.issues = append(t.issues, analysis.Issue{Code: "unresolved-import", Path: from, Message: "import " + imp + " resolved to no file in the project"})
	}
}

func (t *unresolvedImportTally) projectLooking(language, imp string, aliases map[string][]string) bool {
	switch {
	case language == "python":
		if strings.HasPrefix(imp, ".") {
			return true
		}
		first, _, _ := strings.Cut(imp, ".")
		return t.topLevel[first]
	case isJavaScriptLanguage(language):
		// A stylesheet, image or JSON import is not a dependency edge the
		// graph would ever hold, so its absence is not a missing edge.
		if ext := strings.ToLower(path.Ext(imp)); ext != "" && extToLang[ext] == "" {
			return false
		}
		// A path through a hidden directory (./.next/types/x.d.ts) names
		// generated output the walker excludes on purpose; its absence is a
		// choice, not a blind spot.
		for _, segment := range strings.Split(imp, "/") {
			if strings.HasPrefix(segment, ".") && segment != "." && segment != ".." {
				return false
			}
		}
		if imp == "." || imp == ".." || strings.HasPrefix(imp, "./") || strings.HasPrefix(imp, "../") {
			return true
		}
		for pattern := range aliases {
			if prefix, wildcard := strings.CutSuffix(pattern, "*"); (wildcard && strings.HasPrefix(imp, prefix)) || imp == pattern {
				return true
			}
		}
	}
	return false
}

// sources returns one mixed source per language with missing edges, in a
// stable order, in the shape the other coverage notes use.
func (t *unresolvedImportTally) sources() []ScanSourceOutcome {
	var out []ScanSourceOutcome
	for display, missing := range t.missing {
		if missing == 0 {
			continue
		}
		out = append(out, ScanSourceOutcome{
			Name:   strings.ToLower(display) + "-unresolved-imports",
			Status: ScanSourceMixed,
			Detail: fmt.Sprintf("%s: %d of %d project-relative imports did not resolve to a file; those edges are missing (%s)", display, missing, t.seen[display], strings.Join(t.examples[display], ", ")),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
