package main

import (
	"bytes"
	"strings"
	"testing"

	"codemap/analysis"
	"codemap/scanner"
)

// Above the file bound no dependency scan runs, so --importers has no answer.
// It used to print "No files import X" above an unavailable coverage line,
// which reads as a confident zero (#186, #138).
func TestImportersReportUnavailableSaysUnknown(t *testing.T) {
	var out bytes.Buffer
	renderImportersReportCLI(&out, scanner.ImportersReport{
		File:           "src/a.ts",
		CoverageStatus: string(analysis.CoverageUnavailable),
		CoverageNotes:  []string{scanner.ScanBoundNote(0, 60001, 50000)},
	})
	got := out.String()
	if strings.Contains(got, "No files import") {
		t.Fatalf("printed a confident zero with no scan:\n%s", got)
	}
	if !strings.Contains(got, "unknown, no dependency scan ran") || !strings.Contains(got, "scanned 0 of 60001 files (limit 50000") {
		t.Fatalf("output = %q, want the unknown wording and the bound note", got)
	}
}
