// Package find ranks project files against a natural-language query using
// only what the dependency scanner already extracts: file paths and the
// function names inside them. It is lexical (BM25 over split identifiers),
// not semantic; a query has to share vocabulary with the code it wants.
package find

import (
	"context"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"unicode"

	"codemap/analysis"
	"codemap/scanner"
)

const (
	Schema       = "codemap.find/v1"
	DefaultLimit = 10
	bm25K1       = 1.2
	bm25B        = 0.75
	maxMatched   = 5
	// exactBonus is added once per query word that is a whole word of a
	// symbol or the file basename, so "theme" pulls loadTheme ahead of a file
	// whose path merely shares a directory token.
	exactBonus = 1.0
)

type Hit struct {
	Path      string   `json:"path"`
	Score     float64  `json:"score"`
	Matched   []string `json:"matched"`
	Importers int      `json:"importers"`
	Hub       bool     `json:"hub"`
}

type Report struct {
	Schema   string            `json:"schema"`
	Query    string            `json:"query"`
	Hits     []Hit             `json:"hits"`
	Coverage analysis.Coverage `json:"coverage"`
}

// Run scans root, ranks files against query, and annotates hits with importer
// counts from the same graph every other codemap command uses.
func Run(ctx context.Context, root string, filters scanner.Filters, query string, limit int) (Report, error) {
	outcome, err := scanner.ScanForDeps(ctx, root, filters)
	if err != nil {
		return Report{}, err
	}
	report := Report{Schema: Schema, Query: query, Hits: Rank(outcome.Analyses, query, limit)}
	sources := outcome.Sources
	if fg, graphErr := scanner.BuildFileGraphFromOutcome(ctx, root, outcome, filters); graphErr == nil {
		sources = fg.Coverage.Sources
		for i := range report.Hits {
			importers := fg.Importers[report.Hits[i].Path]
			report.Hits[i].Importers = len(importers)
			report.Hits[i].Hub = scanner.CountHubImporters(importers) >= scanner.HubThreshold
		}
	}
	report.Coverage = scanner.CoverageFromSources(sources)
	return report, nil
}

// Rank scores every analysis against query and returns the top limit hits
// with a positive score. Importer fields are left zero; Run fills them.
func Rank(analyses []scanner.FileAnalysis, query string, limit int) []Hit {
	if limit <= 0 {
		limit = DefaultLimit
	}
	queryTokens := tokenize(query)
	if len(queryTokens) == 0 {
		return nil
	}
	rawQuery := rawTokens(query)

	docs := make([][]string, len(analyses))
	df := make(map[string]int)
	totalLen := 0
	for i, a := range analyses {
		docs[i] = tokenize(strings.Join(append([]string{a.Path}, a.Functions...), " "))
		totalLen += len(docs[i])
		for _, tok := range unique(docs[i]) {
			df[tok]++
		}
	}
	n := float64(len(analyses))
	avgLen := 1.0
	if n > 0 {
		avgLen = math.Max(1, float64(totalLen)/n)
	}

	hits := make([]Hit, 0, len(analyses))
	for i, a := range analyses {
		tf := make(map[string]int, len(docs[i]))
		for _, tok := range docs[i] {
			tf[tok]++
		}
		score := 0.0
		for _, q := range queryTokens {
			f := float64(tf[q])
			if f == 0 {
				continue
			}
			idf := math.Log((n-float64(df[q])+0.5)/(float64(df[q])+0.5) + 1)
			score += idf * (f * (bm25K1 + 1)) / (f + bm25K1*(1-bm25B+bm25B*float64(len(docs[i]))/avgLen))
		}
		matched := matchedNames(a, rawQuery)
		score += exactBonus * float64(countExact(a, rawQuery))
		if score <= 0 {
			continue
		}
		hits = append(hits, Hit{Path: a.Path, Score: round(score), Matched: matched})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Path < hits[j].Path
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// Render writes the compact text form: one block per hit, then coverage.
func Render(w io.Writer, r Report) {
	if len(r.Hits) == 0 {
		fmt.Fprintf(w, "No files match %q\n", r.Query)
	}
	for _, h := range r.Hits {
		fmt.Fprintln(w, h.Path)
		if len(h.Matched) > 0 {
			fmt.Fprintf(w, "  matched: %s\n", strings.Join(h.Matched, ", "))
		}
		hub := ""
		if h.Hub {
			hub = " (hub)"
		}
		fmt.Fprintf(w, "  importers: %d%s\n", h.Importers, hub)
	}
	notes := make([]string, 0, len(r.Coverage.Sources))
	for _, s := range r.Coverage.Sources {
		if s.Detail != "" {
			notes = append(notes, s.Detail)
		}
	}
	if len(notes) == 0 {
		fmt.Fprintf(w, "Coverage: %s\n", r.Coverage.Status)
		return
	}
	fmt.Fprintf(w, "Coverage: %s — %s\n", r.Coverage.Status, strings.Join(notes, "; "))
}

// tokenize splits identifiers and paths into lowercase words: camelCase,
// snake_case, kebab-case, dots and slashes are all boundaries.
func tokenize(s string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) >= 2 {
			out = append(out, strings.ToLower(string(cur)))
		}
		cur = cur[:0]
	}
	runes := []rune(s)
	for i, r := range runes {
		switch {
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			flush()
		case unicode.IsUpper(r) && len(cur) > 0 && (!unicode.IsUpper(runes[i-1]) ||
			// HTTPServer: split before the last upper of an acronym run.
			i+1 < len(runes) && unicode.IsLower(runes[i+1])):
			flush()
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return out
}

// rawTokens keeps whole query words (lowercased) for the exact-substring bonus.
func rawTokens(s string) []string {
	return unique(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}))
}

func names(a scanner.FileAnalysis) []string {
	base := a.Path
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	return append([]string{base}, a.Functions...)
}

// hasWord reports whether q is one of name's split words (loadTheme has
// "theme"; DataModel does not have "mode").
func hasWord(name, q string) bool {
	for _, w := range tokenize(name) {
		if w == q {
			return true
		}
	}
	return false
}

func matchedNames(a scanner.FileAnalysis, raw []string) []string {
	var out []string
	for _, name := range names(a) {
		for _, q := range raw {
			if hasWord(name, q) {
				out = append(out, name)
				break
			}
		}
		if len(out) == maxMatched {
			break
		}
	}
	return out
}

func countExact(a scanner.FileAnalysis, raw []string) int {
	count := 0
	for _, q := range raw {
		for _, name := range names(a) {
			if hasWord(name, q) {
				count++
				break
			}
		}
	}
	return count
}

func unique(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := in[:0:0]
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func round(f float64) float64 { return math.Round(f*1000) / 1000 }
