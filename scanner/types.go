package scanner

import (
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"codemap/analysis"
)

// FileInfo represents a single file in the codebase.
type FileInfo struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Ext     string `json:"ext"`
	IsNew   bool   `json:"is_new,omitempty"`
	Added   int    `json:"added,omitempty"`
	Removed int    `json:"removed,omitempty"`
}

// Project represents the root of the codebase for tree/skyline mode.
type Project struct {
	Root      string       `json:"root"`
	Name      string       `json:"name,omitempty"`       // Display name (overrides filepath.Base(Root))
	RemoteURL string       `json:"remote_url,omitempty"` // Source URL if cloned from remote
	Mode      string       `json:"mode"`
	Animate   bool         `json:"animate"`
	Files     []FileInfo   `json:"files"`
	DiffRef   string       `json:"diff_ref,omitempty"`
	Impact    []ImpactInfo `json:"impact,omitempty"`
	Depth     int          `json:"depth,omitempty"`   // Max tree depth (0 = unlimited)
	Only      []string     `json:"only,omitempty"`    // Extension filter (e.g., ["swift", "go"])
	Exclude   []string     `json:"exclude,omitempty"` // Exclusion patterns (e.g., [".xcassets", "Fonts"])
}

// FileAnalysis holds extracted info about a single file for deps mode.
type FileAnalysis struct {
	Path       string            `json:"path"`
	Language   string            `json:"language"`
	Functions  []string          `json:"functions"`
	Imports    []string          `json:"imports"`
	References []ImportReference `json:"-"`
}

// ImportReference preserves language-specific import semantics for graph resolution.
type ImportReference struct {
	Path string
	Kind string
	Line int
}

// DepsProject is the JSON output for --deps mode.
type DepsProject struct {
	SchemaVersion string              `json:"schema_version"`
	Coverage      analysis.Coverage   `json:"coverage"`
	Root          string              `json:"root"`
	Mode          string              `json:"mode"`
	Files         []FileAnalysis      `json:"files"`
	ExternalDeps  map[string][]string `json:"external_deps"`
	DiffRef       string              `json:"diff_ref,omitempty"`
}

// newDepsProject builds a DepsProject with default coverage derived from the
// analyses and inventory (Rust repos are partial). Production callers pass
// explicit graph-derived coverage via NewDepsProjectWithCoverage; this default
// constructor is kept for the determinism and Rust-coverage tests.
func newDepsProject(root string, files []FileAnalysis, externalDeps map[string][]string, diffRef string, inventory ...[]FileInfo) DepsProject {
	coverage := analysis.Coverage{
		Status:  analysis.CoverageComplete,
		Sources: []analysis.Source{{Name: "ast-grep", Status: analysis.SourceAuthoritative}},
	}
	for _, file := range files {
		if file.Language == "rust" || DetectLanguage(file.Path) == "rust" {
			coverage.Status = analysis.CoveragePartial
			coverage.Sources[0].Detail = rustCoverageNote
			break
		}
	}
	if coverage.Status == analysis.CoverageComplete && len(inventory) > 0 {
		for _, file := range inventory[0] {
			if DetectLanguage(file.Path) == "rust" {
				coverage.Status = analysis.CoveragePartial
				coverage.Sources[0].Detail = rustCoverageNote
				break
			}
		}
	}
	return NewDepsProjectWithCoverage(root, files, externalDeps, diffRef, coverage)
}

// NewDepsProjectWithCoverage builds a normalized DepsProject with caller-provided
// provenance coverage, so degraded scans stay honest in the output.
func NewDepsProjectWithCoverage(root string, files []FileAnalysis, externalDeps map[string][]string, diffRef string, coverage analysis.Coverage) DepsProject {
	files = slices.Clone(files)
	if files == nil {
		files = []FileAnalysis{}
	}
	for index := range files {
		files[index].Functions = slices.Clone(files[index].Functions)
		if files[index].Functions == nil {
			files[index].Functions = []string{}
		}
		files[index].Imports = slices.Clone(files[index].Imports)
		if files[index].Imports == nil {
			files[index].Imports = []string{}
		}
		slices.Sort(files[index].Functions)
		slices.Sort(files[index].Imports)
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].Path != files[j].Path {
			return files[i].Path < files[j].Path
		}
		if files[i].Language != files[j].Language {
			return files[i].Language < files[j].Language
		}
		if order := slices.Compare(files[i].Functions, files[j].Functions); order != 0 {
			return order < 0
		}
		return slices.Compare(files[i].Imports, files[j].Imports) < 0
	})
	deps := make(map[string][]string, len(externalDeps))
	for language, values := range externalDeps {
		deps[language] = slices.Clone(values)
		if deps[language] == nil {
			deps[language] = []string{}
		}
		slices.Sort(deps[language])
	}
	return DepsProject{
		SchemaVersion: analysis.SchemaVersion,
		Coverage:      analysis.NormalizeCoverage(coverage),
		Root:          root, Mode: "deps", Files: files, ExternalDeps: deps, DiffRef: diffRef,
	}
}

// ImportersReport is the JSON output for --importers mode.
type ImportersReport struct {
	Root           string   `json:"root"`
	Mode           string   `json:"mode"`
	File           string   `json:"file"`
	Importers      []string `json:"importers"`
	Imports        []string `json:"imports,omitempty"`
	HubImports     []string `json:"hub_imports,omitempty"`
	ImporterCount  int      `json:"importer_count"`
	IsHub          bool     `json:"is_hub"`
	CoverageStatus string   `json:"coverage_status,omitempty"`
	CoverageNotes  []string `json:"coverage_notes,omitempty"`
}

// extToLang maps file extensions to language names
var extToLang = map[string]string{
	".go":    "go",
	".py":    "python",
	".js":    "javascript",
	".jsx":   "javascript",
	".mjs":   "javascript",
	".ts":    "typescript",
	".tsx":   "typescript",
	".rs":    "rust",
	".rb":    "ruby",
	".c":     "c",
	".h":     "c",
	".cpp":   "cpp",
	".hpp":   "cpp",
	".cc":    "cpp",
	".java":  "java",
	".swift": "swift",
	".sh":    "bash",
	".bash":  "bash",
	".kt":    "kotlin",
	".kts":   "kotlin",
	".cs":    "csharp",
	".php":   "php",
	".lua":   "lua",
	".scala": "scala",
	".sc":    "scala",
	".ex":    "elixir",
	".exs":   "elixir",
	".sol":   "solidity",
}

// DetectLanguage returns the language name for a file path
func DetectLanguage(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	return extToLang[ext]
}

// IsSourceExt returns true if the extension (with leading dot) is a recognized source file.
func IsSourceExt(ext string) bool {
	_, ok := extToLang[strings.ToLower(ext)]
	return ok
}

// SourceExtensions returns all recognized source file extensions (with leading dot),
// sorted for deterministic output.
func SourceExtensions() []string {
	exts := make([]string, 0, len(extToLang))
	for ext := range extToLang {
		exts = append(exts, ext)
	}
	sort.Strings(exts)
	return exts
}

// PromptExtensions returns extension strings without the leading dot,
// suitable for regex-based file mention extraction from prompts.
// Sorted for deterministic output.
func PromptExtensions() []string {
	seen := make(map[string]bool)
	var exts []string
	for ext := range extToLang {
		bare := strings.TrimPrefix(ext, ".")
		if !seen[bare] {
			seen[bare] = true
			exts = append(exts, bare)
		}
	}
	sort.Strings(exts)
	return exts
}

// ResolverExtensions returns extensions used for import path resolution,
// including index-file patterns for JS/TS/Python ecosystems.
// Sorted by length descending so longer extensions match first (.tsx before .ts),
// with empty string last as the final fallback.
func ResolverExtensions() []string {
	var exts []string
	for ext := range extToLang {
		exts = append(exts, ext)
	}
	// Sort by length descending, then alphabetically for stability
	sort.Slice(exts, func(i, j int) bool {
		if len(exts[i]) != len(exts[j]) {
			return len(exts[i]) > len(exts[j])
		}
		return exts[i] < exts[j]
	})
	// Index-file patterns for module resolution
	exts = append(exts, "/index.js", "/index.ts", "/index.tsx", "/__init__.py", "/mod.rs")
	// Empty string last — bare path match as final fallback
	exts = append(exts, "")
	return exts
}

var resolverLanguageFamilies = map[string]string{
	"javascript": "js",
	"typescript": "js",
	"c":          "c-cpp",
	"cpp":        "c-cpp",
	"java":       "jvm",
	"kotlin":     "jvm",
	"scala":      "jvm",
	"python":     "python",
	"csharp":     "csharp",
	"go":         "go",
	"rust":       "rust",
	"swift":      "swift",
	"ruby":       "ruby",
	"php":        "php",
	"lua":        "lua",
	"elixir":     "elixir",
	"solidity":   "solidity",
	"bash":       "bash",
}

// languagesCompatible reports whether an import may resolve across the two
// source languages. Unknown languages are deliberately incompatible.
func languagesCompatible(importer, candidate string) bool {
	importerFamily, importerKnown := resolverLanguageFamilies[importer]
	candidateFamily, candidateKnown := resolverLanguageFamilies[candidate]
	return importerKnown && candidateKnown && importerFamily == candidateFamily
}

// LangDisplay maps internal language names to display names
var LangDisplay = map[string]string{
	"go":         "Go",
	"python":     "Python",
	"javascript": "JavaScript",
	"typescript": "TypeScript",
	"rust":       "Rust",
	"ruby":       "Ruby",
	"c":          "C",
	"cpp":        "C++",
	"java":       "Java",
	"swift":      "Swift",
	"bash":       "Bash",
	"kotlin":     "Kotlin",
	"csharp":     "C#",
	"php":        "PHP",
	"lua":        "Lua",
	"scala":      "Scala",
	"elixir":     "Elixir",
	"solidity":   "Solidity",
}

// dedupe removes duplicate strings from a slice
func dedupe(items []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	return result
}
