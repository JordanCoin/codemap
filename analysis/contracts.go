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
	Issues  []Issue        `json:"issues"`
}

func NormalizeCoverage(coverage Coverage) Coverage {
	coverage.Sources = slices.Clone(coverage.Sources)
	coverage.Issues = slices.Clone(coverage.Issues)
	if coverage.Sources == nil {
		coverage.Sources = []Source{}
	}
	if coverage.Issues == nil {
		coverage.Issues = []Issue{}
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
