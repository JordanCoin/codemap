package scanner

import (
	"fmt"

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
// Only usable fallback/mixed sources promote coverage to partial; a
// timeout/failed/unavailable source (which extracted no dependency references)
// keeps coverage unavailable unless a usable source was already recorded.
func (c *GraphCoverage) AddSource(outcome ScanSourceOutcome) {
	if c == nil || outcome.Name == "" {
		return
	}
	c.Sources = append(c.Sources, outcome)
	if outcome.Status == ScanSourceAuthoritative {
		return
	}
	if outcome.Status == ScanSourceFallback || outcome.Status == ScanSourceMixed {
		c.Status = analysis.CoveragePartial
	} else if c.Status == "" {
		c.Status = analysis.CoverageUnavailable
	}
	if outcome.Detail != "" {
		c.Notes = append(c.Notes, outcome.Detail)
	}
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
