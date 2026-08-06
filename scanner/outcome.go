package scanner

import (
	"fmt"
	"slices"

	"codemap/analysis"
)

// Deprecated scanner spellings are aliases, not a second provenance contract.
// The analysis package owns the shared vocabulary; scanner consumes it.
type ScanSourceStatus = analysis.SourceStatus
type ScanSourceOutcome = analysis.Source

const (
	ScanSourceAuthoritative = analysis.SourceAuthoritative
	ScanSourceMixed         = analysis.SourceMixed
	ScanSourceFallback      = analysis.SourceFallback
	ScanSourceTimeout       = analysis.SourceTimeout
	ScanSourceUnavailable   = analysis.SourceUnavailable
	ScanSourceFailed        = analysis.SourceFailed
)

// ScanOutcome contains dependency analyses and their provenance.
type ScanOutcome struct {
	Analyses []FileAnalysis    `json:"analyses"`
	Sources  []analysis.Source `json:"sources,omitempty"`
}

// GraphCoverage describes graph blind spots and scanner provenance.
type GraphCoverage struct {
	Status  analysis.CoverageStatus `json:"status,omitempty"`
	Notes   []string                `json:"notes,omitempty"`
	Sources []analysis.Source       `json:"sources,omitempty"`
}

// AddSource records one scanner outcome and marks degraded results partial.
// Only usable sources (authoritative, fallback, mixed) keep coverage usable: a
// timeout/failed/unavailable source (which extracted no dependency references)
// leaves the graph unavailable unless a usable source was also recorded, and an
// authoritative source can repair an earlier degraded-only unavailable reading.
func (c *GraphCoverage) AddSource(outcome ScanSourceOutcome) {
	if c == nil || outcome.Name == "" {
		return
	}
	c.Sources = append(c.Sources, outcome)
	switch outcome.Status {
	case ScanSourceAuthoritative:
		// Authoritative alone keeps the default complete coverage. It cannot
		// make coverage unavailable, so a degraded-only reading observed
		// earlier must at least become partial once a usable source exists.
		if c.Status == analysis.CoverageUnavailable {
			c.Status = analysis.CoveragePartial
		}
	case ScanSourceFallback, ScanSourceMixed:
		c.Status = analysis.CoveragePartial
	default:
		// timeout/failed/unavailable extracted no dependency references; the
		// graph is only unavailable while no usable source has been recorded.
		if hasUsableSource(c.Sources) {
			c.Status = analysis.CoveragePartial
		} else {
			c.Status = analysis.CoverageUnavailable
		}
	}
	// Record the detail once: distinct sources may legitimately carry the same
	// caveat (e.g. a shared Rust resolution note), and consumers surface every
	// note, so a duplicate would read as a doubled warning in the output.
	if outcome.Detail != "" && !slices.Contains(c.Notes, outcome.Detail) {
		c.Notes = append(c.Notes, outcome.Detail)
	}
}

// hasUsableSource reports whether any recorded source extracted dependency
// references (authoritative, fallback, or mixed).
func hasUsableSource(sources []analysis.Source) bool {
	for _, source := range sources {
		switch source.Status {
		case analysis.SourceAuthoritative, analysis.SourceFallback, analysis.SourceMixed:
			return true
		}
	}
	return false
}

// CoverageFromSources derives honest coverage from source capability evidence.
// A failed-only scan is unavailable; a usable fallback is partial; only an
// all-authoritative set is complete.
func CoverageFromSources(sources []analysis.Source) analysis.Coverage {
	coverage := analysis.Coverage{Sources: append([]analysis.Source(nil), sources...), Issues: []analysis.Issue{}}
	if len(sources) == 0 {
		coverage.Status = analysis.CoverageUnavailable
		return analysis.NormalizeCoverage(coverage)
	}
	allAuthoritative := true
	usable := false
	for _, source := range sources {
		switch source.Status {
		case analysis.SourceAuthoritative:
			usable = true
		case analysis.SourceFallback, analysis.SourceMixed:
			usable = true
			allAuthoritative = false
		default:
			allAuthoritative = false
		}
	}
	switch {
	case allAuthoritative:
		coverage.Status = analysis.CoverageComplete
	case usable:
		coverage.Status = analysis.CoveragePartial
	default:
		coverage.Status = analysis.CoverageUnavailable
	}
	return analysis.NormalizeCoverage(coverage)
}

// IncompleteScanError prevents incomplete scanner output from looking authoritative.
type IncompleteScanError struct {
	Outcome ScanSourceOutcome
	Err     error
}

func newIncompleteScanError(source string, status ScanSourceStatus, detail string, err error) error {
	return &IncompleteScanError{
		Outcome: analysis.Source{Name: source, Status: status, Detail: detail},
		Err:     err,
	}
}

func (e *IncompleteScanError) Error() string {
	if e == nil {
		return "incomplete dependency scan"
	}
	if e.Outcome.Detail != "" {
		return e.Outcome.Detail
	}
	return fmt.Sprintf("%s dependency scan is %s", e.Outcome.Name, e.Outcome.Status)
}

func (e *IncompleteScanError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}
