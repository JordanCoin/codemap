package scanner

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

var errGoFallbackUnavailable = errors.New("go dependency fallback unavailable")

func buildGoFallbackOutcome(ctx context.Context, root string, files []FileInfo) (ScanOutcome, error) {
	var analyses []FileAnalysis
	goFiles := 0
	skipped := 0
	fset := token.NewFileSet()

	for _, file := range files {
		if !strings.EqualFold(filepath.Ext(file.Path), ".go") {
			continue
		}
		goFiles++
		if err := ctx.Err(); err != nil {
			return ScanOutcome{}, err
		}

		path := filepath.Clean(file.Path)
		parsed, err := parser.ParseFile(fset, filepath.Join(root, path), nil, parser.SkipObjectResolution)
		if err != nil {
			skipped++
			continue
		}

		analysis := FileAnalysis{Path: path, Language: "go"}
		for _, spec := range parsed.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err == nil && importPath != "" {
				analysis.Imports = append(analysis.Imports, importPath)
			}
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if ok && function.Name != nil && function.Name.Name != "" {
				analysis.Functions = append(analysis.Functions, function.Name.Name)
			}
		}
		analysis.Imports = dedupe(analysis.Imports)
		analysis.Functions = dedupe(analysis.Functions)
		if len(analysis.Imports) > 0 || len(analysis.Functions) > 0 {
			analyses = append(analyses, analysis)
		}
	}

	if len(analyses) == 0 {
		return ScanOutcome{}, errGoFallbackUnavailable
	}
	sort.Slice(analyses, func(i, j int) bool {
		return analyses[i].Path < analyses[j].Path
	})

	detail := fmt.Sprintf("Go parser fallback recovered %d of %d Go files", len(analyses), goFiles)
	if skipped > 0 {
		detail += fmt.Sprintf(" (skipped %d with parse errors)", skipped)
	}
	return ScanOutcome{
		Analyses: analyses,
		Sources: []ScanSourceOutcome{{
			Name:   "go-parser",
			Status: ScanSourceFallback,
			Detail: detail,
		}},
	}, nil
}
