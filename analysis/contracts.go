package analysis

import (
	"slices"
	"sort"
)

const SchemaVersion = "codemap.analysis/v1"

type CoverageStatus string

const (
	CoverageComplete    CoverageStatus = "complete"
	CoveragePartial     CoverageStatus = "partial"
	CoverageUnavailable CoverageStatus = "unavailable"
)

type SourceStatus string

const (
	SourceAuthoritative SourceStatus = "authoritative"
	SourceMixed         SourceStatus = "mixed"
	SourceFallback      SourceStatus = "fallback"
	SourceTimeout       SourceStatus = "timeout"
	SourceUnavailable   SourceStatus = "unavailable"
	SourceFailed        SourceStatus = "failed"
)

type Source struct {
	Name   string       `json:"name"`
	Status SourceStatus `json:"status"`
	Detail string       `json:"detail,omitempty"`
	// Languages names the scanner languages whose edges this source can
	// affect. Per-file answers keep only the sources tagged with the file's
	// language; an untagged source applies to every file (#111).
	Languages []string `json:"languages,omitempty"`
}

type Issue struct {
	Code       string   `json:"code"`
	Severity   string   `json:"severity,omitempty"`
	Path       string   `json:"path,omitempty"`
	Line       int      `json:"line,omitempty"`
	Message    string   `json:"message"`
	Candidates []string `json:"candidates,omitempty"`
}

type Coverage struct {
	Status  CoverageStatus `json:"status"`
	Sources []Source       `json:"sources"`
	// Issues lists references the scanner tried and failed to resolve. It is
	// omitted when empty: an empty list reads as "nothing was dropped", which
	// is a finding this field cannot make (#137).
	Issues []Issue `json:"issues,omitempty"`
}

func NormalizeCoverage(coverage Coverage) Coverage {
	coverage.Sources = slices.Clone(coverage.Sources)
	coverage.Issues = slices.Clone(coverage.Issues)
	if coverage.Sources == nil {
		coverage.Sources = []Source{}
	}
	for index := range coverage.Sources {
		coverage.Sources[index].Languages = slices.Clone(coverage.Sources[index].Languages)
	}
	sort.Slice(coverage.Sources, func(i, j int) bool {
		left, right := coverage.Sources[i], coverage.Sources[j]
		if left.Name != right.Name {
			return left.Name < right.Name
		}
		if left.Status != right.Status {
			return left.Status < right.Status
		}
		return left.Detail < right.Detail
	})
	for index := range coverage.Issues {
		coverage.Issues[index].Candidates = slices.Clone(coverage.Issues[index].Candidates)
		if coverage.Issues[index].Candidates == nil {
			coverage.Issues[index].Candidates = []string{}
		}
		sort.Strings(coverage.Issues[index].Candidates)
	}
	return coverage
}
