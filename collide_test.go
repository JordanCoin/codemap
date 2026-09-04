package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"codemap/analysis"
	"codemap/scanner"
)

// collideFixturePRs reproduces the four-PR pileup from issue #134 (#124-#127),
// whose real collision matrix was measured by merging every pair in a worktree.
// Keeping the shape means a regression here is visible against ground truth.
func collideFixturePRs() []collidePR {
	rustPRFiles := []collidePRFile{
		{Path: "scanner/astgrep.go"},
		{Path: "scanner/rustgraph.go"},
		{Path: "scanner/sg-rules/rust.yml"},
	}
	return []collidePR{
		{Number: 124, Title: "rust graph edges", HeadRefName: "rust-edges", Files: []collidePRFile{
			{Path: "scanner/astgrep.go"},
			{Path: "scanner/rustgraph.go"},
			{Path: "scanner/rustcargo.go"},
		}},
		{Number: 125, Title: "rust mod resolution", HeadRefName: "rust-mods", Files: rustPRFiles},
		{Number: 126, Title: "rust workspace members", HeadRefName: "rust-workspace", Files: rustPRFiles},
		{Number: 127, Title: "rust re-export edges", HeadRefName: "rust-reexports", Files: rustPRFiles},
	}
}

// collideFixtureLookup is a stand-in for the file graph: astgrep.go is a hub,
// rustgraph.go is ordinary, and the sg-rules YAML is not a file the import
// graph carries edges for.
func collideFixtureLookup(path string) collideImporters {
	switch path {
	case "scanner/astgrep.go":
		return collideImporters{Count: 23, Known: true, InGraph: true}
	case "scanner/rustgraph.go":
		return collideImporters{Count: 4, Known: true, InGraph: true}
	case "scanner/rustcargo.go":
		return collideImporters{Count: 0, Known: true, InGraph: true}
	case "scanner/sg-rules/rust.yml":
		return collideImporters{Known: true, InGraph: false}
	default:
		return collideImporters{Known: true, InGraph: true}
	}
}

func collideCompleteCoverage() scanner.GraphCoverage {
	return scanner.GraphCoverage{Status: analysis.CoverageComplete}
}

func TestCollideSharedFilesGroupsEveryPRTouchingAPath(t *testing.T) {
	shared, hidden := collideSharedFiles(collideFixturePRs(), collideFixtureLookup, 0)
	if hidden != 0 {
		t.Fatalf("hidden = %d, want 0 with --min-importers 0", hidden)
	}

	type want struct {
		path string
		prs  []int
	}
	wants := []want{
		{path: "scanner/astgrep.go", prs: []int{124, 125, 126, 127}},
		{path: "scanner/rustgraph.go", prs: []int{124, 125, 126, 127}},
		{path: "scanner/sg-rules/rust.yml", prs: []int{125, 126, 127}},
	}
	if len(shared) != len(wants) {
		t.Fatalf("shared files = %d (%+v), want %d", len(shared), shared, len(wants))
	}
	for i, expected := range wants {
		if shared[i].Path != expected.path {
			t.Errorf("shared[%d].Path = %q, want %q", i, shared[i].Path, expected.path)
		}
		if len(shared[i].PRs) != len(expected.prs) {
			t.Fatalf("shared[%d].PRs = %v, want %v", i, shared[i].PRs, expected.prs)
		}
		for j, number := range expected.prs {
			if shared[i].PRs[j] != number {
				t.Errorf("shared[%d].PRs = %v, want %v", i, shared[i].PRs, expected.prs)
				break
			}
		}
	}

	// rustcargo.go is changed by one PR only: a file nobody else touches is not
	// a merge-order hazard, however many importers it has.
	for _, file := range shared {
		if file.Path == "scanner/rustcargo.go" {
			t.Errorf("single-PR file %q reported as shared", file.Path)
		}
	}
}

// The pair set and the 2-vs-3 distinction are the part issue #134 verified
// against six real worktree merges.
func TestCollidePairsMatchTheMeasuredMatrix(t *testing.T) {
	shared, _ := collideSharedFiles(collideFixturePRs(), collideFixtureLookup, 0)
	pairs := collidePairs(shared)

	got := make(map[string]int, len(pairs))
	for _, pair := range pairs {
		got[pairLabel(pair.A, pair.B)] = pair.SharedFileCount
	}
	want := map[string]int{
		"#124+#125": 2,
		"#124+#126": 2,
		"#124+#127": 2,
		"#125+#126": 3,
		"#125+#127": 3,
		"#126+#127": 3,
	}
	if len(got) != len(want) {
		t.Fatalf("pairs = %v, want %v", got, want)
	}
	for label, count := range want {
		if got[label] != count {
			t.Errorf("pair %s shared file count = %d, want %d", label, got[label], count)
		}
	}
}

// Ranking is importer count first, shared-file count second: a pair colliding
// on one hub outranks a pair colliding on three fixtures.
func TestCollidePairsRankByImporterCountThenSharedFiles(t *testing.T) {
	prs := []collidePR{
		{Number: 1, Files: []collidePRFile{{Path: "hub.go"}}},
		{Number: 2, Files: []collidePRFile{{Path: "hub.go"}}},
		{Number: 3, Files: []collidePRFile{{Path: "a.go"}, {Path: "b.go"}, {Path: "c.go"}}},
		{Number: 4, Files: []collidePRFile{{Path: "a.go"}, {Path: "b.go"}, {Path: "c.go"}}},
		{Number: 5, Files: []collidePRFile{{Path: "a.go"}}},
	}
	lookup := func(path string) collideImporters {
		if path == "hub.go" {
			return collideImporters{Count: 40, Known: true, InGraph: true}
		}
		return collideImporters{Count: 2, Known: true, InGraph: true}
	}

	pairs := collidePairs(mustSharedFiles(t, prs, lookup, 0))
	if len(pairs) < 2 {
		t.Fatalf("pairs = %+v, want at least 2", pairs)
	}
	if label := pairLabel(pairs[0].A, pairs[0].B); label != "#1+#2" {
		t.Errorf("top pair = %s (top file %s, %d importers), want #1+#2 on the hub",
			label, pairs[0].TopFile, pairs[0].TopImporterCount)
	}
	if pairs[0].TopFile != "hub.go" || pairs[0].TopImporterCount != 40 {
		t.Errorf("top pair top file = %s (%d importers), want hub.go (40)", pairs[0].TopFile, pairs[0].TopImporterCount)
	}
	if label := pairLabel(pairs[1].A, pairs[1].B); label != "#3+#4" {
		t.Errorf("second pair = %s, want #3+#4 (three shared files beats one)", label)
	}
}

// --min-importers is a severity filter, so it may only hide files whose
// severity is known to be below it.
func TestCollideMinImportersHidesLowCountsButNeverUnknownOnes(t *testing.T) {
	prs := []collidePR{
		{Number: 1, Files: []collidePRFile{{Path: "quiet.go"}, {Path: "busy.go"}, {Path: "opaque.swift"}}},
		{Number: 2, Files: []collidePRFile{{Path: "quiet.go"}, {Path: "busy.go"}, {Path: "opaque.swift"}}},
	}
	lookup := func(path string) collideImporters {
		switch path {
		case "busy.go":
			return collideImporters{Count: 7, Known: true, InGraph: true}
		case "quiet.go":
			return collideImporters{Count: 0, Known: true, InGraph: true}
		default:
			return collideImporters{Known: false, InGraph: true}
		}
	}

	shared, hidden := collideSharedFiles(prs, lookup, 1)
	if hidden != 1 {
		t.Errorf("hidden = %d, want 1 (quiet.go is measured below the threshold)", hidden)
	}
	paths := make([]string, 0, len(shared))
	for _, file := range shared {
		paths = append(paths, file.Path)
	}
	if strings.Join(paths, ",") != "busy.go,opaque.swift" {
		t.Errorf("shared paths = %v, want busy.go and opaque.swift (an unknown count is not below a threshold)", paths)
	}
}

// Degraded inputs must not be laundered into a confident answer.
func TestCollideDegradedCoverageYieldsUnknownCountsAndTrustLow(t *testing.T) {
	prs := []collidePR{
		{Number: 10, Files: []collidePRFile{{Path: "App/Model.swift"}}},
		{Number: 11, Files: []collidePRFile{{Path: "App/Model.swift"}}},
	}
	coverage := scanner.GraphCoverage{
		Status: analysis.CoveragePartial,
		Notes:  []string{"Swift: imports name modules, not files"},
	}
	lookup := func(string) collideImporters {
		return collideImporters{Count: 0, Known: collideImportersKnown(coverage), InGraph: true}
	}

	report := buildCollideReport(prs, lookup, coverage, "", 1)
	if report.Trust != collideTrustLow {
		t.Errorf("trust = %q, want %q", report.Trust, collideTrustLow)
	}
	if len(report.SharedFiles) != 1 {
		t.Fatalf("shared files = %+v, want the Swift file kept despite --min-importers 1", report.SharedFiles)
	}
	if report.SharedFiles[0].ImportersKnown {
		t.Error("shared file reports a known importer count from partial coverage")
	}
	if len(report.Pairs) != 1 || report.Pairs[0].TopImportersKnown {
		t.Errorf("pairs = %+v, want one pair with an unknown top count", report.Pairs)
	}

	var buf bytes.Buffer
	renderCollideReport(&buf, report)
	output := buf.String()
	if !strings.Contains(output, "unknown importers") {
		t.Errorf("human output states a count it cannot know:\n%s", output)
	}
	if !strings.Contains(output, "TRUST LOW") {
		t.Errorf("human output missing TRUST LOW:\n%s", output)
	}
	if !strings.Contains(output, "Swift: imports name modules, not files") {
		t.Errorf("human output does not say why trust is low:\n%s", output)
	}
	if strings.Contains(output, "0 importers") {
		t.Errorf("human output printed a fabricated zero:\n%s", output)
	}
}

// A "no collisions" answer from a partial graph is exactly as unreliable as a
// positive one, so it carries the same verdict.
func TestCollideNoSharedFilesStillReportsCoverage(t *testing.T) {
	prs := []collidePR{
		{Number: 1, Files: []collidePRFile{{Path: "a.go"}}},
		{Number: 2, Files: []collidePRFile{{Path: "b.go"}}},
	}
	lookup := func(string) collideImporters { return collideImporters{Known: false, InGraph: true} }

	report := buildCollideReport(prs, lookup, scanner.GraphCoverage{Status: analysis.CoverageUnavailable}, "", 1)
	if report.Trust != collideTrustLow {
		t.Errorf("trust = %q, want %q for an empty answer from an unavailable graph", report.Trust, collideTrustLow)
	}
	var buf bytes.Buffer
	renderCollideReport(&buf, report)
	if !strings.Contains(buf.String(), "No shared files between open PRs.") {
		t.Errorf("missing empty-result line:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "TRUST LOW") {
		t.Errorf("empty result skipped its trust verdict:\n%s", buf.String())
	}
}

// An empty graph status is complete knowledge, but a consumer cannot read an
// empty string, so it is spelled out.
func TestCollideEmptyCoverageStatusRendersComplete(t *testing.T) {
	if got := collideCoverageStatus(scanner.GraphCoverage{}); got != string(analysis.CoverageComplete) {
		t.Errorf("collideCoverageStatus(zero) = %q, want %q", got, analysis.CoverageComplete)
	}
	if !collideImportersKnown(scanner.GraphCoverage{}) {
		t.Error("zero coverage treated as degraded; it means nothing was reported against the graph")
	}
	for _, status := range []analysis.CoverageStatus{analysis.CoveragePartial, analysis.CoverageUnavailable} {
		if collideImportersKnown(scanner.GraphCoverage{Status: status}) {
			t.Errorf("coverage %q treated as knowable", status)
		}
	}
}

func TestCollideHumanOutputGolden(t *testing.T) {
	report := buildCollideReport(collideFixturePRs(), collideFixtureLookup, collideCompleteCoverage(), "JordanCoin/codemap", 0)

	want := `OPEN PRs (4): #124, #125, #126, #127

SHARED FILES (each = a merge-order hazard):
  4 PRs   scanner/astgrep.go         23 importers      <- #124, #125, #126, #127
  4 PRs   scanner/rustgraph.go       4 importers       <- #124, #125, #126, #127
  3 PRs   scanner/sg-rules/rust.yml  not in graph      <- #125, #126, #127

PREDICTED COLLIDING PAIRS:
  #125 + #126  ->  3 shared file(s)  top: scanner/astgrep.go (23 importers)
  #125 + #127  ->  3 shared file(s)  top: scanner/astgrep.go (23 importers)
  #126 + #127  ->  3 shared file(s)  top: scanner/astgrep.go (23 importers)
  #124 + #125  ->  2 shared file(s)  top: scanner/astgrep.go (23 importers)
  #124 + #126  ->  2 shared file(s)  top: scanner/astgrep.go (23 importers)
  #124 + #127  ->  2 shared file(s)  top: scanner/astgrep.go (23 importers)

Graph coverage: go complete (2 files), 1 file not tracked by the import graph. TRUST HIGH.
`

	var buf bytes.Buffer
	renderCollideReport(&buf, report)
	if got := buf.String(); got != want {
		t.Errorf("human output mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestCollideHiddenFilesAreAnnouncedNotSilent(t *testing.T) {
	prs := []collidePR{
		{Number: 1, Files: []collidePRFile{{Path: "quiet.go"}}},
		{Number: 2, Files: []collidePRFile{{Path: "quiet.go"}}},
	}
	lookup := func(string) collideImporters { return collideImporters{Count: 0, Known: true, InGraph: true} }

	report := buildCollideReport(prs, lookup, collideCompleteCoverage(), "", 1)
	if report.HiddenByMinImporters != 1 {
		t.Fatalf("hidden = %d, want 1", report.HiddenByMinImporters)
	}
	var buf bytes.Buffer
	renderCollideReport(&buf, report)
	output := buf.String()
	if !strings.Contains(output, "hidden by --min-importers 1") {
		t.Errorf("a hazard was dropped without saying so:\n%s", output)
	}
	// "No shared files between open PRs" would be a false negative here: the
	// PRs do share a file, it was filtered.
	if strings.Contains(output, "No shared files between open PRs.") {
		t.Errorf("filtered result claims the PRs share nothing:\n%s", output)
	}
	if !strings.Contains(output, "No shared files at or above --min-importers 1.") {
		t.Errorf("filtered empty result does not say the threshold caused it:\n%s", output)
	}
}

func TestCollideGHErrorIsOneActionableLine(t *testing.T) {
	cases := []struct {
		name   string
		stderr string
		want   string
	}{
		{
			name:   "unauthenticated",
			stderr: "gh: To use GitHub CLI in a GitHub Actions workflow, set the GH_TOKEN environment variable.\nmore noise",
			want:   "gh is not authenticated. Run: gh auth login",
		},
		{
			name:   "other failure",
			stderr: "GraphQL: Could not resolve to a Repository\nsecond line",
			want:   "gh pr list failed: GraphQL: Could not resolve to a Repository",
		},
		{
			name:   "silent failure",
			stderr: "",
			want:   "gh pr list failed: exit status 1",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := collideGHError(testCase.stderr, errors.New("exit status 1"))
			if got.Error() != testCase.want {
				t.Errorf("collideGHError() = %q, want %q", got, testCase.want)
			}
			if strings.Contains(got.Error(), "\n") {
				t.Errorf("error spans multiple lines: %q", got)
			}
		})
	}
}

func mustSharedFiles(t *testing.T, prs []collidePR, lookup collideLookup, minImporters int) []collideSharedFile {
	t.Helper()
	shared, _ := collideSharedFiles(prs, lookup, minImporters)
	return shared
}

func pairLabel(a, b int) string {
	return fmt.Sprintf("#%d+#%d", a, b)
}
