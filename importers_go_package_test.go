package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"codemap/scanner"
)

// The printed lines for a Go file inside a multi-file package, on the #191
// fixture: the count is labelled with its unit, and an empty answer says what
// the graph does not model instead of a bare zero.
func TestImportersCLIReportsGoPackageGranularity(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("testdata", "go-multi-file-package"))
	if err != nil {
		t.Fatal(err)
	}
	fg, err := scanner.BuildFileGraph(context.Background(), root, scanner.Filters{})
	if err != nil {
		t.Fatalf("BuildFileGraph: %v", err)
	}

	render := func(file string) (scanner.ImportersReport, string) {
		t.Helper()
		report, err := buildImportersReportFromGraph(root, file, fg)
		if err != nil {
			t.Fatalf("buildImportersReportFromGraph(%s): %v", file, err)
		}
		var buf strings.Builder
		renderImportersReportCLI(&buf, report)
		return report, buf.String()
	}

	report, out := render("multi/a.go")
	if got, want := report.Importers, []string{"app/main.go"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("importers of multi/a.go = %v, want %v", got, want)
	}
	if report.Package != "example.com/demo/multi" || report.PackageSiblings != 1 {
		t.Fatalf("report package = %q siblings = %d, want example.com/demo/multi with 1 sibling", report.Package, report.PackageSiblings)
	}
	want := "   Imported by 1 file(s) (package example.com/demo/multi: Go imports name packages, not files)\n   • app/main.go\n"
	if !strings.Contains(out, want) {
		t.Fatalf("--importers multi/a.go output:\n%q\nwant it to contain:\n%q", out, want)
	}

	_, out = render("multi/a_test.go")
	want = "No cross-package importers of multi/a_test.go. Same-package references are not modeled (2 other files in package example.com/demo/multi).\n"
	if !strings.Contains(out, want) {
		t.Fatalf("--importers multi/a_test.go output:\n%q\nwant it to contain:\n%q", out, want)
	}

	// A single-file package with no importers is a true zero, and gets no
	// same-package caveat because there is nothing unmodeled.
	report, out = render("orphan/orphan.go")
	if len(report.Importers) != 0 || report.PackageSiblings != 0 {
		t.Fatalf("orphan report = %+v, want zero importers and zero siblings", report)
	}
	if !strings.Contains(out, "No files import orphan/orphan.go.\n") || strings.Contains(out, "Same-package") {
		t.Fatalf("--importers orphan/orphan.go output:\n%q\nwant a plain zero with no same-package caveat", out)
	}
}

// End to end through the binary, as the issue reproduced it.
func TestImportersCommandListsMultiFilePackageImporter(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("testdata", "go-multi-file-package"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := runCodemap("--importers", "multi/a.go", root)
	if err != nil {
		t.Fatalf("codemap --importers multi/a.go: %v\n%s", err, out)
	}
	if !strings.Contains(out, "• app/main.go") {
		t.Fatalf("--importers multi/a.go must list app/main.go:\n%s", out)
	}
	if strings.Contains(out, "No files import") {
		t.Fatalf("--importers multi/a.go reported a zero:\n%s", out)
	}
}
