package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"codemap/render"
	"codemap/scanner"
)

const (
	maxSymbolFiles    = 5
	maxSymbolsPerFile = 3
)

// countLines returns physical line counts for files that are not assets,
// not binary (NUL in the first 8000 bytes) and at most render.MaxLineCountBytes.
func countLines(root string, files []scanner.FileInfo) map[string]int {
	counts := make(map[string]int, len(files))
	for _, f := range files {
		if render.IsAssetExtension(strings.ToLower(f.Ext)) {
			continue
		}
		data, ok := readRegularFile(filepath.Join(root, f.Path))
		if !ok || bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
			continue
		}
		n := bytes.Count(data, []byte{'\n'})
		if len(data) > 0 && data[len(data)-1] != '\n' {
			n++
		}
		counts[filepath.ToSlash(f.Path)] = n
	}
	return counts
}

// readRegularFile reads path only when it is a regular file (not a symlink,
// FIFO or device) of at most render.MaxLineCountBytes. The scan's size is not
// trusted: the read itself is capped.
func readRegularFile(path string) ([]byte, bool) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, false
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	if opened, err := f.Stat(); err != nil || !os.SameFile(info, opened) {
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(f, render.MaxLineCountBytes+1))
	if err != nil || len(data) > render.MaxLineCountBytes {
		return nil, false
	}
	return data, true
}

// hubSymbols lists up to maxSymbolsPerFile exported top-level symbols for
// the first maxSymbolFiles single-file hubs. A multi-file Go package is
// skipped: its importers name the package, not one file.
func hubSymbols(root string, hubs []scanner.HubEntry) map[string][]scanner.Symbol {
	sg, err := scanner.NewAstGrepScanner()
	if err != nil || !sg.Available() {
		return nil
	}
	defer sg.Close()
	out := map[string][]scanner.Symbol{}
	picked := 0
	for _, h := range hubs {
		if h.IsPackage() {
			continue
		}
		if picked == maxSymbolFiles {
			break
		}
		picked++
		if syms := fileSymbols(sg, root, h.Path); len(syms) > 0 {
			out[h.Path] = syms
		}
	}
	return out
}

// fileSymbols keeps a symbol only when the source line ast-grep reported
// names it, so a wrong line number drops the symbol instead of printing it.
func fileSymbols(sg *scanner.AstGrepScanner, root, rel string) []scanner.Symbol {
	outcome, err := sg.ScanDirectory(context.Background(), filepath.Join(root, filepath.Dir(rel)))
	if err != nil {
		return nil
	}
	var analysis *scanner.FileAnalysis
	for i := range outcome.Analyses {
		if filepath.ToSlash(outcome.Analyses[i].Path) == pathBase(rel) {
			analysis = &outcome.Analyses[i]
		}
	}
	src, ok := readRegularFile(filepath.Join(root, rel))
	if analysis == nil || !ok {
		return nil
	}
	lines := strings.Split(string(src), "\n")
	var syms []scanner.Symbol
	for name, line := range analysis.SymbolLines {
		if line < 0 || line >= len(lines) {
			continue
		}
		text := lines[line]
		if !regexp.MustCompile(`\b`+regexp.QuoteMeta(name)+`\b`).MatchString(text) || !exportedTopLevel(analysis.Language, name, text) {
			continue
		}
		syms = append(syms, scanner.Symbol{Name: name, Line: line + 1})
	}
	sort.Slice(syms, func(i, j int) bool { return syms[i].Line < syms[j].Line })
	return syms[:min(len(syms), maxSymbolsPerFile)]
}

func pathBase(rel string) string { return filepath.Base(filepath.FromSlash(rel)) }

// exportedTopLevel: the declaration starts its line (no indentation) and is
// visible outside the file by the language's rule.
func exportedTopLevel(language, name, text string) bool {
	if text == "" || text[0] == ' ' || text[0] == '\t' {
		return false
	}
	switch language {
	case "go":
		r, _ := utf8.DecodeRuneInString(name)
		return unicode.IsUpper(r) && !strings.HasPrefix(text, "func (")
	case "python":
		return !strings.HasPrefix(name, "_")
	case "typescript", "javascript":
		return strings.HasPrefix(text, "export ")
	}
	return true
}
