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
func scanCUEFiles(ctx context.Context, root string) (ScanOutcome, error) {
	files, err := ScanFiles(ctx, root, NewGitIgnoreCache(root), nil, nil)
	if err != nil {
		return ScanOutcome{}, err
	}

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
		analyses = append(analyses, FileAnalysis{
			Path:     path,
			Language: "cue",
			Imports:  cueImports(data),
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
	s := newCUEScanner(data)

	var imports []string
	for token := s.Scan(); token != textscanner.EOF; token = s.Scan() {
		if token != textscanner.Ident || s.TokenText() != "import" {
			continue
		}
		token = s.Scan()
		if token == '(' {
			for token = s.Scan(); token != textscanner.EOF && token != ')'; token = s.Scan() {
				if token == textscanner.String {
					if path, err := strconv.Unquote(s.TokenText()); err == nil && path != "" {
						imports = append(imports, path)
					}
				}
			}
			continue
		}
		if token == textscanner.Ident {
			token = s.Scan()
		}
		if token == textscanner.String {
			if path, err := strconv.Unquote(s.TokenText()); err == nil && path != "" {
				imports = append(imports, path)
			}
		}
	}
	return dedupe(imports)
}

func detectCUEModule(root string) string {
	return readCUEModule(filepath.Join(root, "cue.mod", "module.cue"))
}

func detectCUEModuleWithFiles(root string, files []FileInfo) (string, string) {
	paths := []string{"cue.mod/module.cue"}
	for _, file := range files {
		path := filepath.Clean(file.Path)
		if filepath.Base(path) == "module.cue" && filepath.Base(filepath.Dir(path)) == "cue.mod" {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		if module := readCUEModule(filepath.Join(root, path)); module != "" {
			return module, filepath.Dir(filepath.Dir(path))
		}
	}
	return "", ""
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
			return module
		}
	}
	return ""
}

func newCUEScanner(data []byte) *textscanner.Scanner {
	s := &textscanner.Scanner{}
	s.Init(strings.NewReader(string(data)))
	s.Mode = textscanner.ScanIdents | textscanner.ScanStrings | textscanner.ScanComments | textscanner.SkipComments
	s.Error = func(*textscanner.Scanner, string) {}
	return s
}
