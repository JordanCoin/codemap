package find

import (
	"bytes"
	"strings"
	"testing"

	"codemap/analysis"
	"codemap/scanner"
)

func TestRankPrefersSymbolVocabulary(t *testing.T) {
	analyses := []scanner.FileAnalysis{
		{Path: "src/theme/use-theme.ts", Functions: []string{"useTheme", "saveTheme"}},
		{Path: "src/storage/preferences.ts", Functions: []string{"loadTheme", "persistPreference"}},
		{Path: "src/net/client.ts", Functions: []string{"fetchJSON", "retry"}},
	}
	hits := Rank(analyses, "theme persistence", 10)
	if len(hits) != 2 {
		t.Fatalf("want 2 hits, got %+v", hits)
	}
	// Lexical, no stemming: "persistence" does not match persistPreference,
	// so the file that says "theme" three times ranks first.
	if hits[0].Path != "src/theme/use-theme.ts" || hits[1].Path != "src/storage/preferences.ts" {
		t.Fatalf("theme files should rank, unrelated dropped, got %+v", hits)
	}
	if got := strings.Join(hits[0].Matched, ","); got != "use-theme.ts,useTheme,saveTheme" {
		t.Fatalf("matched = %q", got)
	}
	if got := tokenize("HTTPServer_loadTheme.v2"); strings.Join(got, " ") != "http server load theme v2" {
		t.Fatalf("tokenize = %v", got)
	}
}

func TestRenderShape(t *testing.T) {
	var buf bytes.Buffer
	Render(&buf, Report{
		Query: "theme",
		Hits:  []Hit{{Path: "a/b.swift", Matched: []string{"loadTheme"}, Importers: 3, Hub: true}},
		Coverage: analysis.Coverage{Status: analysis.CoveragePartial, Sources: []analysis.Source{
			{Name: "symbol-imports/swift", Status: analysis.SourceUnavailable, Detail: "Swift edges missing"},
		}},
	})
	want := "a/b.swift\n  matched: loadTheme\n  importers: 3 (hub)\nCoverage: partial — Swift edges missing\n"
	if buf.String() != want {
		t.Fatalf("render =\n%s\nwant\n%s", buf.String(), want)
	}
}
