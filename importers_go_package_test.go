package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codemap/scanner"
)

// The printed lines for a Go file inside a multi-file package, on the #191
// fixture: the count is labelled with its unit, and an empty answer says what
// the graph does not model instead of a bare zero.
func TestImportersCLIReportsGoPackageGranularity(t *testing.T) {
	root, err := filepath.Abs("testdata/go-multi-file-package")
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
	want := "   Package example.com/demo/multi is imported by 1 file (Go imports name packages, not files)\n   • app/main.go\n"
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

// Three importers of a two-file package make the package a hub, not the file:
// --importers names the package as the thing imported and raises no HUB FILE
// warning, while a one-file package at the same count still does (#285).
func TestImportersCLIGoPackageHubIsNotAFileHub(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":           "module example.com/hub\n\ngo 1.21\n",
		"multi/a.go":       "package multi\n\nfunc A() {}\n",
		"multi/b.go":       "package multi\n\nfunc B() {}\n",
		"single/single.go": "package single\n\nfunc S() {}\n",
	}
	for _, user := range []string{"u1", "u2", "u3"} {
		files[user+"/"+user+".go"] = "package " + user + "\n\nimport (\n\t\"example.com/hub/multi\"\n\t\"example.com/hub/single\"\n)\n\nfunc F() { multi.A(); single.S() }\n"
	}
	for rel, body := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
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
	if report.IsHub || strings.Contains(out, "HUB") {
		t.Fatalf("multi/a.go flagged as a hub file (is_hub=%v):\n%s", report.IsHub, out)
	}
	if !strings.Contains(out, "Package example.com/hub/multi is imported by 3 files") {
		t.Fatalf("--importers multi/a.go must name the package as imported:\n%s", out)
	}
	report, out = render("single/single.go")
	if !report.IsHub || !strings.Contains(out, "HUB FILE: single/single.go") {
		t.Fatalf("one-file package lost its hub warning (is_hub=%v):\n%s", report.IsHub, out)
	}
}

// End to end through the binary, as the issue reproduced it.
func TestImportersCommandListsMultiFilePackageImporter(t *testing.T) {
	root, err := filepath.Abs("testdata/go-multi-file-package")
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
