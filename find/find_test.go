package find

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
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

func TestRunFallsBackToBodyText(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":              "module fixture\n\ngo 1.24\n",
		"routing/matcher.go":  "package routing\n\n// Each converter turns a segment into values.\nfunc Match(rule string) []string {\n\tvar values []string\n\tfor _, converter := range rule {\n\t\tvalues = append(values, string(converter))\n\t}\n\treturn values\n}\n",
		"routing/rules.go":    "package routing\n\nfunc Compile() {}\n",
		"wrappers/request.go": "package wrappers\n\nfunc Parse() {}\n",
		"wrappers/notes.go":   "package wrappers\n\n// match converter values\nfunc Notes() {}\n",
	}
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	report, err := Run(context.Background(), root, scanner.Filters{}, "wrong converter values segment order", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Hits) == 0 || report.Hits[0].Path != "routing/matcher.go" {
		t.Fatalf("body-only match should rank first, got %+v", report.Hits)
	}
	if got := strings.Join(report.Hits[0].Matched, ","); got != "(body)" {
		t.Fatalf("matched = %q, want (body)", got)
	}

	// Same body words, but matcher.go also matches by name: it outranks the
	// body-only file and keeps its name tag.
	report, err = Run(context.Background(), root, scanner.Filters{}, "match converter values", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Hits) < 2 || report.Hits[0].Path != "routing/matcher.go" || report.Hits[1].Path != "wrappers/notes.go" {
		t.Fatalf("name+body should outrank body-only, got %+v", report.Hits)
	}
	if got := strings.Join(report.Hits[0].Matched, ","); got != "Match" {
		t.Fatalf("matched = %q, want Match", got)
	}
}

func TestBodyHitsHonorsCancellation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n// converter values\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	hits, err := bodyHits(ctx, root, []scanner.FileAnalysis{{Path: "a.go"}}, "converter values")
	if err == nil || len(hits) != 0 {
		t.Fatalf("cancelled ctx: hits=%+v err=%v, want no hits and an error", hits, err)
	}
	if _, err := Run(ctx, root, scanner.Filters{}, "converter values", 5); err == nil {
		t.Fatal("Run with cancelled ctx returned no error")
	}
}
