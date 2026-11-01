package scanner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"codemap/analysis"
)

var (
	phpNamespaceStatement = regexp.MustCompile(`(?m)^\s*(?:<\?php\s+)?namespace\s+[A-Za-z_\\]`)
	rubyGemfileAutoloader = regexp.MustCompile(`(?m)^\s*gem\s*\(?\s*['"](rails|railties|zeitwerk)['"]`)
	// A development dependency tests against Rails; it does not load this gem.
	rubyGemspecAutoloader = regexp.MustCompile(`(?m)^\s*\w+\.add_(?:runtime_)?dependency\s*\(?\s*['"](rails|railties|zeitwerk)['"]`)
	rubyZeitwerkLoader    = regexp.MustCompile(`(?m)^[^#\n]*\bZeitwerk::Loader\b`)
	// ponytail: line shape, not a shell parse. A matching line in a heredoc
	// counts; the cost is a partial label, never a false complete.
	bashDynamicSource = regexp.MustCompile("^\\s*(?:.*[;&|]\\s*|.*\\b(?:then|do|else)\\s+)?(?:source|\\.|load)\\s+(?:\"[^\"\\s]*[$`]|[^\\s'\"]*[$`]|<\\()")
)

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
func missingEdgeSources(absRoot string, files []FileInfo, phpUnresolvedUse string) []ScanSourceOutcome {
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
	// .bats has no language mapping, but it is where bats load lives.
	shell := byLanguage["bash"]
	for _, path := range byLanguage[""] {
		if strings.HasSuffix(path, ".bats") {
			shell = append(shell, path)
		}
	}
	sort.Strings(shell)
	if evidence := bashDynamicSourceEvidence(absRoot, shell); evidence != "" {
		sources = append(sources, ScanSourceOutcome{
			Name:      "bash-dynamic-source",
			Status:    ScanSourceMixed,
			Detail:    "Bash: source, . or load with a dynamic argument is not resolved (" + evidence + "): search the file name before treating 0 importers as unused",
			Languages: []string{"bash"},
		})
	}
	return sources
}

// bashDynamicSourceEvidence reads the source text rather than the extracted
// imports: extraction drops these arguments, and load is not extracted at all.
func bashDynamicSourceEvidence(absRoot string, shellFiles []string) string {
	first, total := "", 0
	for _, path := range shellFiles {
		for index, line := range strings.Split(string(readProjectFile(absRoot, path)), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") || !bashDynamicSource.MatchString(line) {
				continue
			}
			if first == "" {
				first = fmt.Sprintf("%s:%d", path, index+1)
			}
			total++
		}
	}
	if total == 0 {
		return ""
	}
	return fmt.Sprintf("first at %s, %d in total", first, total)
}

func rubyAutoloadEvidence(absRoot string, rubyFiles []string) string {
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
		if match := pattern.FindSubmatch(readProjectFile(absRoot, name)); match != nil {
			return name + " names " + string(match[1])
		}
	}
	for _, path := range rubyFiles {
		if path == "config/application.rb" || strings.HasSuffix(path, "/config/application.rb") {
			return path + " is present"
		}
	}
	for _, path := range rubyFiles {
		if rubyZeitwerkLoader.Match(readProjectFile(absRoot, path)) {
			return path + " calls Zeitwerk::Loader"
		}
	}
	return ""
}

func phpAutoloadEvidence(absRoot string, phpFiles []string, unresolvedUse string) string {
	if composerDeclaresAutoload(filepath.Join(absRoot, "composer.json")) {
		return "composer.json has an autoload section"
	}
	if unresolvedUse != "" {
		return "use statement resolved to no file: " + unresolvedUse
	}
	for _, path := range phpFiles {
		if phpNamespaceStatement.Match(readProjectFile(absRoot, path)) {
			return path + " has a namespace statement"
		}
	}
	return ""
}

func composerDeclaresAutoload(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
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

// readProjectFile returns nil for an unreadable file: it offers no evidence, and
// the graph build must not fail over it.
func readProjectFile(absRoot, path string) []byte {
	data, err := os.ReadFile(filepath.Join(absRoot, filepath.FromSlash(path)))
	if err != nil {
		return nil
	}
	return data
}
