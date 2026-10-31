package render

import (
	"bytes"
	"strings"
	"testing"

	"codemap/scanner"
)

// Every file keeps its extension in every rendering: the compact "all .ext"
// row for a multi-file directory, the single-file directory, and the --diff
// overlay. Stripping the shared extension made paths uncopyable (#154 part 2).
func TestTreeKeepsExtensionsInMultiFileDirectoriesUnderDiff(t *testing.T) {
	project := scanner.Project{
		Name: "app",
		Root: t.TempDir(),
		Files: []scanner.FileInfo{
			{Path: "Features/Detail/DetailView.swift", Ext: ".swift", Size: 10, Added: 4, Removed: 4},
			{Path: "Features/Detail/DetailViewModel.swift", Ext: ".swift", Size: 10, Added: 10, Removed: 9},
			{Path: "Integrations/SomeManager.swift", Ext: ".swift", Size: 10, Added: 18, Removed: 16},
		},
	}
	for _, diff := range []string{"main", ""} {
		project.DiffRef = diff
		var buf bytes.Buffer
		Tree(&buf, project)
		out := stripTreeANSI(buf.String())
		for _, want := range []string{"DetailView.swift", "DetailViewModel.swift", "SomeManager.swift"} {
			if !strings.Contains(out, want) {
				t.Errorf("DiffRef=%q: tree lost the extension of %s:\n%s", diff, want, out)
			}
		}
		if strings.Contains(out, "DetailView ") || strings.Contains(out, "DetailViewModel ") {
			t.Errorf("DiffRef=%q: tree rendered a name without its extension:\n%s", diff, out)
		}
	}
}
