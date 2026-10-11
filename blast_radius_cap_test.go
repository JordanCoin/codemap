package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #149: the changed-file cap took the first files in scan order and the diff
// panel header reported that subset's counts as the whole change. The cap must
// keep the highest-churn files, the header must state the full totals, and
// the deps panel must cover the same files as the diff panel.
func TestBlastRadiusCapRanksByChurnAndStatesTotals(t *testing.T) {
	requireBlastRadiusTools(t)
	root := makeBlastRadiusHubRepo(t)

	// hub.go sorts after aaa/aaa.go; give it the larger churn so only ranking
	// can put it in the single shown slot.
	hub := "package hub\n\nfunc Helper() int {\n\treturn 2\n}\n\nfunc A() {}\n\nfunc B() {}\n\nfunc C() {}\n"
	if err := os.WriteFile(filepath.Join(root, "pkg", "hub", "hub.go"), []byte(hub), 0o644); err != nil {
		t.Fatal(err)
	}

	out, stderr, err := runCodemapWithInput("", "blast-radius", "--json", "--ref", "HEAD",
		"--max-changed-files", "1", root)
	if err != nil {
		t.Fatalf("blast-radius failed: %v\nstderr=%s", err, stderr)
	}
	var bundle blastRadiusBundle
	if err := json.Unmarshal([]byte(out), &bundle); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, out)
	}

	if len(bundle.Diff.Files) != 1 || !strings.Contains(bundle.Diff.Files[0].Path, "hub") {
		t.Fatalf("shown diff files = %+v, want only the high-churn hub.go", bundle.Diff.Files)
	}
	if bundle.Diff.DiffTotals == nil || bundle.Diff.DiffTotals.Files != 2 {
		t.Fatalf("diff_totals = %+v, want files=2 for the whole diff", bundle.Diff.DiffTotals)
	}
	wantAdded := 0
	for _, f := range bundle.Diff.Files {
		wantAdded += f.Added
	}
	if bundle.Diff.DiffTotals.Added <= wantAdded {
		t.Fatalf("diff_totals.added = %d, want more than the shown subset's %d", bundle.Diff.DiffTotals.Added, wantAdded)
	}
	for _, want := range []string{"Changed: 2 files", "1 shown, ranked by churn (--max-changed-files)"} {
		if !strings.Contains(bundle.Rendered.Diff, want) {
			t.Fatalf("rendered diff header lacks %q:\n%s", want, bundle.Rendered.Diff)
		}
	}
	if len(bundle.Deps.Files) != 1 || !strings.Contains(bundle.Deps.Files[0].Path, "hub") {
		t.Fatalf("deps files = %+v, want the same single file the diff panel shows", bundle.Deps.Files)
	}
	md, stderr, err := runCodemapWithInput("", "blast-radius", "--ref", "HEAD", "--max-changed-files", "1", root)
	if err != nil {
		t.Fatalf("blast-radius markdown failed: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(md, "1 shown of 2 (ranked by churn; raise --max-changed-files)") {
		t.Fatalf("markdown summary does not label the cap:\n%s", md)
	}
}
