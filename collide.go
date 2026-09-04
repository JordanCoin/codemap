package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"codemap/analysis"
	"codemap/cmd"
	"codemap/config"
	"codemap/scanner"
)

// collideSchema versions the machine-readable output so a hosted consumer can
// detect a shape change instead of guessing.
const collideSchema = "codemap.collide/v1"

const (
	collideTrustHigh = "high"
	collideTrustLow  = "low"
)

// collideUnknownWeight is the ranking weight of a file whose importer count the
// graph cannot state. It sorts below a measured zero on purpose: a pair whose
// severity is unknown must never outrank one whose severity was measured.
const collideUnknownWeight = -1

// collidePRFile is one changed path from `gh pr list --json files`.
type collidePRFile struct {
	Path string `json:"path"`
}

// collidePR is the slice of a pull request this command reads. Field names
// match `gh pr list --json number,title,headRefName,files` exactly.
type collidePR struct {
	Number      int             `json:"number"`
	Title       string          `json:"title"`
	HeadRefName string          `json:"headRefName"`
	Files       []collidePRFile `json:"files"`
}

// collideImporters is everything the graph knows about one shared file.
//
// Known and InGraph are different kinds of ignorance and are kept apart: a file
// the graph does not track (a YAML rule file, a fixture) has a truthful answer
// of "no import edges exist for this kind of file", while a file the graph does
// track under degraded coverage has no truthful answer at all.
type collideImporters struct {
	Count   int
	Known   bool
	InGraph bool
}

// collideLookup answers importer questions for a repository-relative path.
type collideLookup func(path string) collideImporters

// collideSharedFile is one path changed by more than one open PR.
type collideSharedFile struct {
	Path           string `json:"path"`
	PRs            []int  `json:"prs"`
	Language       string `json:"language,omitempty"`
	ImporterCount  int    `json:"importer_count"`
	ImportersKnown bool   `json:"importers_known"`
	InGraph        bool   `json:"in_graph"`
}

// weight is the ranking value of this file's severity.
func (f collideSharedFile) weight() int {
	if !f.ImportersKnown {
		return collideUnknownWeight
	}
	return f.ImporterCount
}

// collidePair is two open PRs that change at least one file in common.
type collidePair struct {
	A                 int      `json:"a"`
	B                 int      `json:"b"`
	SharedFileCount   int      `json:"shared_file_count"`
	SharedFiles       []string `json:"shared_files"`
	TopFile           string   `json:"top_file"`
	TopImporterCount  int      `json:"top_importer_count"`
	TopImportersKnown bool     `json:"top_importers_known"`
	TopFileInGraph    bool     `json:"top_file_in_graph"`
}

// collideLanguageCoverage reports graph coverage for one language present in
// the shared set, which is the only slice of the repository this verdict rests
// on.
type collideLanguageCoverage struct {
	Language string `json:"language"`
	Status   string `json:"status"`
	Files    int    `json:"files"`
}

// collideCoverage carries the graph provenance behind every importer count.
type collideCoverage struct {
	Status    string                    `json:"status"`
	Notes     []string                  `json:"notes,omitempty"`
	Languages []collideLanguageCoverage `json:"languages"`
	Untracked int                       `json:"untracked_files"`
}

// collideReportPR labels a PR number in the output.
type collideReportPR struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Head   string `json:"head_ref_name,omitempty"`
}

// collideReport is the whole answer, human rendering and JSON alike.
type collideReport struct {
	Schema               string              `json:"schema"`
	Repo                 string              `json:"repo,omitempty"`
	MinImporters         int                 `json:"min_importers"`
	Trust                string              `json:"trust"`
	Coverage             collideCoverage     `json:"coverage"`
	PRs                  []collideReportPR   `json:"prs"`
	SharedFiles          []collideSharedFile `json:"shared_files"`
	Pairs                []collidePair       `json:"pairs"`
	HiddenByMinImporters int                 `json:"hidden_by_min_importers"`
}

func runCollideSubcommand(args []string, launchDir string) int {
	fs := flag.NewFlagSet("collide", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	repo := fs.String("repo", "", "Repository to read open PRs from (owner/name); defaults to the current checkout")
	jsonMode := fs.Bool("json", false, "Emit a single JSON object")
	minImporters := fs.Int("min-importers", 1, "Hide shared files with fewer importers than this")
	limit := fs.Int("limit", 50, "Maximum open PRs to read")
	var help bool
	fs.BoolVar(&help, "help", false, "Show collide help")
	fs.BoolVar(&help, "h", false, "Show collide help")
	fs.Usage = func() { printCollideUsage(fs) }

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if help {
		fs.Usage()
		return 0
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "Error: unexpected argument %q; codemap collide reads the current checkout (use -C <repo> to change it)\n", fs.Arg(0))
		return 2
	}
	if *minImporters < 0 {
		fmt.Fprintln(os.Stderr, "Error: --min-importers cannot be negative")
		return 2
	}
	if *limit <= 0 {
		fmt.Fprintln(os.Stderr, "Error: --limit must be greater than zero")
		return 2
	}

	absRoot, err := filepath.Abs(launchDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving project root: %v\n", err)
		return 1
	}
	resolvedRoot, _, err := cmd.ResolveNearestGitRoot(absRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving project root: %v\n", err)
		return 1
	}
	if _, err := cmd.ValidateProjectPath(resolvedRoot); err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving project root: %v\n", err)
		return 1
	}

	prs, err := collideFetchPRs(*repo, *limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	cfg := config.Load(resolvedRoot)
	filters := scanner.Filters{Only: cfg.Only, Exclude: cfg.Exclude}
	fg, err := scanner.BuildFileGraph(context.Background(), resolvedRoot, filters)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error building file graph: %v\n", err)
		return 1
	}

	report := buildCollideReport(prs, collideGraphLookup(resolvedRoot, fg), fg.Coverage, *repo, *minImporters)

	if *jsonMode {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
			return 1
		}
		return 0
	}
	renderCollideReport(os.Stdout, report)
	return 0
}

func printCollideUsage(fs *flag.FlagSet) {
	fmt.Fprintln(os.Stderr, "Usage: codemap collide [options]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Rank open pull requests by the merge-order hazard they share: which pairs")
	fmt.Fprintln(os.Stderr, "change the same files, weighted by how many files import them. CI cannot see")
	fmt.Fprintln(os.Stderr, "this, because every PR is built against main and never against its siblings.")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Options:")
	fs.PrintDefaults()
}

// collideImportersKnown decides, once for the whole run, whether importer
// counts from this graph may be stated as fact.
//
// The rule is deliberately whole-graph rather than per-language. Coverage notes
// are free text, so narrowing "this graph is partial" down to a subset of
// languages by matching those strings would hand back confidence the graph
// never claimed — the exact failure the composite is supposed to refuse. When
// per-language attribution becomes available from the scanner (PR #174 adds
// scanner.ResolvesFileLevelImports), it belongs in this function and nowhere
// else.
func collideImportersKnown(coverage scanner.GraphCoverage) bool {
	return coverage.Status == "" || coverage.Status == analysis.CoverageComplete
}

// collideCoverageStatus renders the graph's status, spelling out the zero value
// rather than emitting an empty string a reader cannot interpret.
func collideCoverageStatus(coverage scanner.GraphCoverage) string {
	if coverage.Status == "" {
		return string(analysis.CoverageComplete)
	}
	return string(coverage.Status)
}

// collideGraphLookup answers importer questions from a built file graph,
// reusing the same helper that backs `codemap --importers` and blast-radius so
// all three report the same number for the same file.
func collideGraphLookup(root string, fg *scanner.FileGraph) collideLookup {
	known := collideImportersKnown(fg.Coverage)
	return func(path string) collideImporters {
		if !scanner.IsSourceExt(filepath.Ext(path)) {
			// Not a dishonest zero: the import graph has no edges for this kind
			// of file at all, which is a different statement from "nothing
			// imports it".
			return collideImporters{Known: true, InGraph: false}
		}
		report, err := buildImportersReportFromGraph(root, path, fg)
		if err != nil {
			return collideImporters{Known: false, InGraph: true}
		}
		return collideImporters{Count: report.ImporterCount, Known: known, InGraph: true}
	}
}

// buildCollideReport is the whole computation, kept free of gh and the scanner
// so it can be tested against fixtures.
func buildCollideReport(prs []collidePR, lookup collideLookup, coverage scanner.GraphCoverage, repo string, minImporters int) collideReport {
	shared, hidden := collideSharedFiles(prs, lookup, minImporters)
	report := collideReport{
		Schema:               collideSchema,
		Repo:                 repo,
		MinImporters:         minImporters,
		PRs:                  collideReportPRs(prs),
		SharedFiles:          shared,
		Pairs:                collidePairs(shared),
		HiddenByMinImporters: hidden,
		Coverage:             collideCoverageFor(shared, coverage),
	}
	report.Trust = collideTrust(shared, coverage)
	if report.SharedFiles == nil {
		report.SharedFiles = []collideSharedFile{}
	}
	if report.Pairs == nil {
		report.Pairs = []collidePair{}
	}
	return report
}

func collideReportPRs(prs []collidePR) []collideReportPR {
	out := make([]collideReportPR, 0, len(prs))
	for _, pr := range prs {
		out = append(out, collideReportPR{Number: pr.Number, Title: pr.Title, Head: pr.HeadRefName})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out
}

// collideSharedFiles groups every path changed by more than one open PR and
// attaches what the graph knows about it.
//
// A file whose importer count is unknown is never hidden by --min-importers:
// the threshold is a severity filter, and a file with no known severity has not
// been shown to be below it.
func collideSharedFiles(prs []collidePR, lookup collideLookup, minImporters int) (shared []collideSharedFile, hidden int) {
	byPath := make(map[string]map[int]bool)
	for _, pr := range prs {
		for _, file := range pr.Files {
			path := strings.TrimSpace(filepath.ToSlash(file.Path))
			if path == "" {
				continue
			}
			if byPath[path] == nil {
				byPath[path] = make(map[int]bool)
			}
			byPath[path][pr.Number] = true
		}
	}

	for path, numbers := range byPath {
		if len(numbers) < 2 {
			continue
		}
		prNumbers := make([]int, 0, len(numbers))
		for number := range numbers {
			prNumbers = append(prNumbers, number)
		}
		sort.Ints(prNumbers)

		importers := lookup(path)
		if importers.Known && importers.Count < minImporters {
			hidden++
			continue
		}
		shared = append(shared, collideSharedFile{
			Path:           path,
			PRs:            prNumbers,
			Language:       scanner.DetectLanguage(path),
			ImporterCount:  importers.Count,
			ImportersKnown: importers.Known,
			InGraph:        importers.InGraph,
		})
	}

	sort.Slice(shared, func(i, j int) bool {
		left, right := shared[i], shared[j]
		if len(left.PRs) != len(right.PRs) {
			return len(left.PRs) > len(right.PRs)
		}
		if left.weight() != right.weight() {
			return left.weight() > right.weight()
		}
		return left.Path < right.Path
	})
	return shared, hidden
}

// collidePairs ranks every pair of PRs that share a file by the worst importer
// count among the files they share, then by how many files they share.
//
// When no shared file has a known importer count every pair weighs the same, so
// the ranking degrades to the file count rather than inventing an order the
// graph cannot support.
func collidePairs(shared []collideSharedFile) []collidePair {
	type pairKey struct{ a, b int }
	pairs := make(map[pairKey]*collidePair)

	for _, file := range shared {
		for i := 0; i < len(file.PRs); i++ {
			for j := i + 1; j < len(file.PRs); j++ {
				key := pairKey{a: file.PRs[i], b: file.PRs[j]}
				pair, ok := pairs[key]
				if !ok {
					pair = &collidePair{A: key.a, B: key.b, TopImporterCount: collideUnknownWeight}
					pairs[key] = pair
				}
				pair.SharedFiles = append(pair.SharedFiles, file.Path)
				// shared is already ordered worst-first, so the first file to
				// land on a pair is its top file.
				if pair.TopFile == "" {
					pair.TopFile = file.Path
					pair.TopImporterCount = file.ImporterCount
					pair.TopImportersKnown = file.ImportersKnown
					pair.TopFileInGraph = file.InGraph
				}
			}
		}
	}

	out := make([]collidePair, 0, len(pairs))
	for _, pair := range pairs {
		pair.SharedFileCount = len(pair.SharedFiles)
		out = append(out, *pair)
	}

	weight := func(pair collidePair) int {
		if !pair.TopImportersKnown {
			return collideUnknownWeight
		}
		return pair.TopImporterCount
	}
	sort.Slice(out, func(i, j int) bool {
		left, right := out[i], out[j]
		if weight(left) != weight(right) {
			return weight(left) > weight(right)
		}
		if left.SharedFileCount != right.SharedFileCount {
			return left.SharedFileCount > right.SharedFileCount
		}
		if left.A != right.A {
			return left.A < right.A
		}
		return left.B < right.B
	})
	return out
}

// collideCoverageFor reports coverage for exactly the languages the verdict
// rests on: the ones present in the shared set.
func collideCoverageFor(shared []collideSharedFile, coverage scanner.GraphCoverage) collideCoverage {
	status := collideCoverageStatus(coverage)
	files := make(map[string]int)
	degraded := make(map[string]bool)
	untracked := 0

	for _, file := range shared {
		if !file.InGraph {
			untracked++
			continue
		}
		language := file.Language
		if language == "" {
			language = "unknown"
		}
		files[language]++
		if !file.ImportersKnown {
			degraded[language] = true
		}
	}

	languages := make([]collideLanguageCoverage, 0, len(files))
	for language, count := range files {
		languageStatus := string(analysis.CoverageComplete)
		if degraded[language] {
			languageStatus = status
		}
		languages = append(languages, collideLanguageCoverage{Language: language, Status: languageStatus, Files: count})
	}
	sort.Slice(languages, func(i, j int) bool { return languages[i].Language < languages[j].Language })

	return collideCoverage{
		Status:    status,
		Notes:     append([]string(nil), coverage.Notes...),
		Languages: languages,
		Untracked: untracked,
	}
}

// collideTrust refuses a confident verdict whenever an input reports degraded
// coverage. That includes the "no collisions" answer: a negative finding from a
// partial graph is exactly as unreliable as a positive one.
func collideTrust(shared []collideSharedFile, coverage scanner.GraphCoverage) string {
	if !collideImportersKnown(coverage) {
		return collideTrustLow
	}
	for _, file := range shared {
		if !file.ImportersKnown {
			return collideTrustLow
		}
	}
	return collideTrustHigh
}

// collideImportersText states an importer count the way it is actually known.
func collideImportersText(known, inGraph bool, count int) string {
	if !known {
		return "unknown importers"
	}
	if !inGraph {
		return "not in graph"
	}
	if count == 1 {
		return "1 importer"
	}
	return fmt.Sprintf("%d importers", count)
}

func renderCollideReport(w io.Writer, report collideReport) {
	numbers := make([]string, 0, len(report.PRs))
	for _, pr := range report.PRs {
		numbers = append(numbers, fmt.Sprintf("#%d", pr.Number))
	}
	if len(numbers) == 0 {
		fmt.Fprintln(w, "OPEN PRs (0): none")
	} else {
		fmt.Fprintf(w, "OPEN PRs (%d): %s\n", len(numbers), strings.Join(numbers, ", "))
	}
	fmt.Fprintln(w)

	if len(report.SharedFiles) == 0 {
		// "None" and "none left after filtering" are different answers, and
		// only one of them means the open PRs are independent.
		if report.HiddenByMinImporters > 0 {
			fmt.Fprintf(w, "No shared files at or above --min-importers %d.\n", report.MinImporters)
		} else {
			fmt.Fprintln(w, "No shared files between open PRs.")
		}
	} else {
		width := 0
		for _, file := range report.SharedFiles {
			if len(file.Path) > width {
				width = len(file.Path)
			}
		}
		fmt.Fprintln(w, "SHARED FILES (each = a merge-order hazard):")
		for _, file := range report.SharedFiles {
			owners := make([]string, 0, len(file.PRs))
			for _, number := range file.PRs {
				owners = append(owners, fmt.Sprintf("#%d", number))
			}
			fmt.Fprintf(w, "  %d PRs   %-*s  %-17s <- %s\n",
				len(file.PRs), width, file.Path,
				collideImportersText(file.ImportersKnown, file.InGraph, file.ImporterCount),
				strings.Join(owners, ", "))
		}
	}
	if report.HiddenByMinImporters > 0 {
		fmt.Fprintf(w, "  (%d shared file(s) hidden by --min-importers %d; rerun with --min-importers 0 to see them)\n",
			report.HiddenByMinImporters, report.MinImporters)
	}

	if len(report.Pairs) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "PREDICTED COLLIDING PAIRS:")
		for _, pair := range report.Pairs {
			fmt.Fprintf(w, "  #%d + #%d  ->  %d shared file(s)  top: %s (%s)\n",
				pair.A, pair.B, pair.SharedFileCount, pair.TopFile,
				collideImportersText(pair.TopImportersKnown, pair.TopFileInGraph, pair.TopImporterCount))
		}
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, renderCollideCoverageLine(report))
}

// renderCollideCoverageLine is the last line of the human output: what the
// graph knows about the languages in the shared set, and the trust that follows
// from it.
func renderCollideCoverageLine(report collideReport) string {
	var parts []string
	for _, language := range report.Coverage.Languages {
		noun := "files"
		if language.Files == 1 {
			noun = "file"
		}
		parts = append(parts, fmt.Sprintf("%s %s (%d %s)", language.Language, language.Status, language.Files, noun))
	}
	if report.Coverage.Untracked > 0 {
		noun := "files"
		if report.Coverage.Untracked == 1 {
			noun = "file"
		}
		parts = append(parts, fmt.Sprintf("%d %s not tracked by the import graph", report.Coverage.Untracked, noun))
	}
	if len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("graph %s, no shared files to weight", report.Coverage.Status))
	}

	line := fmt.Sprintf("Graph coverage: %s. TRUST %s.", strings.Join(parts, ", "), strings.ToUpper(report.Trust))
	if report.Trust == collideTrustLow && len(report.Coverage.Notes) > 0 {
		line += "\n  Why: " + strings.Join(report.Coverage.Notes, "; ")
	}
	if report.Trust == collideTrustLow {
		line += "\n  Ranking is by shared-file count only; importer weighting needs complete coverage."
	}
	return line
}

// collideFetchPRs shells out to gh, the only network this command uses.
func collideFetchPRs(repo string, limit int) ([]collidePR, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, errors.New("gh not found: install GitHub CLI from https://cli.github.com, then run: gh auth login")
	}

	args := []string{"pr", "list", "--state", "open", "--json", "number,title,headRefName,files", "--limit", fmt.Sprintf("%d", limit)}
	if strings.TrimSpace(repo) != "" {
		args = append(args, "--repo", repo)
	}

	command := exec.Command("gh", args...)
	var stderr strings.Builder
	command.Stderr = &stderr
	stdout, err := command.Output()
	if err != nil {
		return nil, collideGHError(stderr.String(), err)
	}

	var prs []collidePR
	if err := json.Unmarshal(stdout, &prs); err != nil {
		return nil, fmt.Errorf("parse gh pr list output: %w", err)
	}
	return prs, nil
}

// collideGHError turns a gh failure into one actionable line.
func collideGHError(stderr string, err error) error {
	message := strings.TrimSpace(stderr)
	if index := strings.IndexByte(message, '\n'); index >= 0 {
		message = strings.TrimSpace(message[:index])
	}
	lowered := strings.ToLower(message)
	switch {
	case strings.Contains(lowered, "auth") || strings.Contains(lowered, "logged in") || strings.Contains(lowered, "token"):
		return errors.New("gh is not authenticated. Run: gh auth login")
	case message == "":
		return fmt.Errorf("gh pr list failed: %w", err)
	default:
		return fmt.Errorf("gh pr list failed: %s", message)
	}
}
