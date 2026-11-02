package scanner

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"codemap/analysis"
)

var (
	phpNamespaceStatement = regexp.MustCompile(`(?m)^\s*(?:<\?php\s+)?namespace\s+[^\s;{]+\s*[;{]`)
	phpBlockComment       = regexp.MustCompile(`(?s)/\*.*?\*/`)
	rubyGemfileAutoloader = regexp.MustCompile(`(?m)^\s*gem\s*\(?\s*['"](rails|railties|zeitwerk)['"]`)
	// A development dependency tests against Rails; it does not load this gem.
	rubyGemspecAutoloader = regexp.MustCompile(`(?m)^\s*\w+\.add_(?:runtime_)?dependency\s*\(?\s*['"](rails|railties|zeitwerk)['"]`)
	rubyZeitwerkLoader    = regexp.MustCompile(`(?m)^[^#\n]*\bZeitwerk::Loader\b`)
	rubyStringLiteral     = regexp.MustCompile(`"(?:[^"\\\n]|\\.)*"|'(?:[^'\\\n]|\\.)*'`)
	bashDynamicSource     = regexp.MustCompile("^\\s*(?:(?:then|do|else|if|elif|while|until|!|[^\\s()]+\\))\\s+|[({]\\s*)*(source|\\.|load)\\s+(?:\"[^\"\\s]*[$`]|[^\\s'\"\\\\]*[$`]|<\\()")
	bashHeredocStart      = regexp.MustCompile(`^<<-?[ \t]*(?:'([^']+)'|"([^"]+)"|\\?([^\s;&|<>()'"]+))`)
)

// maxEvidenceFileBytes bounds what one file can cost a graph build. A file past
// it is reported by name, because evidence it might hold went unseen.
const maxEvidenceFileBytes = 8 << 20

// FileCoverage is ForFile plus what the graph knows about the file itself: a file
// outside the scanned set, or one with no language mapping, cannot read complete
// however complete the scan of everything else was. A graph with no inventory
// (KnownFiles nil) or no scan behind it says nothing more about the file.
func (fg *FileGraph) FileCoverage(file string) GraphCoverage {
	coverage := fg.Coverage.ForFile(file)
	if fg.KnownFiles == nil || coverage.EffectiveStatus() == analysis.CoverageUnavailable {
		return coverage
	}
	var detail string
	switch {
	case !fg.KnownFiles[file]:
		detail = file + " is not in the scanned file set (ignored, excluded by only/exclude in .codemap/config.json, or missing): its imports and importers are unknown, not 0"
	case DetectLanguage(file) == "":
		detail = file + " has no language mapping: its own imports are not extracted, and its importers are only scanned files that name it by path"
	default:
		return coverage
	}
	coverage.Sources = slices.Clone(coverage.Sources)
	coverage.Notes = slices.Clone(coverage.Notes)
	coverage.AddSource(ScanSourceOutcome{Name: "file-scope", Status: ScanSourceMixed, Detail: detail})
	return coverage
}

// missingEdgeSources names languages whose repo shows they reach other files by
// a route the import graph does not follow. Each source is tagged with its
// language so ForFile keeps other languages' files complete. phpUnresolvedUse is
// the first "file -> name" PHP use statement that resolved to no file.
func missingEdgeSources(absRoot string, files []FileInfo, phpUnresolvedUse string, idx *fileIndex, batsReached map[string]bool) []ScanSourceOutcome {
	byLanguage := make(map[string][]string)
	for _, file := range files {
		language := fileInfoLanguage(file)
		byLanguage[language] = append(byLanguage[language], filepath.ToSlash(file.Path))
	}
	for _, paths := range byLanguage {
		sort.Strings(paths)
	}

	var sources []ScanSourceOutcome
	if php := byLanguage["php"]; len(php) > 0 {
		if evidence := phpAutoloadEvidence(absRoot, php, phpUnresolvedUse); evidence != "" {
			sources = append(sources, ScanSourceOutcome{
				Name:      "php-autoload",
				Status:    ScanSourceMixed,
				Detail:    "PHP: class references through autoloading are not resolved (" + evidence + "): search the class name before treating 0 importers as unused",
				Languages: []string{"php"},
			})
		}
	}
	if ruby := byLanguage["ruby"]; len(ruby) > 0 {
		if evidence := rubyAutoloadEvidence(absRoot, ruby); evidence != "" {
			sources = append(sources, ScanSourceOutcome{
				Name:      "ruby-autoload",
				Status:    ScanSourceMixed,
				Detail:    "Ruby: autoloaded constant references are not resolved (" + evidence + "): search the constant name before treating 0 importers as unused",
				Languages: []string{"ruby"},
			})
		}
	}
	if evidence := bashDynamicSourceEvidence(absRoot, byLanguage["bash"], idx, batsReached); evidence != "" {
		sources = append(sources, ScanSourceOutcome{
			Name:      "bash-dynamic-source",
			Status:    ScanSourceMixed,
			Detail:    "Bash: source, . or load with a dynamic argument resolves only when its literal remainder names one shell file (" + evidence + "): search the file name before treating 0 importers as unused",
			Languages: []string{"bash"},
		})
	}
	return sources
}

// bashDynamicSourceEvidence reads the source text rather than the extracted
// imports, so a form with no literal remainder still counts. One that resolved
// to an edge is no missing edge.
func bashDynamicSourceEvidence(absRoot string, shellFiles []string, idx *fileIndex, batsReached map[string]bool) string {
	first, total, unread := "", 0, ""
	for _, path := range shellFiles {
		data, whole := readProjectFile(absRoot, path)
		if !whole && unread == "" {
			unread = path
		}
		forEachBashCommand(data, func(line int, command string) {
			if !bashCommandMissesEdge(command, path, idx, batsReached) {
				return
			}
			if first == "" {
				first = fmt.Sprintf("%s:%d", path, line+1)
			}
			total++
		})
	}
	if total == 0 {
		return unreadEvidence(unread)
	}
	return fmt.Sprintf("first at %s, %d in total", first, total)
}

// unreadEvidence names a file whose evidence went unseen, or "" for none.
func unreadEvidence(path string) string {
	if path == "" {
		return ""
	}
	return path + " is too large or unreadable to check whole"
}

// forEachBashCommand visits each command outside comments and heredocs in one
// pass. A heredoc opens only when its terminator occurs later, so a << that is
// not one skips nothing and later commands still count.
func forEachBashCommand(data []byte, visit func(line int, command string)) {
	lines := strings.Split(string(data), "\n")
	lastSeen := make(map[string]int, len(lines))
	for index, line := range lines {
		lastSeen[strings.TrimSpace(line)] = index
	}
	heredoc := ""
	for index, line := range lines {
		if heredoc != "" {
			if strings.TrimSpace(line) == heredoc {
				heredoc = ""
			}
			continue
		}
		commands, opened := bashCommands(line)
		for _, command := range commands {
			visit(index, command)
		}
		if lastSeen[opened] > index {
			heredoc = opened
		}
	}
}

func bashCommandMissesEdge(command, path string, idx *fileIndex, batsReached map[string]bool) bool {
	match := bashDynamicSource.FindStringSubmatchIndex(command)
	if match == nil {
		return false
	}
	verb := command[match[2]:match[3]]
	if verb == "load" && !strings.HasSuffix(path, ".bats") && !batsReached[path] {
		return false
	}
	target := bashImportTarget(command[match[3]:])
	if verb == "load" {
		target = bashLoadTarget(target)
	}
	return target == "" || len(resolveBashDynamic(target, idx)) == 0
}

// bashCommands splits a line at its unquoted separators, stopping at a comment,
// and names the heredoc it may open. A << inside (( or [[ is a shift or a string
// comparison, not a heredoc.
//
// ponytail: one line at a time, so the inside of a multi-line string reads as
// code; a real command there costs a partial label. A shell parser is the upgrade.
func bashCommands(line string) (commands []string, heredoc string) {
	start, nesting := 0, 0
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '\\' && quote != '\'':
			i++
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t'):
			return append(commands, line[start:i]), heredoc
		case c == ';' || c == '&' || c == '|':
			commands = append(commands, line[start:i])
			start = i + 1
		case strings.HasPrefix(line[i:], "((") || strings.HasPrefix(line[i:], "[["):
			nesting++
			i++
		case strings.HasPrefix(line[i:], "))") || strings.HasPrefix(line[i:], "]]"):
			nesting = max(nesting-1, 0)
			i++
		case strings.HasPrefix(line[i:], "<<"):
			if match := bashHeredocStart.FindStringSubmatch(line[i:]); match != nil && nesting == 0 {
				heredoc = match[1] + match[2] + match[3]
			}
			i++
		}
	}
	return append(commands, line[start:]), heredoc
}

func rubyAutoloadEvidence(absRoot string, rubyFiles []string) string {
	unread := ""
	entries, _ := os.ReadDir(absRoot)
	for _, entry := range entries {
		name := entry.Name()
		var pattern *regexp.Regexp
		switch {
		case name == "Gemfile":
			pattern = rubyGemfileAutoloader
		case strings.HasSuffix(name, ".gemspec"):
			pattern = rubyGemspecAutoloader
		default:
			continue
		}
		data, whole := readProjectFile(absRoot, name)
		if match := pattern.FindSubmatch(data); match != nil {
			return name + " names " + string(match[1])
		}
		if !whole && unread == "" {
			unread = name
		}
	}
	for _, path := range rubyFiles {
		if path == "config/application.rb" || strings.HasSuffix(path, "/config/application.rb") {
			return path + " is present"
		}
	}
	for _, path := range rubyFiles {
		data, whole := readProjectFile(absRoot, path)
		if rubyZeitwerkLoader.Match(rubyStringLiteral.ReplaceAll(data, nil)) {
			return path + " calls Zeitwerk::Loader"
		}
		if !whole && unread == "" {
			unread = path
		}
	}
	return unreadEvidence(unread)
}

func phpAutoloadEvidence(absRoot string, phpFiles []string, unresolvedUse string) string {
	unread := ""
	if composer, whole := readProjectFile(absRoot, "composer.json"); !whole {
		unread = "composer.json"
	} else if composerDeclaresAutoload(composer) {
		return "composer.json has an autoload section"
	}
	if unresolvedUse != "" {
		return "use statement resolved to no file: " + unresolvedUse
	}
	for _, path := range phpFiles {
		data, whole := readProjectFile(absRoot, path)
		if phpNamespaceStatement.Match(phpBlockComment.ReplaceAll(data, nil)) {
			return path + " has a namespace statement"
		}
		if !whole && unread == "" {
			unread = path
		}
	}
	return unreadEvidence(unread)
}

func composerDeclaresAutoload(data []byte) bool {
	var composer map[string]json.RawMessage
	if json.Unmarshal(data, &composer) != nil {
		return false
	}
	for _, key := range []string{"autoload", "autoload-dev"} {
		var section map[string]any
		if json.Unmarshal(composer[key], &section) == nil && len(section) > 0 {
			return true
		}
	}
	return false
}

// readProjectFile returns up to maxEvidenceFileBytes of a regular file, and
// whether that is all of it. A scanned name can be a FIFO, or a link to a FIFO or
// /dev/zero: those and missing files are not source, so they read as empty and
// whole. A regular file it cannot read, or read to its end, is not whole, so the
// caller can say what it did not check.
func readProjectFile(absRoot, path string) (data []byte, whole bool) {
	fullPath := filepath.Join(absRoot, filepath.FromSlash(path))
	info, err := os.Stat(fullPath)
	if err != nil {
		return nil, errors.Is(err, os.ErrNotExist)
	}
	if !info.Mode().IsRegular() {
		return nil, true
	}
	file, err := os.Open(fullPath)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	data, err = io.ReadAll(io.LimitReader(file, maxEvidenceFileBytes+1))
	if err != nil {
		return nil, false
	}
	if len(data) > maxEvidenceFileBytes {
		return data[:maxEvidenceFileBytes], false
	}
	return data, true
}
