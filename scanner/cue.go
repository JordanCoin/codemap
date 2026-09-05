package scanner

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	textscanner "text/scanner"

	"codemap/analysis"
)

// scanCUEFiles extracts CUE package imports without evaluating the module.
// CUE packages are resolved later from cue.mod/module.cue and the file index.
func scanCUEFiles(ctx context.Context, root string, filters Filters) (ScanOutcome, error) {
	files, err := ScanFiles(ctx, root, NewGitIgnoreCache(root), filters.Only, filters.Exclude)
	if err != nil {
		return ScanOutcome{}, err
	}
	outcome, err := scanCUEFilesFromFiles(ctx, root, files)
	outcome.files = files
	outcome.hasFileInventory = err == nil
	return outcome, err
}

func scanCUEFilesFromFiles(ctx context.Context, root string, files []FileInfo) (ScanOutcome, error) {
	var analyses []FileAnalysis
	cueFiles := 0
	for _, file := range files {
		if !strings.EqualFold(filepath.Ext(file.Path), ".cue") {
			continue
		}
		cueFiles++
		if err := ctx.Err(); err != nil {
			return ScanOutcome{}, err
		}
		path := filepath.Clean(file.Path)
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			continue
		}
		pkg, imports := cueHeader(data)
		analyses = append(analyses, FileAnalysis{
			Path:     path,
			Language: "cue",
			Package:  pkg,
			Imports:  imports,
		})
	}
	if cueFiles == 0 {
		return ScanOutcome{}, nil
	}
	return ScanOutcome{
		Analyses: analyses,
		Sources: []analysis.Source{{
			Name:   "cue-imports",
			Status: analysis.SourceAuthoritative,
			Detail: "CUE package evaluation and external module loading are not graph edges",
		}},
	}, nil
}

func cueImports(data []byte) []string {
	_, imports := cueHeader(data)
	return imports
}

// CUE imports are valid only in the file preamble.
func cueHeader(data []byte) (string, []string) {
	s := newCUEScanner(data)

	var pkg string
	var imports []string
	for token := s.Scan(); token != textscanner.EOF; {
		if token != textscanner.Ident {
			break
		}
		switch s.TokenText() {
		case "package":
			if s.Scan() == textscanner.Ident {
				pkg = s.TokenText()
			}
			token = skipCUEHeaderLine(s)
		case "import":
			token = scanCUEImport(s, &imports)
		default:
			return pkg, dedupe(imports)
		}
	}
	return pkg, dedupe(imports)
}

func skipCUEHeaderLine(s *textscanner.Scanner) rune {
	line := s.Line
	for token := s.Scan(); token != textscanner.EOF; token = s.Scan() {
		if s.Line > line {
			return token
		}
	}
	return textscanner.EOF
}

func scanCUEImport(s *textscanner.Scanner, imports *[]string) rune {
	token := s.Scan()
	if token == '(' {
		for token = s.Scan(); token != textscanner.EOF && token != ')'; token = s.Scan() {
			if token == textscanner.String {
				if path, err := strconv.Unquote(s.TokenText()); err == nil && path != "" {
					*imports = append(*imports, path)
				}
			}
		}
		return s.Scan()
	}
	if token == textscanner.Ident {
		token = s.Scan()
	}
	if token == textscanner.String {
		if path, err := strconv.Unquote(s.TokenText()); err == nil && path != "" {
			*imports = append(*imports, path)
		}
	}
	return s.Scan()
}

func detectCUEModule(root string) string {
	return readCUEModule(filepath.Join(root, "cue.mod", "module.cue"))
}

type cueModuleInfo struct {
	path string
	root string
}

func detectCUEModulesWithFiles(root string, files []FileInfo) []cueModuleInfo {
	paths := map[string]bool{"cue.mod/module.cue": true}
	for _, file := range files {
		path := filepath.ToSlash(filepath.Clean(file.Path))
		if path == "cue.mod/module.cue" || strings.HasSuffix(path, "/cue.mod/module.cue") {
			paths[path] = true
		}
	}
	var modules []cueModuleInfo
	for path := range paths {
		module := readCUEModule(filepath.Join(root, filepath.FromSlash(path)))
		if module == "" {
			continue
		}
		moduleRoot := strings.TrimSuffix(path, "/cue.mod/module.cue")
		if moduleRoot == path {
			moduleRoot = ""
		}
		modules = append(modules, cueModuleInfo{path: module, root: moduleRoot})
	}
	sort.Slice(modules, func(i, j int) bool {
		if len(modules[i].root) != len(modules[j].root) {
			return len(modules[i].root) > len(modules[j].root)
		}
		return modules[i].root < modules[j].root
	})
	return modules
}

func readCUEModule(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	s := newCUEScanner(data)
	for token := s.Scan(); token != textscanner.EOF; token = s.Scan() {
		if token != textscanner.Ident || s.TokenText() != "module" {
			continue
		}
		if s.Scan() != ':' || s.Scan() != textscanner.String {
			continue
		}
		module, err := strconv.Unquote(s.TokenText())
		if err == nil {
			return normalizeCUEModule(module)
		}
	}
	return ""
}

func normalizeCUEModule(module string) string {
	module = strings.TrimSpace(module)
	marker := strings.LastIndex(module, "@v")
	if marker < 0 || marker+2 == len(module) {
		return module
	}
	for _, r := range module[marker+2:] {
		if r < '0' || r > '9' {
			return module
		}
	}
	return module[:marker]
}

func newCUEScanner(data []byte) *textscanner.Scanner {
	s := &textscanner.Scanner{}
	s.Init(strings.NewReader(string(data)))
	s.Mode = textscanner.ScanIdents | textscanner.ScanStrings | textscanner.ScanComments | textscanner.SkipComments
	s.Error = func(*textscanner.Scanner, string) {}
	return s
}
