package scanner

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

//go:embed queries/*.scm
var queryFiles embed.FS

// LanguageConfig holds dynamically loaded parser and query
type LanguageConfig struct {
	Language *tree_sitter.Language
	Query    *tree_sitter.Query
}

// GrammarLoader handles dynamic loading of tree-sitter grammars
type GrammarLoader struct {
	configs    map[string]*LanguageConfig
	grammarDir string
}

// LangInfo holds display names for a language
type LangInfo struct {
	Short string // Compact label: "JS", "Py"
	Full  string // Full name: "JavaScript", "Python"
}

// LangDisplay maps internal language names to display names
var LangDisplay = map[string]LangInfo{
	"go":         {"Go", "Go"},
	"python":     {"Py", "Python"},
	"javascript": {"JS", "JavaScript"},
	"typescript": {"TS", "TypeScript"},
	"rust":       {"Rs", "Rust"},
	"ruby":       {"Rb", "Ruby"},
	"c":          {"C", "C"},
	"cpp":        {"C++", "C++"},
	"java":       {"Java", "Java"},
	"swift":      {"Swift", "Swift"},
	"bash":       {"Sh", "Bash"},
	"kotlin":     {"Kt", "Kotlin"},
	"c_sharp":    {"C#", "C#"},
	"php":        {"PHP", "PHP"},
	"dart":       {"Dart", "Dart"},
	"r":          {"R", "R"},
}

// Extension to language mapping
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
	".cs":    "c_sharp",
	".php":   "php",
	".dart":  "dart",
	".r":     "r",
	".R":     "r",
}

// NewGrammarLoader creates a loader that searches for grammars
func NewGrammarLoader() *GrammarLoader {
	loader := &GrammarLoader{
		configs: make(map[string]*LanguageConfig),
	}

	// Find grammar directory - check env var first (for Homebrew install)
	possibleDirs := []string{}
	if envDir := os.Getenv("CODEMAP_GRAMMAR_DIR"); envDir != "" {
		possibleDirs = append(possibleDirs, envDir)
	}
	possibleDirs = append(possibleDirs,
		filepath.Join(getExecutableDir(), "grammars"),
		filepath.Join(getExecutableDir(), "..", "lib", "grammars"),
		"/opt/homebrew/opt/codemap/libexec/grammars", // Homebrew Apple Silicon
		"/usr/local/opt/codemap/libexec/grammars",    // Homebrew Intel Mac
		"/usr/local/lib/codemap/grammars",
		filepath.Join(os.Getenv("HOME"), ".codemap", "grammars"),
		"./grammars",         // For development
		"./scanner/grammars", // For development from root
	)

	for _, dir := range possibleDirs {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			loader.grammarDir = dir
			break
		}
	}

	return loader
}

// HasGrammars returns true if grammar directory was found
func (l *GrammarLoader) HasGrammars() bool {
	return l.grammarDir != ""
}

// GrammarDir returns the grammar directory path (for diagnostics)
func (l *GrammarLoader) GrammarDir() string {
	return l.grammarDir
}

// LoadLanguage dynamically loads a grammar from .so/.dylib
func (l *GrammarLoader) LoadLanguage(lang string) error {
	if _, exists := l.configs[lang]; exists {
		return nil // Already loaded
	}

	if l.grammarDir == "" {
		return fmt.Errorf("no grammar directory found")
	}

	// OS-specific library extension
	var libExt string
	switch runtime.GOOS {
	case "darwin":
		libExt = ".dylib"
	case "windows":
		libExt = ".dll"
	default:
		libExt = ".so"
	}

	// Load shared library
	libPath := filepath.Join(l.grammarDir, fmt.Sprintf("libtree-sitter-%s%s", lang, libExt))
	lib, err := loadLibrary(libPath)
	if err != nil {
		return fmt.Errorf("load %s: %w", libPath, err)
	}

	// Get language function
	langFunc, err := getLanguageFunc(lib, lang)
	if err != nil {
		return fmt.Errorf("get func for %s: %w", lang, err)
	}
	language := tree_sitter.NewLanguage(langFunc())

	// Load query
	queryBytes, err := queryFiles.ReadFile(fmt.Sprintf("queries/%s.scm", lang))
	if err != nil {
		return fmt.Errorf("no query for %s", lang)
	}

	query, qerr := tree_sitter.NewQuery(language, string(queryBytes))
	if qerr != nil {
		return fmt.Errorf("bad query for %s: %v", lang, qerr)
	}

	l.configs[lang] = &LanguageConfig{Language: language, Query: query}
	return nil
}

// DetectLanguage returns the language name for a file path
func DetectLanguage(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	return extToLang[ext]
}

// AnalyzeFile extracts functions and imports
func (l *GrammarLoader) AnalyzeFile(filePath string) (*FileAnalysis, error) {
	lang := DetectLanguage(filePath)
	if lang == "" {
		return nil, nil
	}

	if err := l.LoadLanguage(lang); err != nil {
		return nil, nil // Skip if grammar unavailable
	}

	config := l.configs[lang]
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	parser := tree_sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(config.Language)

	tree := parser.Parse(content, nil)
	defer tree.Close()

	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()

	analysis := &FileAnalysis{Path: filePath, Language: lang}

	// Use Matches() API - iterate over query matches
	matches := cursor.Matches(config.Query, tree.RootNode(), content)
	for match := matches.Next(); match != nil; match = matches.Next() {
		for _, capture := range match.Captures {
			name := config.Query.CaptureNames()[capture.Index]
			text := strings.Trim(capture.Node.Utf8Text(content), `"'`)

			switch name {
			case "function", "method":
				analysis.Functions = append(analysis.Functions, text)
			case "import", "module":
				analysis.Imports = append(analysis.Imports, text)
			}
		}
	}

	analysis.Functions = dedupe(analysis.Functions)
	analysis.Imports = dedupe(analysis.Imports)
	return analysis, nil
}

func getExecutableDir() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(exe)
	}
	return "."
}

func dedupe(s []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
