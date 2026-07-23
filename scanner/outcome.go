package scanner

import "fmt"

// ScanSourceStatus describes the trust level of one dependency-analysis source.
type ScanSourceStatus string

const (
	ScanSourceAuthoritative ScanSourceStatus = "authoritative"
	ScanSourceMixed         ScanSourceStatus = "mixed"
	ScanSourceFallback      ScanSourceStatus = "fallback"
	ScanSourceTimeout       ScanSourceStatus = "timeout"
	ScanSourceUnavailable   ScanSourceStatus = "unavailable"
	ScanSourceFailed        ScanSourceStatus = "failed"
)

// ScanSourceOutcome records how one scanner contributed to an analysis.
type ScanSourceOutcome struct {
	Source string           `json:"source"`
	Status ScanSourceStatus `json:"status"`
	Detail string           `json:"detail,omitempty"`
}

// ScanOutcome contains dependency analyses and their provenance.
type ScanOutcome struct {
	Analyses []FileAnalysis      `json:"analyses"`
	Sources  []ScanSourceOutcome `json:"sources,omitempty"`
}

// GraphCoverage describes graph blind spots and scanner provenance.
type GraphCoverage struct {
	Status  string              `json:"status,omitempty"`
	Notes   []string            `json:"notes,omitempty"`
	Sources []ScanSourceOutcome `json:"sources,omitempty"`
}

// AddSource records one scanner outcome and marks degraded results partial.
func (c *GraphCoverage) AddSource(outcome ScanSourceOutcome) {
	if c == nil || outcome.Source == "" {
		return
	}
	c.Sources = append(c.Sources, outcome)
	if outcome.Status == ScanSourceAuthoritative {
		return
	}
	c.Status = "partial"
	if outcome.Detail != "" {
		c.Notes = append(c.Notes, outcome.Detail)
	}
}

// IncompleteScanError prevents incomplete scanner output from looking authoritative.
type IncompleteScanError struct {
	Outcome ScanSourceOutcome
	Err     error
}

func newIncompleteScanError(source string, status ScanSourceStatus, detail string, err error) error {
	return &IncompleteScanError{
		Outcome: ScanSourceOutcome{Source: source, Status: status, Detail: detail},
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
	return fmt.Sprintf("%s dependency scan is %s", e.Outcome.Source, e.Outcome.Status)
}

func (e *IncompleteScanError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}
