package scanner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	phpNamespaceStatement = regexp.MustCompile(`(?m)^\s*(?:<\?php\s+)?namespace\s+[A-Za-z_\\]`)
	rubyGemfileAutoloader = regexp.MustCompile(`(?m)^\s*gem\s*\(?\s*['"](rails|railties|zeitwerk)['"]`)
	// A development dependency tests against Rails; it does not load this gem.
	rubyGemspecAutoloader = regexp.MustCompile(`(?m)^\s*\w+\.add_(?:runtime_)?dependency\s*\(?\s*['"](rails|railties|zeitwerk)['"]`)
	rubyZeitwerkLoader    = regexp.MustCompile(`(?m)^[^#\n]*\bZeitwerk::Loader\b`)
)

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
	return sources
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
