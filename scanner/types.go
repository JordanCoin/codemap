package scanner

import (
	"path/filepath"
	"strings"
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
	Root    string       `json:"root"`
	Mode    string       `json:"mode"`
	Animate bool         `json:"animate"`
	Files   []FileInfo   `json:"files"`
	DiffRef string       `json:"diff_ref,omitempty"`
	Impact  []ImpactInfo `json:"impact,omitempty"`
	Depth   int          `json:"depth,omitempty"` // Max tree depth (0 = unlimited)
}

// FileAnalysis holds extracted info about a single file for deps mode.
type FileAnalysis struct {
	Path      string   `json:"path"`
	Language  string   `json:"language"`
	Functions []string `json:"functions"`
	Imports   []string `json:"imports"`
}

// DepsProject is the JSON output for --deps mode.
type DepsProject struct {
	Root         string              `json:"root"`
	Mode         string              `json:"mode"`
	Files        []FileAnalysis      `json:"files"`
	ExternalDeps map[string][]string `json:"external_deps"`
	DiffRef      string              `json:"diff_ref,omitempty"`
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
