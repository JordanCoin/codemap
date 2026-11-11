package render

import (
	"bytes"
	"strings"
	"testing"

	"codemap/scanner"
)

func renderHubTree(t *testing.T, project scanner.Project) string {
	t.Helper()
	project.Name = "demo"
	project.Root = t.TempDir()
	var buf bytes.Buffer
	Tree(&buf, project)
	return stripTreeANSI(buf.String())
}

func hubTestFiles(paths ...string) []scanner.FileInfo {
	files := make([]scanner.FileInfo, len(paths))
	for i, p := range paths {
		files[i] = scanner.FileInfo{Path: p, Ext: p[strings.LastIndex(p, "."):], Size: 10}
	}
	return files
}

func TestTreeStarsHubsWithImporterCounts(t *testing.T) {
	out := renderHubTree(t, scanner.Project{
		Files: hubTestFiles("scanner/a.go", "scanner/b.go", "web/types.ts", "web/x.ts", "web/big_but_unused.ts"),
		Hubs: []scanner.HubEntry{
			{Path: "scanner/", Files: []string{"scanner/a.go", "scanner/b.go"}, Importers: 73},
			{Path: "web/types.ts", Files: []string{"web/types.ts"}, Importers: 41},
		},
	})
	for _, want := range []string{
		"⭐️ scanner/ ←73",
		"⭐️ types.ts ←41",
		"⭐️ ←N = imported by N files, 3+ of them non-test (Go: package count, on its dir)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"⭐️ a.go", "⭐️ x.ts", "⭐️ big_but_unused.ts"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("unexpected %q in:\n%s", unwanted, out)
		}
	}
}

func TestTreeHubLegendCarriesNote(t *testing.T) {
	partial := renderHubTree(t, scanner.Project{Files: hubTestFiles("a.go"), Hubs: []scanner.HubEntry{}, HubNote: "graph coverage partial"})
	if !strings.Contains(partial, "of them non-test (Go: package count, on its dir); graph coverage partial") {
		t.Fatalf("partial note missing:\n%s", partial)
	}
	skipped := renderHubTree(t, scanner.Project{Files: hubTestFiles("a.go"), HubNote: "6422 files is above the 5000-file graph limit"})
	if !strings.Contains(skipped, "No ⭐️ hub marks: 6422 files is above the 5000-file graph limit") || strings.Contains(skipped, "←N") {
		t.Fatalf("skip note wrong:\n%s", skipped)
	}
}

func TestTreeWithoutHubDataHasNoStarsOrLegend(t *testing.T) {
	plain := renderHubTree(t, scanner.Project{Files: hubTestFiles("a.go", "b.go")})
	if strings.Contains(plain, "⭐") {
		t.Fatalf("stars without hub data:\n%s", plain)
	}
	diff := renderHubTree(t, scanner.Project{DiffRef: "main", Files: hubTestFiles("a.go"), HubNote: "x", Hubs: []scanner.HubEntry{}})
	if strings.Contains(diff, "⭐") {
		t.Fatalf("legend in diff mode:\n%s", diff)
	}
}
