package ask

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codemap/find"
	"codemap/scanner"
)

func TestRunReturnsMatchWithCompactCards(t *testing.T) {
	root := writeAskFixture(t)
	answer, err := Run(context.Background(), root, scanner.Filters{}, "UseTheme SaveTheme", Options{Limit: 5, BudgetBytes: 900})
	if err != nil {
		t.Fatal(err)
	}
	if answer.Status != StatusMatch {
		t.Fatalf("status = %s message = %q", answer.Status, answer.Message)
	}
	if !answer.Experimental || answer.Schema != Schema {
		t.Fatalf("experimental markers missing: %+v", answer)
	}
	if len(answer.Cards) == 0 {
		t.Fatal("expected at least one card")
	}
	text := RenderText(answer)
	if !strings.Contains(text, "EXPERIMENTAL") || !strings.Contains(text, "MATCH") {
		t.Fatalf("render missing markers:\n%s", text)
	}
	if answer.UsedBytes <= 0 {
		t.Fatalf("used_bytes=%d", answer.UsedBytes)
	}
	for _, card := range answer.Cards {
		if card.ID == "" || card.Revision == "" || card.Capsule == "" || card.Label == "" {
			t.Fatalf("incomplete card: %+v", card)
		}
		if len(strings.Fields(card.Label)) > 15 {
			t.Fatalf("label too long: %q", card.Label)
		}
	}
}

func TestRunReturnsExplicitNoMatch(t *testing.T) {
	root := writeAskFixture(t)
	answer, err := Run(context.Background(), root, scanner.Filters{}, "quantum flux capacitor zucchini", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if answer.Status != StatusNoMatch {
		t.Fatalf("status = %s cards=%+v", answer.Status, answer.Cards)
	}
	if !strings.Contains(answer.Message, StatusNoMatch) {
		t.Fatalf("message = %q", answer.Message)
	}
	if len(answer.Cards) != 0 {
		t.Fatalf("NO_MATCH must not return cards, got %+v", answer.Cards)
	}
}

func TestRunEmptyQueryIsNoMatch(t *testing.T) {
	answer, err := Run(context.Background(), t.TempDir(), scanner.Filters{}, "  ", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if answer.Status != StatusNoMatch || !strings.Contains(answer.Message, "empty query") {
		t.Fatalf("got %+v", answer)
	}
}

func TestAdaptiveShortlistStopsOnWeakTrail(t *testing.T) {
	hits := []find.Hit{
		{Path: "a.go", Score: 8.0},
		{Path: "b.go", Score: 6.0},
		{Path: "c.go", Score: 2.0}, // below relativeKeep of best
		{Path: "d.go", Score: 0.1}, // below floor
	}
	got := adaptiveShortlist(hits)
	if len(got) != 2 || got[0].Path != "a.go" || got[1].Path != "b.go" {
		t.Fatalf("shortlist = %+v", got)
	}
}

func TestFilterReliableHitsRequiresOverlap(t *testing.T) {
	hits := []find.Hit{
		{Path: "main_test.go", Score: 5.5, Matched: []string{"TestNonexistentPath"}},
		{Path: "theme/use_theme.go", Score: 9.0, Matched: []string{"UseTheme", "SaveTheme"}},
	}
	if got := filterReliableHits(hits, "quantum flux capacitor zucchini"); len(got) != 0 {
		t.Fatalf("expected NO reliable hits for nonsense, got %+v", got)
	}
	if got := filterReliableHits(hits, "nonexistent flibbertigibbet xyzzy"); len(got) != 0 {
		t.Fatalf("single accidental token among many should NO_MATCH, got %+v", got)
	}
	got := filterReliableHits(hits, "UseTheme SaveTheme")
	if len(got) != 1 || got[0].Path != "theme/use_theme.go" {
		t.Fatalf("UseTheme SaveTheme filter = %+v", got)
	}
	if got := filterReliableHits(hits, "theme persistence"); len(got) != 0 {
		// Lexical only: "persistence" does not stem to Persist/Preference.
		t.Fatalf("synonym-only second token should stay NO_MATCH in this slice, got %+v", got)
	}
}

func TestSelectWithinBudgetKeepsSmallestPrefix(t *testing.T) {
	cards := []Card{
		{ID: "1", Label: "one", Capsule: "alpha beta gamma", Paths: []string{"a.go"}, Score: 2},
		{ID: "2", Label: "two", Capsule: strings.Repeat("word ", 40), Paths: []string{"b.go"}, Score: 1.5},
		{ID: "3", Label: "three", Capsule: "delta", Paths: []string{"c.go"}, Score: 1.2},
	}
	kept := selectWithinBudget(cards, 220)
	if len(kept) < 1 || kept[0].ID != "1" {
		t.Fatalf("kept = %+v", kept)
	}
	if len(kept) > 2 {
		t.Fatalf("budget should stop early, kept %d", len(kept))
	}
}

func TestDedupPackageCardsShareLogicalID(t *testing.T) {
	hit := find.Hit{Path: "pkg/a.go", Package: "example.com/pkg", Score: 1.2, Importers: 3, Hub: true, Matched: []string{"Load"}}
	analysis := scanner.FileAnalysis{
		Path:      "pkg/a.go",
		Functions: []string{"Load", "Save", "Parse", "Validate", "Reset", "Extra"},
		Types:     []string{"Config"},
		Imports:   []string{"os"},
	}
	graph := &scanner.FileGraph{
		Importers: map[string][]string{"pkg/a.go": {"b/one.go", "b/two.go", "b/three.go", "b/four.go"}},
		Imports:   map[string][]string{"pkg/a.go": {"c/dep.go", "c/other.go"}},
	}
	first := buildCard(hit, analysis, graph)
	second := buildCard(hit, analysis, graph)
	if first.ID != second.ID || first.ID != "package:example.com/pkg" {
		t.Fatalf("ids = %q %q", first.ID, second.ID)
	}
	if first.Revision != second.Revision {
		t.Fatal("identical capsules should share revision")
	}
	if len(first.Links) == 0 || !strings.Contains(first.Capsule, "symbols:") {
		t.Fatalf("card should include graph links and symbols: %+v", first)
	}
	if got := trimTokens(strings.Repeat("word ", 20), 5); len(strings.Fields(got)) != 5 {
		t.Fatalf("trimTokens = %q", got)
	}
	if got := uniqueStable([]string{"", "b", "a", "b"}); strings.Join(got, ",") != "a,b" {
		t.Fatalf("uniqueStable = %v", got)
	}
	var buf strings.Builder
	Render(&buf, Answer{Schema: Schema, Status: StatusMatch, Message: matchMessage(nil), Cards: []Card{first}, BudgetBytes: 900})
	if !strings.Contains(buf.String(), first.Paths[0]) {
		t.Fatalf("Render missing path:\n%s", buf.String())
	}
}

func writeAskFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":                 "module fixture\n\ngo 1.24\n",
		"theme/use_theme.go":     "package theme\n\nfunc UseTheme() {}\nfunc SaveTheme() {}\n",
		"storage/preferences.go": "package storage\n\nfunc LoadTheme() {}\nfunc PersistPreference() {}\n",
		"net/client.go":          "package net\n\nfunc FetchJSON() {}\nfunc Retry() {}\n",
		"unrelated/math.go":      "package unrelated\n\nfunc Add(a, b int) int { return a + b }\n",
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
	return root
}
