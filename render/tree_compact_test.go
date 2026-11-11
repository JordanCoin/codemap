package render

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"codemap/scanner"
)

func renderCompactTree(t *testing.T, project scanner.Project) string {
	t.Helper()
	project.Name = "demo"
	project.Root = t.TempDir()
	var buf bytes.Buffer
	Tree(&buf, project)
	return stripTreeANSI(buf.String())
}

func goFiles(paths ...string) []scanner.FileInfo {
	files := make([]scanner.FileInfo, len(paths))
	for i, p := range paths {
		files[i] = scanner.FileInfo{Path: p, Ext: p[strings.LastIndex(p, "."):], Size: 10}
	}
	return files
}

func TestTestSubjectByLanguage(t *testing.T) {
	exists := map[string]bool{"x.go": true, "app.ts": true, "util.tsx": true, "mod.py": true}
	cases := []struct {
		name, subject string
		isTest        bool
	}{
		{"x_test.go", "x.go", true},
		{"x_more_test.go", "x.go", true},
		{"x_more_deep_test.go", "x.go", true},
		{"orphan_test.go", "", true},
		{"app.test.ts", "app.ts", true},
		{"app.spec.ts", "app.ts", true},
		{"util.test.ts", "util.tsx", true},
		{"test_mod.py", "mod.py", true},
		{"mod_test.py", "mod.py", true},
		{"test_none.py", "", true},
		{"x.go", "", false},
		{"helper.py", "", false},
		{"README.md", "", false},
	}
	for _, c := range cases {
		subject, isTest := testSubject(c.name, exists)
		if subject != c.subject || isTest != c.isTest {
			t.Errorf("testSubject(%q) = (%q, %v), want (%q, %v)", c.name, subject, isTest, c.subject, c.isTest)
		}
	}
}

func TestTreeFoldsTestsIntoSourceAndKeepsDirCounts(t *testing.T) {
	out := renderCompactTree(t, scanner.Project{Files: goFiles(
		"pkg/filegraph.go", "pkg/filegraph_test.go", "pkg/filegraph_more_test.go", "pkg/filegraph_go_test.go",
		"pkg/other.go", "pkg/main_test.go", "pkg/testmain_test.go",
	)})
	for _, want := range []string{"pkg/ (7 files", "filegraph.go (+3 tests)", "+2 other tests"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "filegraph_test.go") || strings.Contains(out, "other.go (+") {
		t.Fatalf("test file listed or misattributed:\n%s", out)
	}
}

func TestTreeStarsHubsWithCountsAndLegend(t *testing.T) {
	out := renderCompactTree(t, scanner.Project{
		Files: goFiles("scanner/a.go", "scanner/b.go", "web/types.ts", "web/x.ts"),
		Hubs: []scanner.HubEntry{
			{Path: "scanner/", Files: []string{"scanner/a.go", "scanner/b.go"}, Importers: 73},
			{Path: "web/types.ts", Files: []string{"web/types.ts"}, Importers: 41},
		},
	})
	for _, want := range []string{
		"⭐️ scanner/ ←73",
		"⭐️ types.ts ←41",
		"⭐️ ←N = imported by N files, 3+ of them non-test",
		"Top hubs: scanner/ ←73, web/types.ts ←41",
		"Next: codemap --deps · codemap --importers scanner/a.go",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "⭐️ a.go") {
		t.Fatalf("package file starred on its own:\n%s", out)
	}
}

func TestTreeSaysWhyHubStarsAreMissing(t *testing.T) {
	out := renderCompactTree(t, scanner.Project{Files: goFiles("a.go"), HubNote: "dependency graph unavailable: no ast-grep"})
	if !strings.Contains(out, "No ⭐️ hub marks: dependency graph unavailable: no ast-grep") || strings.Contains(out, "Top hubs") {
		t.Fatalf("unexpected hub legend:\n%s", out)
	}
	if plain := renderCompactTree(t, scanner.Project{Files: goFiles("a.go")}); strings.Contains(plain, "⭐") {
		t.Fatalf("stars without hub data:\n%s", plain)
	}
}

func TestRunKeyCollapsesOnlyVaryingSegments(t *testing.T) {
	var files []string
	for _, stamp := range []string{"2026-10-21T12-01-02-111Z", "2026-10-21T12-05-09-222Z", "2026-10-22T08-00-00-333Z", "2026-10-23T09-30-00-444Z"} {
		files = append(files, "logs/page-"+stamp+".yml")
	}
	files = append(files, "logs/console-2026-10-21T12-01-02-111Z.yml", "logs/001_init.sql", "logs/002_users.sql", "logs/003_posts.sql", "logs/004_tags.sql")
	out := renderCompactTree(t, scanner.Project{Files: goFiles(files...)})
	if !strings.Contains(out, "page-*.yml ×4") {
		t.Fatalf("run not collapsed:\n%s", out)
	}
	for _, keep := range []string{"console-2026-10-21T12-01-02-111Z.yml", "001_init.sql", "004_tags.sql"} {
		if !strings.Contains(out, keep) {
			t.Fatalf("%q merged though its fixed part differs:\n%s", keep, out)
		}
	}
	if a, _ := runKey("file12.go"); a != "file*.go" {
		t.Fatalf("runKey(file12.go) = %q", a)
	}
}

func TestTreeCollapsesUntrackedDirectory(t *testing.T) {
	out := renderCompactTree(t, scanner.Project{
		Files:         goFiles(".scratch/a.yml", ".scratch/deep/b.yml", "main.go"),
		UntrackedDirs: map[string]bool{".scratch": true},
	})
	if !strings.Contains(out, ".scratch/ (untracked, 2 files, 20.0B)") || strings.Contains(out, "a.yml") {
		t.Fatalf("untracked dir not collapsed:\n%s", out)
	}
}

func TestTreeSingleFileDirsRenderOneLineAtAnyDepth(t *testing.T) {
	out := renderCompactTree(t, scanner.Project{Depth: 3, Files: goFiles(
		".claude/skills/codemap/SKILL.md", ".claude/skills/explore/SKILL.md",
		"plugins/codemap/skills/codemap/SKILL.md", "plugins/codemap/skills/explore/SKILL.md", "plugins/codemap/README.md",
	)})
	if strings.Count(out, "codemap/SKILL.md (10.0B)") != 2 || strings.Contains(out, "directories") {
		t.Fatalf("single-file dirs rendered inconsistently:\n%s", out)
	}
}

func TestTreeLongNameDoesNotForceSingleColumn(t *testing.T) {
	paths := []string{"cmd/" + strings.Repeat("very_long_name_", 5) + ".go"}
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		paths = append(paths, "cmd/"+n+".go")
	}
	out := renderCompactTree(t, scanner.Project{Files: goFiles(paths...)})
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, " a.go") && strings.Count(line, ".go") >= 2 {
			if !strings.Contains(out, paths[0][len("cmd/"):]) {
				t.Fatalf("long name dropped:\n%s", out)
			}
			return
		}
	}
	t.Fatalf("long name forced one column:\n%s", out)
}

func TestDetailTreeShowsLinesFullHubPathsAndSymbols(t *testing.T) {
	out := renderCompactTree(t, scanner.Project{
		Files:      goFiles("cmd/hooks.go", "cmd/hooks_test.go", "cmd/x.go"),
		Hubs:       []scanner.HubEntry{{Path: "cmd/hooks.go", Files: []string{"cmd/hooks.go"}, Importers: 5}},
		LineCounts: map[string]int{"cmd/hooks.go": 2535, "cmd/hooks_test.go": 900, "cmd/x.go": 12},
		HubSymbols: map[string][]scanner.Symbol{"cmd/hooks.go": {{Name: "Run", Line: 41}}},
	})
	for _, want := range []string{
		"⭐️ hooks.go 2535L ←5 (+1 test)", "x.go ", "shown at 100+",
		"Hubs (1): cmd/hooks.go ←5 2535L", "  cmd/hooks.go:41 Run",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestDetailTreeHidesShortLineCountsAndCapsHubList(t *testing.T) {
	var hubs []scanner.HubEntry
	var paths []string
	for i := 0; i < 30; i++ {
		p := fmt.Sprintf("src/some/deeply/nested/module_%02d/implementation.ts", i)
		paths = append(paths, p)
		hubs = append(hubs, scanner.HubEntry{Path: p, Files: []string{p}, Importers: 100 - i})
	}
	out := renderCompactTree(t, scanner.Project{Files: goFiles(paths...), Hubs: hubs, LineCounts: map[string]int{paths[0]: 12}, HubSymbols: map[string][]scanner.Symbol{}})
	if strings.Contains(out, "12L") {
		t.Fatalf("line count under the floor shown:\n%s", out)
	}
	trailer := out[strings.Index(out, "⭐️ ←N"):]
	if !strings.Contains(trailer, "Hubs (top 10 of 30): ") || !strings.Contains(trailer, "module_09/") || strings.Contains(trailer, "module_10/") {
		t.Fatalf("hub list not capped:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if n := len([]rune(line)); n > maxTrailerLine {
			t.Fatalf("line of %d runes:\n%s", n, line)
		}
	}
}

func TestTestsOnlyDirListsTestFiles(t *testing.T) {
	out := renderCompactTree(t, scanner.Project{Files: goFiles("tests/test_alpha.py", "tests/test_beta.py", "tests/conftest.py", "tests/utils.py", "tests/test_utils.py")})
	if !strings.Contains(out, "test_alpha.py") || !strings.Contains(out, "utils.py (+1 test)") || strings.Contains(out, "other test") {
		t.Fatalf("tests-only dir collapsed:\n%s", out)
	}
}

func TestOutlierKeepsSortedOrder(t *testing.T) {
	long := strings.Repeat("m", 75) + ".go"
	paths := []string{"cmd/" + long}
	for _, n := range []string{"a", "b", "c", "d", "x", "y", "z", "zz"} {
		paths = append(paths, "cmd/"+n+".go")
	}
	out := renderCompactTree(t, scanner.Project{Files: goFiles(paths...)})
	ia, il, iz := strings.Index(out, " a.go"), strings.Index(out, long), strings.Index(out, " x.go")
	if ia < 0 || il < ia || iz < il {
		t.Fatalf("order not kept (a=%d long=%d x=%d):\n%s", ia, il, iz, out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, " a.go") && strings.Count(line, ".go") >= 2 {
			return
		}
	}
	t.Fatalf("outlier forced one column:\n%s", out)
}

func TestDiffTreeKeepsTestsAndStars(t *testing.T) {
	out := renderCompactTree(t, scanner.Project{DiffRef: "main", Files: []scanner.FileInfo{
		{Path: "a.go", Ext: ".go", Size: 1, Added: 1}, {Path: "a_test.go", Ext: ".go", Size: 1, Added: 1},
	}})
	if !strings.Contains(out, "a_test.go") || strings.Contains(out, "+1 test") || strings.Contains(out, "Top hubs") {
		t.Fatalf("diff render changed:\n%s", out)
	}
}
