package main

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"github.com/ebitengine/purego"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

//go:embed queries/*.scm
var queryFiles embed.FS

// FileAnalysis holds extracted info about a single file
type FileAnalysis struct {
	Path      string   `json:"path"`
	Language  string   `json:"language"`
	Functions []string `json:"functions"`
	Imports   []string `json:"imports"`
}

// DepsProject is the JSON output for --deps mode
type DepsProject struct {
	Root         string              `json:"root"`
	Mode         string              `json:"mode"`
	Files        []FileAnalysis      `json:"files"`
	ExternalDeps map[string][]string `json:"external_deps"`
}

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
		"/usr/local/lib/codemap/grammars",
		filepath.Join(os.Getenv("HOME"), ".codemap", "grammars"),
		"./grammars", // For development
	)

	for _, dir := range possibleDirs {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			loader.grammarDir = dir
			break
		}
	}

	return loader
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
	lib, err := purego.Dlopen(libPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return fmt.Errorf("load %s: %w", libPath, err)
	}

	// Get language function
	var langFunc func() uintptr
	purego.RegisterLibFunc(&langFunc, lib, fmt.Sprintf("tree_sitter_%s", lang))
	language := tree_sitter.NewLanguage(unsafe.Pointer(langFunc()))

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

// ReadExternalDeps reads manifest files (go.mod, requirements.txt, package.json)
func ReadExternalDeps(root string) map[string][]string {
	deps := make(map[string][]string)

	// Directories to skip
	skipDirs := map[string]bool{
		"node_modules": true,
		"vendor":       true,
		".git":         true,
		"venv":         true,
		".venv":        true,
		"__pycache__":  true,
	}

	// Walk tree to find all manifest files
	filepath.Walk(root, func(path string, info os.FileInfo, _ error) error {
		if info == nil {
			return nil
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		switch info.Name() {
		case "go.mod":
			if c, err := os.ReadFile(path); err == nil {
				deps["go"] = append(deps["go"], parseGoMod(string(c))...)
			}
		case "requirements.txt":
			if c, err := os.ReadFile(path); err == nil {
				deps["python"] = append(deps["python"], parseRequirements(string(c))...)
			}
		case "package.json":
			if c, err := os.ReadFile(path); err == nil {
				deps["javascript"] = append(deps["javascript"], parsePackageJson(string(c))...)
			}
		case "Podfile":
			if c, err := os.ReadFile(path); err == nil {
				deps["swift"] = append(deps["swift"], parsePodfile(string(c))...)
			}
		case "Package.swift":
			if c, err := os.ReadFile(path); err == nil {
				deps["swift"] = append(deps["swift"], parsePackageSwift(string(c))...)
			}
		}
		return nil
	})

	for k, v := range deps {
		deps[k] = dedupe(v)
	}
	return deps
}

func parseGoMod(c string) (deps []string) {
	inReq := false
	for _, line := range strings.Split(c, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "require (") {
			inReq = true
		} else if inReq && line == ")" {
			inReq = false
		} else if inReq && line != "" && !strings.HasPrefix(line, "//") {
			if parts := strings.Fields(line); len(parts) >= 1 {
				deps = append(deps, parts[0])
			}
		}
	}
	return
}

func parseRequirements(c string) (deps []string) {
	for _, line := range strings.Split(c, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for _, sep := range []string{"==", ">=", "<=", "~=", "<", ">", "[", ";", "#"} {
			if i := strings.Index(line, sep); i != -1 {
				line = line[:i]
			}
		}
		if line != "" {
			deps = append(deps, line)
		}
	}
	return
}

func parsePackageJson(c string) (deps []string) {
	inDeps := false
	for _, line := range strings.Split(c, "\n") {
		if strings.Contains(line, `"dependencies"`) || strings.Contains(line, `"devDependencies"`) {
			inDeps = true
		} else if inDeps && strings.Contains(line, "}") {
			inDeps = false
		} else if inDeps && strings.Contains(line, ":") {
			parts := strings.SplitN(line, ":", 2)
			name := strings.Trim(strings.TrimSpace(parts[0]), `"`)
			if name != "" {
				deps = append(deps, name)
			}
		}
	}
	return
}

func parsePodfile(c string) (deps []string) {
	// Parse Podfile: pod 'Name' or pod 'Name', '~> 1.0'
	for _, line := range strings.Split(c, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "pod ") {
			// Extract pod name from: pod 'Name' or pod 'Name', ...
			line = strings.TrimPrefix(line, "pod ")
			line = strings.Trim(line, "'\"")
			if i := strings.Index(line, "'"); i != -1 {
				line = line[:i]
			}
			if i := strings.Index(line, "\""); i != -1 {
				line = line[:i]
			}
			if i := strings.Index(line, ","); i != -1 {
				line = line[:i]
			}
			line = strings.Trim(line, "'\" ")
			if line != "" {
				deps = append(deps, line)
			}
		}
	}
	return
}

func parsePackageSwift(c string) (deps []string) {
	// Parse Package.swift: .package(url: "...", ...) or .package(name: "Name", ...)
	// Look for package names in .product(name: "Name", package: "Package")
	for _, line := range strings.Split(c, "\n") {
		// Match .package(url: "https://github.com/user/repo", ...)
		if strings.Contains(line, ".package(") && strings.Contains(line, "url:") {
			// Extract repo name from URL
			if i := strings.Index(line, "url:"); i != -1 {
				rest := line[i+4:]
				rest = strings.Trim(rest, " \"'")
				if j := strings.Index(rest, "\""); j != -1 {
					url := rest[:j]
					// Get repo name from URL
					parts := strings.Split(url, "/")
					if len(parts) > 0 {
						name := parts[len(parts)-1]
						name = strings.TrimSuffix(name, ".git")
						if name != "" {
							deps = append(deps, name)
						}
					}
				}
			}
		}
	}
	return
}
