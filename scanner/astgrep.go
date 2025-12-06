package scanner

import (
	"embed"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed sg-rules/*.yml
var sgRules embed.FS

// ScanMatch represents a match from sg scan JSON output
type ScanMatch struct {
	File   string `json:"file"`
	RuleID string `json:"ruleId"`
	Range  struct {
		Start struct {
			Line   int `json:"line"`
			Column int `json:"column"`
		} `json:"start"`
	} `json:"range"`
	Text          string `json:"text"`
	MetaVariables struct {
		Single map[string]struct {
			Text string `json:"text"`
		} `json:"single"`
	} `json:"metaVariables"`
}

// AstGrepScanner uses ast-grep with YAML rules for code analysis
type AstGrepScanner struct {
	rulesDir string
}

// NewAstGrepScanner creates a scanner, extracting rules to temp dir
func NewAstGrepScanner() (*AstGrepScanner, error) {
	// Create temp directory for rules
	rulesDir, err := os.MkdirTemp("", "codemap-sg-rules-*")
	if err != nil {
		return nil, err
	}

	// Extract embedded rules
	entries, err := sgRules.ReadDir("sg-rules")
	if err != nil {
		os.RemoveAll(rulesDir)
		return nil, err
	}

	for _, entry := range entries {
		content, err := sgRules.ReadFile("sg-rules/" + entry.Name())
		if err != nil {
			continue
		}
		os.WriteFile(filepath.Join(rulesDir, entry.Name()), content, 0644)
	}

	return &AstGrepScanner{rulesDir: rulesDir}, nil
}

// Close cleans up temp rules directory
func (s *AstGrepScanner) Close() {
	if s.rulesDir != "" {
		os.RemoveAll(s.rulesDir)
	}
}

// Available checks if sg CLI is available
func (s *AstGrepScanner) Available() bool {
	_, err := exec.LookPath("sg")
	return err == nil
}

// ScanDirectory analyzes all files in a directory using sg scan
func (s *AstGrepScanner) ScanDirectory(root string) ([]FileAnalysis, error) {
	if !s.Available() {
		return nil, nil
	}

	// Combine all rules into one string with --- separators
	var rules []string
	entries, _ := os.ReadDir(s.rulesDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".yml") && e.Name() != "sgconfig.yml" {
			content, err := os.ReadFile(filepath.Join(s.rulesDir, e.Name()))
			if err == nil {
				rules = append(rules, string(content))
			}
		}
	}
	inlineRules := strings.Join(rules, "\n---\n")

	cmd := exec.Command("sg", "scan", "--inline-rules", inlineRules, "--json", root)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// sg scan returns non-zero if no matches, check if output is valid JSON
		if len(out) == 0 || !strings.HasPrefix(string(out), "[") {
			return nil, nil
		}
	}

	var matches []ScanMatch
	if err := json.Unmarshal(out, &matches); err != nil {
		return nil, err
	}

	// Group matches by file
	fileMap := make(map[string]*FileAnalysis)

	for _, m := range matches {
		relPath, _ := filepath.Rel(root, m.File)
		if relPath == "" {
			relPath = m.File
		}

		if fileMap[relPath] == nil {
			lang := detectLangFromRuleID(m.RuleID)
			fileMap[relPath] = &FileAnalysis{
				Path:     relPath,
				Language: lang,
			}
		}

		if strings.HasSuffix(m.RuleID, "-imports") {
			// Use metaVariable PATH if available, otherwise fall back to text extraction
			var mod string
			if pathVar, ok := m.MetaVariables.Single["PATH"]; ok && pathVar.Text != "" {
				mod = pathVar.Text
			} else {
				mod = extractImportPath(m.Text)
			}
			if mod != "" {
				fileMap[relPath].Imports = append(fileMap[relPath].Imports, mod)
			}
		} else if strings.HasSuffix(m.RuleID, "-functions") {
			// Extract function name from text
			name := extractFunctionName(m.Text, fileMap[relPath].Language)
			if name != "" {
				fileMap[relPath].Functions = append(fileMap[relPath].Functions, name)
			}
		}
	}

	// Convert map to slice and dedupe
	var results []FileAnalysis
	for _, a := range fileMap {
		a.Functions = dedupe(a.Functions)
		a.Imports = dedupe(a.Imports)
		results = append(results, *a)
	}

	return results, nil
}

func detectLangFromRuleID(ruleID string) string {
	parts := strings.Split(ruleID, "-")
	if len(parts) > 0 {
		switch parts[0] {
		case "go":
			return "go"
		case "ts":
			return "typescript"
		case "js":
			return "javascript"
		case "py":
			return "python"
		case "rust":
			return "rust"
		case "java":
			return "java"
		case "ruby":
			return "ruby"
		case "swift":
			return "swift"
		case "kotlin":
			return "kotlin"
		case "c":
			return "c"
		case "cpp":
			return "cpp"
		case "bash":
			return "bash"
		}
	}
	return ""
}

func extractImportPath(text string) string {
	// Handle various import formats
	text = strings.TrimSpace(text)

	// C/C++: #include <header> or #include "header"
	if strings.HasPrefix(text, "#include") {
		// Try angle brackets first
		if start := strings.Index(text, "<"); start >= 0 {
			if end := strings.Index(text[start:], ">"); end > 0 {
				return text[start+1 : start+end]
			}
		}
	}

	// Bash: source ./file or . ./file
	if strings.HasPrefix(text, "source ") || strings.HasPrefix(text, ". ") {
		parts := strings.Fields(text)
		if len(parts) >= 2 {
			return parts[1]
		}
	}

	// Find quoted strings (Go, TS/JS, Python, C/C++ with quotes)
	for _, q := range []string{`"`, `'`, "`"} {
		if idx := strings.Index(text, q); idx >= 0 {
			end := strings.Index(text[idx+1:], q)
			if end > 0 {
				return text[idx+1 : idx+1+end]
			}
		}
	}

	// Python: import foo
	if strings.HasPrefix(text, "import ") {
		parts := strings.Fields(text)
		if len(parts) >= 2 {
			return parts[1]
		}
	}

	// Python: from foo import bar
	if strings.HasPrefix(text, "from ") {
		parts := strings.Fields(text)
		if len(parts) >= 2 {
			return parts[1]
		}
	}

	// Rust: use foo::bar;
	if strings.HasPrefix(text, "use ") {
		text = strings.TrimPrefix(text, "use ")
		text = strings.TrimSuffix(text, ";")
		// Get root module
		if idx := strings.Index(text, "::"); idx > 0 {
			return text[:idx]
		}
		return strings.TrimSpace(text)
	}

	// Java: import foo.bar.Baz;
	if strings.HasPrefix(text, "import ") {
		text = strings.TrimPrefix(text, "import ")
		text = strings.TrimSuffix(text, ";")
		text = strings.TrimSpace(text)
		// Get package (everything except last part)
		if idx := strings.LastIndex(text, "."); idx > 0 {
			return text[:idx]
		}
		return text
	}

	return ""
}

func extractFunctionName(text string, lang string) string {
	text = strings.TrimSpace(text)

	switch lang {
	case "go":
		// func Name(...) or func (r Receiver) Name(...)
		if strings.HasPrefix(text, "func ") {
			text = strings.TrimPrefix(text, "func ")
			// Skip receiver if present
			if strings.HasPrefix(text, "(") {
				if idx := strings.Index(text, ")"); idx > 0 {
					text = strings.TrimSpace(text[idx+1:])
				}
			}
			// Get function name (up to paren)
			if idx := strings.Index(text, "("); idx > 0 {
				return text[:idx]
			}
		}

	case "typescript", "javascript":
		// function name(...) or async function name(...)
		if strings.Contains(text, "function ") {
			idx := strings.Index(text, "function ") + 9
			text = text[idx:]
			if paren := strings.Index(text, "("); paren > 0 {
				return strings.TrimSpace(text[:paren])
			}
		}
		// Method definitions: [modifiers] name(...) or get/set name(...)
		if paren := strings.Index(text, "("); paren > 0 {
			name := strings.TrimSpace(text[:paren])
			// Strip TypeScript/JS modifiers
			for _, mod := range []string{"public ", "private ", "protected ", "static ", "readonly ", "async ", "get ", "set ", "override "} {
				name = strings.TrimPrefix(name, mod)
			}
			// Skip control flow keywords
			if name == "if" || name == "for" || name == "while" || name == "switch" || name == "catch" || name == "" {
				return ""
			}
			if isValidIdentifier(name) {
				return name
			}
		}

	case "python":
		// def name(...):
		if strings.HasPrefix(text, "def ") {
			text = strings.TrimPrefix(text, "def ")
			if paren := strings.Index(text, "("); paren > 0 {
				return text[:paren]
			}
		}

	case "rust":
		// fn name(...) or pub fn name(...)
		if idx := strings.Index(text, "fn "); idx >= 0 {
			text = text[idx+3:]
			if paren := strings.Index(text, "("); paren > 0 {
				name := text[:paren]
				// Handle generics
				if bracket := strings.Index(name, "<"); bracket > 0 {
					name = name[:bracket]
				}
				return name
			}
		}

	case "java":
		// public void name(...) - method declaration
		// Find last word before (
		if paren := strings.Index(text, "("); paren > 0 {
			before := strings.TrimSpace(text[:paren])
			parts := strings.Fields(before)
			if len(parts) > 0 {
				return parts[len(parts)-1]
			}
		}

	case "ruby":
		// def name or def name(...)
		if strings.HasPrefix(text, "def ") {
			text = strings.TrimPrefix(text, "def ")
			if paren := strings.Index(text, "("); paren > 0 {
				return text[:paren]
			}
			// No parens - take first word
			if space := strings.Index(text, " "); space > 0 {
				return text[:space]
			}
			if newline := strings.Index(text, "\n"); newline > 0 {
				return text[:newline]
			}
			return text
		}

	case "swift", "kotlin":
		// func name(...) or fun name(...)
		for _, prefix := range []string{"func ", "fun "} {
			if idx := strings.Index(text, prefix); idx >= 0 {
				text = text[idx+len(prefix):]
				if paren := strings.Index(text, "("); paren > 0 {
					name := text[:paren]
					if bracket := strings.Index(name, "<"); bracket > 0 {
						name = name[:bracket]
					}
					return strings.TrimSpace(name)
				}
			}
		}

	case "c", "cpp":
		// type name(...) - find last identifier before (
		if paren := strings.Index(text, "("); paren > 0 {
			before := strings.TrimSpace(text[:paren])
			// Handle pointers: int *foo -> foo
			before = strings.TrimLeft(before, "*")
			parts := strings.Fields(before)
			if len(parts) > 0 {
				name := parts[len(parts)-1]
				name = strings.TrimLeft(name, "*")
				if isValidIdentifier(name) {
					return name
				}
			}
		}

	case "bash":
		// function name() or name()
		text = strings.TrimPrefix(text, "function ")
		if paren := strings.Index(text, "("); paren > 0 {
			name := strings.TrimSpace(text[:paren])
			if isValidIdentifier(name) {
				return name
			}
		}
	}

	return ""
}

func isValidIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		if i == 0 {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_') {
				return false
			}
		} else {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
				return false
			}
		}
	}
	return true
}

// Legacy exports for compatibility
type AstGrepAnalyzer = AstGrepScanner

func NewAstGrepAnalyzer() *AstGrepAnalyzer {
	s, _ := NewAstGrepScanner()
	return s
}

func (s *AstGrepScanner) AnalyzeFile(filePath string) (*FileAnalysis, error) {
	results, err := s.ScanDirectory(filepath.Dir(filePath))
	if err != nil {
		return nil, err
	}
	base := filepath.Base(filePath)
	for _, r := range results {
		if filepath.Base(r.Path) == base {
			return &r, nil
		}
	}
	return nil, nil
}
