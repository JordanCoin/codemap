package scanner

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"codemap/analysis"
)

//go:embed sg-rules/*.yml
var sgRules embed.FS

var astGrepScanTimeout = 30 * time.Second
var ErrAstGrepNotFound = errors.New("ast-grep not found (checked bundled tools and PATH)")

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
	rulesDir    string
	inlineRules string
	binary      string // "sg" or "ast-grep", whichever is available
}

// NewAstGrepScanner creates a scanner using the embedded rules.
func NewAstGrepScanner() (*AstGrepScanner, error) {
	// Find ast-grep binary (installed as "sg" via brew, "ast-grep" via cargo/pipx)
	binary := findAstGrepBinary()

	entries, err := sgRules.ReadDir("sg-rules")
	if err != nil {
		return nil, err
	}

	var rules []string
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".yml") || entry.Name() == "sgconfig.yml" {
			continue
		}
		content, err := sgRules.ReadFile("sg-rules/" + entry.Name())
		if err != nil {
			continue
		}
		rules = append(rules, string(content))
	}

	return &AstGrepScanner{inlineRules: strings.Join(rules, "\n---\n"), binary: binary}, nil
}

// extractJSONArray finds and extracts a JSON array from output that may have garbage before it
// This handles ast-grep 0.40.2 which had a debug println! bug
func extractJSONArray(data []byte) []byte {
	// Find the first '[' which starts the JSON array
	idx := strings.Index(string(data), "[")
	if idx == -1 {
		return nil
	}
	return data[idx:]
}

func isAstGrepBinary(path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil && len(out) == 0 {
		return false
	}

	return strings.Contains(strings.ToLower(string(out)), "ast-grep")
}

func bundledAstGrepNames() []string {
	if runtime.GOOS == "windows" {
		return []string{"ast-grep.exe", "sg.exe"}
	}
	return []string{"ast-grep", "sg"}
}

func bundledAstGrepCandidates(executablePath string) []string {
	if executablePath == "" {
		return nil
	}

	seen := map[string]bool{}
	dirs := make([]string, 0, 2)
	addDir := func(dir string) {
		if dir == "" {
			return
		}
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			dir = resolved
		}
		if seen[dir] {
			return
		}
		seen[dir] = true
		dirs = append(dirs, dir)
	}

	addDir(filepath.Dir(executablePath))
	if resolved, err := filepath.EvalSymlinks(executablePath); err == nil {
		addDir(filepath.Dir(resolved))
	}

	var candidates []string
	for _, dir := range dirs {
		for _, name := range bundledAstGrepNames() {
			candidates = append(candidates, filepath.Join(dir, name))
		}
	}
	return candidates
}

func findBundledAstGrepBinary() string {
	executablePath, err := os.Executable()
	if err != nil {
		return ""
	}

	for _, candidate := range bundledAstGrepCandidates(executablePath) {
		if isAstGrepBinary(candidate) {
			return candidate
		}
	}
	return ""
}

// findAstGrepBinary checks for "ast-grep" first, then "sg".
// Linux commonly ships a non-ast-grep "sg" binary, so candidates must
// identify themselves as ast-grep before we accept them.
func findAstGrepBinary() string {
	if bundled := findBundledAstGrepBinary(); bundled != "" {
		return bundled
	}

	for _, candidate := range []string{"ast-grep", "sg"} {
		path, err := exec.LookPath(candidate)
		if err != nil {
			continue
		}
		if isAstGrepBinary(path) {
			return path
		}
	}
	return ""
}

// Close cleans up temp rules directory
func (s *AstGrepScanner) Close() {
	if s.rulesDir != "" {
		os.RemoveAll(s.rulesDir)
	}
}

// Available checks if ast-grep CLI is available (as "sg" or "ast-grep")
func (s *AstGrepScanner) Available() bool {
	return s.binary != ""
}

// findNestedGitRepos returns subdirectory names that contain their own .git
// These are separate repositories (not submodules) that should be excluded
// from scanning to avoid hanging on large nested repos.
func findNestedGitRepos(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}

	var repos []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		gitPath := filepath.Join(root, e.Name(), ".git")
		if info, err := os.Stat(gitPath); err == nil && info.IsDir() {
			repos = append(repos, e.Name())
		}
	}
	return repos
}

// ScanDirectory analyzes all files in a directory while honoring caller
// cancellation and recording ast-grep provenance. A timed-out or failed scan
// fails closed to an empty outcome whose Sources record the degraded status,
// so callers get an honest, usable answer instead of a hard error; an
// unavailable scanner (ast-grep not installed) still returns an error.
func (s *AstGrepScanner) ScanDirectory(parent context.Context, root string) (ScanOutcome, error) {
	analyses, err := s.scanDirectory(parent, root)
	if err != nil {
		var incomplete *IncompleteScanError
		if errors.As(err, &incomplete) && incomplete.Outcome.Status != ScanSourceUnavailable {
			return ScanOutcome{
				Analyses: nil,
				Sources:  []analysis.Source{incomplete.Outcome},
			}, nil
		}
		return ScanOutcome{}, err
	}
	// ast-grep extraction is authoritative regardless of language: it scanned
	// the files and reported every reference it found. The Rust caveat
	// (macro-generated, string-routed, and #[path] edges) is a *resolution*
	// gap owned by rust-cargo, which the file graph records separately; labeling
	// ast-grep mixed here would degrade a Go+Rust monorepo's Go analysis and
	// duplicate the same caveat on two sources.
	return ScanOutcome{
		Analyses: analyses,
		Sources:  []analysis.Source{{Name: "ast-grep", Status: analysis.SourceAuthoritative}},
	}, nil
}

// scanDirectory analyzes all files in a directory while honoring caller cancellation.
// The existing internal timeout remains a best-effort empty-result safety cap.
func (s *AstGrepScanner) scanDirectory(parent context.Context, root string) ([]FileAnalysis, error) {
	if err := parent.Err(); err != nil {
		return nil, err
	}
	if !s.Available() {
		return nil, newIncompleteScanError("ast-grep", ScanSourceUnavailable, ErrAstGrepNotFound.Error(), ErrAstGrepNotFound)
	}

	inlineRules := s.inlineRules
	if inlineRules == "" && s.rulesDir != "" {
		// Preserve support for manually constructed scanners backed by a rule directory.
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
		inlineRules = strings.Join(rules, "\n---\n")
	}

	// Build command args, excluding nested git repos that ast-grep would
	// treat as separate repo boundaries (ignoring parent .gitignore)
	args := []string{"scan", "--inline-rules", inlineRules, "--json"}
	for _, repo := range findNestedGitRepos(root) {
		args = append(args, "--globs", "!"+repo+"/**")
	}
	args = append(args, root)

	ctx, cancel := context.WithTimeout(parent, astGrepScanTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, s.binary, args...)
	cmd.WaitDelay = 100 * time.Millisecond
	out, err := cmd.Output()
	if err != nil {
		if parentErr := parent.Err(); parentErr != nil {
			return nil, parentErr
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return nil, newIncompleteScanError("ast-grep", ScanSourceTimeout, fmt.Sprintf("ast-grep timed out after %s", astGrepScanTimeout), err)
		}

		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				return nil, newIncompleteScanError("ast-grep", ScanSourceFailed, fmt.Sprintf("ast-grep terminated by signal %d", status.Signal()), err)
			}
		}

		return nil, newIncompleteScanError("ast-grep", ScanSourceFailed, "ast-grep exited unsuccessfully", err)
	}
	if err := parent.Err(); err != nil {
		return nil, err
	}

	// Extract JSON array from output (handles debug output before JSON, e.g. ast-grep 0.40.2 bug)
	jsonData := extractJSONArray(out)
	if jsonData == nil {
		return nil, newIncompleteScanError("ast-grep", ScanSourceFailed, "ast-grep produced no JSON results", err)
	}

	var matches []ScanMatch
	if decodeErr := json.Unmarshal(jsonData, &matches); decodeErr != nil {
		return nil, newIncompleteScanError("ast-grep", ScanSourceFailed, "ast-grep produced invalid JSON results", decodeErr)
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

		if m.RuleID == "rust-mod-imports" || m.RuleID == "rust-path-module-imports" || m.RuleID == "rust-path-imports" || m.RuleID == "rust-use-imports" || m.RuleID == "rust-askama-template-imports" {
			var path string
			var explicitTarget string
			kind := "rust-path"
			switch m.RuleID {
			case "rust-mod-imports":
				kind = "rust-module"
				if pathVar, ok := m.MetaVariables.Single["PATH"]; ok {
					path = pathVar.Text
				}
			case "rust-path-module-imports":
				kind = "rust-path-module"
				if pathVar, ok := m.MetaVariables.Single["PATH"]; ok {
					path = pathVar.Text
				}
				if targetVar, ok := m.MetaVariables.Single["TARGET"]; ok {
					explicitTarget = targetVar.Text
				}
			case "rust-path-imports":
				path = m.Text
			case "rust-use-imports":
				if pathVar, ok := m.MetaVariables.Single["PATH"]; ok {
					path = pathVar.Text
				}
			case "rust-askama-template-imports":
				kind = "rust-askama-template"
				if targetVar, ok := m.MetaVariables.Single["TARGET"]; ok && !rustAttributeAssigns(m.Text, "config") {
					if _, literal := parseRustStringLiteral(targetVar.Text); literal {
						path = targetVar.Text
						explicitTarget = targetVar.Text
					}
				}
			}
			if path != "" {
				if m.RuleID != "rust-path-imports" && m.RuleID != "rust-askama-template-imports" {
					fileMap[relPath].Imports = append(fileMap[relPath].Imports, path)
				}
				fileMap[relPath].References = append(fileMap[relPath].References, ImportReference{
					Path:           path,
					Kind:           kind,
					Line:           m.Range.Start.Line,
					ExplicitTarget: explicitTarget,
				})
			}
		} else if strings.HasSuffix(m.RuleID, "-imports") {
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
		a.References = dedupeImportReferences(a.References)
		results = append(results, *a)
	}

	return results, nil
}

func dedupeImportReferences(refs []ImportReference) []ImportReference {
	seen := make(map[ImportReference]bool)
	result := make([]ImportReference, 0, len(refs))
	for _, ref := range refs {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		result = append(result, ref)
	}
	return result
}

// ruleIDToLang maps ast-grep rule ID prefixes to language names.
// Must cover every prefix used in sg-rules/*.yml files.
var ruleIDToLang = map[string]string{
	"go": "go", "ts": "typescript", "tsx": "typescript",
	"js": "javascript", "jsx": "javascript", "py": "python",
	"rust": "rust", "java": "java", "ruby": "ruby",
	"swift": "swift", "kotlin": "kotlin", "c": "c", "cpp": "cpp",
	"bash": "bash", "csharp": "csharp",
	"php": "php", "lua": "lua", "scala": "scala",
	"elixir": "elixir", "solidity": "solidity",
}

func detectLangFromRuleID(ruleID string) string {
	parts := strings.Split(ruleID, "-")
	if len(parts) > 0 {
		return ruleIDToLang[parts[0]]
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

	// C#: using Foo.Bar.Baz; / using static Foo.Bar; / using Alias = Foo.Bar.Baz;
	if strings.HasPrefix(text, "using ") {
		text = strings.TrimPrefix(text, "using ")
		text = strings.TrimSuffix(text, ";")
		text = strings.TrimSpace(text)

		text = strings.TrimPrefix(text, "static ")

		if idx := strings.Index(text, "="); idx >= 0 {
			text = text[idx+1:]
		}

		return strings.TrimSpace(text)
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

	case "java", "csharp":
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
	outcome, err := s.ScanDirectory(context.Background(), filepath.Dir(filePath))
	if err != nil {
		return nil, err
	}
	base := filepath.Base(filePath)
	for _, r := range outcome.Analyses {
		if filepath.Base(r.Path) == base {
			return &r, nil
		}
	}
	return nil, nil
}
