// Package ask is an EXPERIMENTAL bartender-inspired retrieval surface.
//
// It builds compact, deduplicated summary cards from codemap's existing
// dependency map and returns the smallest sufficient answer for a question,
// or an explicit NO_MATCH when nothing is reliable enough.
//
// Design credit: [bartender](https://github.com/everoo/bartender) by everoo
// (Alexandria cards, Adaptive Fuzzy Retrieval, smallest-sufficient / NO_MATCH).
// See docs/design/BARTENDER-ASK.md for the concept map and open questions.
package ask

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"unicode"

	"codemap/analysis"
	"codemap/find"
	"codemap/scanner"
)

const (
	Schema        = "codemap.ask/v1-experimental"
	StatusMatch   = "MATCH"
	StatusNoMatch = "NO_MATCH"

	// DefaultLimit is how many find hits to consider before adaptive cut.
	DefaultLimit = 8
	// DefaultBudgetBytes caps the rendered answer (cards + framing).
	DefaultBudgetBytes = 1200
	// minScoreFloor rejects weak lexical matches as unreliable for a MATCH.
	minScoreFloor = 1.5
	// relativeKeep keeps a trailing hit only when it is within this fraction
	// of the best score (adaptive shortlist, not a fixed top-K dump).
	relativeKeep = 0.45
	maxSymbols   = 5
	maxLinks     = 3
	maxCards     = 5
)

// Card is a compact routing card derived from the codemap map (Alexandria-inspired).
type Card struct {
	ID       string   `json:"id"`
	Revision string   `json:"revision"`
	Type     string   `json:"type"`
	Label    string   `json:"label"`
	Capsule  string   `json:"capsule"`
	Paths    []string `json:"paths"`
	Links    []string `json:"links,omitempty"`
	Score    float64  `json:"score,omitempty"`
}

// Answer is the smallest sufficient retrieval outcome for one question.
type Answer struct {
	Schema       string            `json:"schema"`
	Experimental bool              `json:"experimental"`
	Status       string            `json:"status"`
	Query        string            `json:"query"`
	Message      string            `json:"message"`
	Cards        []Card            `json:"cards,omitempty"`
	Coverage     analysis.Coverage `json:"coverage"`
	BudgetBytes  int               `json:"budget_bytes"`
	UsedBytes    int               `json:"used_bytes"`
}

// Options controls retrieval budgets for one ask call.
type Options struct {
	Limit       int
	BudgetBytes int
}

// Run answers query from root's existing codemap scan: find ranks candidates,
// cards summarize them, and adaptive early-stop keeps only what fits the budget.
func Run(ctx context.Context, root string, filters scanner.Filters, query string, opts Options) (Answer, error) {
	query = strings.TrimSpace(query)
	answer := Answer{
		Schema:       Schema,
		Experimental: true,
		Query:        query,
		BudgetBytes:  opts.BudgetBytes,
	}
	if answer.BudgetBytes <= 0 {
		answer.BudgetBytes = DefaultBudgetBytes
	}
	if query == "" {
		answer.Status = StatusNoMatch
		answer.Message = noMatchMessage("empty query")
		answer.UsedBytes = len(answer.Message)
		return answer, nil
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}

	outcome, err := scanner.ScanForDeps(ctx, root, filters)
	if err != nil {
		return Answer{}, err
	}
	answer.Coverage = scanner.CoverageFromSources(outcome.Sources)

	hits := find.Rank(outcome.Analyses, query, limit)
	var graph *scanner.FileGraph
	if fg, graphErr := scanner.BuildFileGraphFromOutcome(ctx, root, outcome, filters); graphErr == nil {
		graph = fg
		answer.Coverage = scanner.CoverageFromSources(fg.Coverage.Sources)
		for i := range hits {
			importers := fg.Importers[hits[i].Path]
			hits[i].Importers = len(importers)
			hits[i].Hub = fg.IsFileHub(hits[i].Path)
			if pkg, shared := fg.SharedGoPackage(hits[i].Path); shared {
				hits[i].Package = pkg
			}
		}
	}

	reliable := filterReliableHits(hits, query)
	if len(reliable) == 0 {
		answer.Status = StatusNoMatch
		answer.Message = noMatchMessage("")
		answer.UsedBytes = len(RenderText(answer))
		return answer, nil
	}

	byPath := make(map[string]scanner.FileAnalysis, len(outcome.Analyses))
	for _, a := range outcome.Analyses {
		byPath[a.Path] = a
	}

	shortlist := adaptiveShortlist(reliable)
	cards := make([]Card, 0, len(shortlist))
	seen := make(map[string]struct{})
	for _, hit := range shortlist {
		card := buildCard(hit, byPath[hit.Path], graph)
		if _, dup := seen[card.ID]; dup {
			continue
		}
		seen[card.ID] = struct{}{}
		cards = append(cards, card)
	}

	kept := selectWithinBudget(cards, answer.BudgetBytes)
	if len(kept) == 0 {
		answer.Status = StatusNoMatch
		answer.Message = noMatchMessage("candidates exceeded compact budget")
		answer.UsedBytes = len(RenderText(answer))
		return answer, nil
	}

	answer.Status = StatusMatch
	answer.Cards = kept
	answer.Message = matchMessage(kept)
	answer.UsedBytes = len(RenderText(answer))
	return answer, nil
}

func filterReliableHits(hits []find.Hit, query string) []find.Hit {
	qtokens := splitIdentTokens(query)
	if len(qtokens) == 0 {
		return nil
	}
	out := make([]find.Hit, 0, len(hits))
	for _, hit := range hits {
		if hit.Score < minScoreFloor {
			continue
		}
		overlap := overlapQueryTokens(hit, qtokens)
		need := 1
		if len(qtokens) >= 2 {
			// Multi-word questions must share at least two tokens with the
			// card surface so a lone common verb ("save") cannot MATCH.
			need = 2
		}
		if overlap < need {
			continue
		}
		out = append(out, hit)
	}
	return out
}

// splitIdentTokens mirrors find's identifier splitting: camelCase, snake_case,
// path separators, and punctuation all become lowercase words of length >= 2.
func splitIdentTokens(s string) []string {
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
			i+1 < len(runes) && unicode.IsLower(runes[i+1])):
			flush()
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	seen := make(map[string]struct{}, len(out))
	uniq := out[:0]
	for _, tok := range out {
		if _, ok := seen[tok]; ok {
			continue
		}
		seen[tok] = struct{}{}
		uniq = append(uniq, tok)
	}
	return uniq
}

func overlapQueryTokens(hit find.Hit, qtokens []string) int {
	parts := splitIdentTokens(hit.Path + " " + strings.Join(hit.Matched, " "))
	have := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		have[p] = struct{}{}
	}
	n := 0
	for _, q := range qtokens {
		if _, ok := have[q]; ok {
			n++
		}
	}
	return n
}

func adaptiveShortlist(hits []find.Hit) []find.Hit {
	if len(hits) == 0 {
		return nil
	}
	best := hits[0].Score
	out := make([]find.Hit, 0, len(hits))
	for _, hit := range hits {
		if hit.Score < minScoreFloor {
			break
		}
		if hit.Score < best*relativeKeep && len(out) > 0 {
			break
		}
		out = append(out, hit)
		if len(out) >= maxCards {
			break
		}
	}
	return out
}

func buildCard(hit find.Hit, analysis scanner.FileAnalysis, graph *scanner.FileGraph) Card {
	cardType := "file"
	if hit.Hub {
		cardType = "hub"
	}
	if hit.Package != "" {
		cardType = "package"
	}

	base := path.Base(hit.Path)
	label := base
	if hit.Hub {
		label += " hub"
	}
	if hit.Package != "" {
		label = hit.Package + " package"
	}
	label = trimTokens(label, 15)

	symbols := append([]string{}, analysis.Functions...)
	symbols = append(symbols, analysis.Types...)
	if len(symbols) > maxSymbols {
		symbols = symbols[:maxSymbols]
	}

	var links []string
	importers, imports := 0, len(analysis.Imports)
	if graph != nil {
		importers = len(graph.Importers[hit.Path])
		if list := graph.Importers[hit.Path]; len(list) > 0 {
			links = append(links, takeBasenames(list, maxLinks)...)
		}
		if list := graph.Imports[hit.Path]; len(list) > 0 {
			links = append(links, takeBasenames(list, maxLinks)...)
		}
		if imports == 0 {
			imports = len(graph.Imports[hit.Path])
		}
	}
	links = uniqueStable(links)

	lang := analysis.Language
	if lang == "" {
		lang = strings.TrimPrefix(path.Ext(hit.Path), ".")
	}

	parts := []string{lang}
	if importers > 0 || hit.Importers > 0 {
		n := importers
		if n == 0 {
			n = hit.Importers
		}
		parts = append(parts, fmt.Sprintf("←%d", n))
	}
	if imports > 0 {
		parts = append(parts, fmt.Sprintf("→%d", imports))
	}
	if len(symbols) > 0 {
		parts = append(parts, "symbols: "+strings.Join(symbols, ", "))
	}
	if len(hit.Matched) > 0 {
		parts = append(parts, "matched: "+strings.Join(hit.Matched, ", "))
	}
	capsule := trimTokens(strings.Join(parts, " · "), 55)

	id := "file:" + hit.Path
	if hit.Package != "" {
		id = "package:" + hit.Package
	}
	revision := shortHash(id + "|" + capsule)

	return Card{
		ID:       id,
		Revision: revision,
		Type:     cardType,
		Label:    label,
		Capsule:  capsule,
		Paths:    []string{hit.Path},
		Links:    links,
		Score:    hit.Score,
	}
}

func selectWithinBudget(cards []Card, budget int) []Card {
	if budget <= 0 {
		budget = DefaultBudgetBytes
	}
	framing := len(matchMessage(nil)) + 64
	remaining := budget - framing
	if remaining < 80 {
		remaining = 80
	}
	kept := make([]Card, 0, len(cards))
	used := 0
	for _, card := range cards {
		cost := len(card.Label) + len(card.Capsule) + len(card.ID) + 24
		for _, p := range card.Paths {
			cost += len(p)
		}
		if len(kept) > 0 && used+cost > remaining {
			break
		}
		kept = append(kept, card)
		used += cost
		if len(kept) >= maxCards {
			break
		}
	}
	return kept
}

func matchMessage(cards []Card) string {
	if len(cards) == 0 {
		return "MATCH — compact cards selected"
	}
	if len(cards) == 1 {
		return "MATCH — smallest sufficient card"
	}
	return fmt.Sprintf("MATCH — %d compact cards (adaptive shortlist)", len(cards))
}

func noMatchMessage(detail string) string {
	base := "NO_MATCH — no sufficiently reliable relevant record found"
	if detail == "" {
		return base
	}
	return base + " (" + detail + ")"
}

// RenderText writes the agent-facing compact answer.
func RenderText(a Answer) string {
	var b strings.Builder
	fmt.Fprintf(&b, "EXPERIMENTAL codemap ask (%s)\n", a.Schema)
	fmt.Fprintf(&b, "%s\n", a.Message)
	if a.Query != "" {
		fmt.Fprintf(&b, "query: %s\n", a.Query)
	}
	for i, card := range a.Cards {
		fmt.Fprintf(&b, "%d. %s [%s] %s\n", i+1, card.Paths[0], card.Type, card.Label)
		fmt.Fprintf(&b, "   %s\n", card.Capsule)
		if len(card.Links) > 0 {
			fmt.Fprintf(&b, "   links: %s\n", strings.Join(card.Links, ", "))
		}
		fmt.Fprintf(&b, "   id=%s rev=%s score=%.3f\n", card.ID, card.Revision, card.Score)
	}
	notes := make([]string, 0, len(a.Coverage.Sources))
	for _, s := range a.Coverage.Sources {
		if s.Detail != "" {
			notes = append(notes, s.Detail)
		}
	}
	if a.Coverage.Status != "" {
		if len(notes) == 0 {
			fmt.Fprintf(&b, "Coverage: %s\n", a.Coverage.Status)
		} else {
			fmt.Fprintf(&b, "Coverage: %s — %s\n", a.Coverage.Status, strings.Join(notes, "; "))
		}
	}
	fmt.Fprintf(&b, "budget_cap: %d bytes\n", a.BudgetBytes)
	return b.String()
}

// Render writes RenderText to w.
func Render(w io.Writer, a Answer) {
	_, _ = io.WriteString(w, RenderText(a))
}

func takeBasenames(paths []string, n int) []string {
	out := make([]string, 0, n)
	for _, p := range paths {
		out = append(out, path.Base(p))
		if len(out) >= n {
			break
		}
	}
	return out
}

func uniqueStable(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func trimTokens(s string, max int) string {
	fields := strings.Fields(s)
	if len(fields) <= max {
		return strings.Join(fields, " ")
	}
	return strings.Join(fields[:max], " ")
}

func shortHash(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:6])
}
