package render

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"codemap/scanner"
)

var treeANSIPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripTreeANSI(s string) string {
	return treeANSIPattern.ReplaceAllString(s, "")
}

func TestTreeRendersDiffModeRemoteAndImpact(t *testing.T) {
	project := scanner.Project{
		Name:      "demo",
		Root:      t.TempDir(),
		DiffRef:   "main",
		RemoteURL: "https://github.com/acme/demo",
		Files: []scanner.FileInfo{
			{Path: "src/app/main.go", Ext: ".go", Size: 120, Added: 8, Removed: 2},
			{Path: "src/app/new.go", Ext: ".go", Size: 80, Added: 5, IsNew: true},
			{Path: "README.md", Ext: ".md", Size: 30},
		},
		Impact: []scanner.ImpactInfo{{File: "src/app/main.go", UsedBy: 1}},
	}

	var buf bytes.Buffer
	Tree(&buf, project)
	out := stripTreeANSI(buf.String())

	checks := []string{
		"demo",
		"Changed: 3 files | +13 -2 lines vs main",
		"Top Extensions:",
		"↳ https://github.com/acme/demo",
		"src/app/",
		"(new) new.go (+5)",
		"✎ main.go (+8 -2)",
		"⚠ src/app/main.go is used by 1 other file",
	}
	for _, check := range checks {
		if !strings.Contains(out, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, out)
		}
	}
}

func TestPrintTreeNodeFlattensDirsAndSummarizesHiddenContent(t *testing.T) {
	root := &treeNode{children: map[string]*treeNode{
		"pkg": {
			name: "pkg",
			children: map[string]*treeNode{
				"feature": {
					name: "feature",
					children: map[string]*treeNode{
						"alpha.go": {name: "alpha.go", isFile: true, file: &scanner.FileInfo{Path: "pkg/feature/alpha.go", Ext: ".go", Size: 10}},
						"beta.go":  {name: "beta.go", isFile: true, file: &scanner.FileInfo{Path: "pkg/feature/beta.go", Ext: ".go", Size: 20}},
					},
				},
			},
		},
	}}

	var buf bytes.Buffer
	printTreeNode(&buf, root, "", true, nil, 1, 1)
	out := stripTreeANSI(buf.String())

	if !strings.Contains(out, "pkg/feature/") {
		t.Fatalf("expected flattened directory path, got:\n%s", out)
	}
	if !strings.Contains(out, "... 2 files") {
		t.Fatalf("expected hidden content summary, got:\n%s", out)
	}
}

func TestPrintTreeNodeMarksLargeAndChangedFiles(t *testing.T) {
	root := &treeNode{children: map[string]*treeNode{
		"big.go":  {name: "big.go", isFile: true, file: &scanner.FileInfo{Path: "big.go", Ext: ".go", Size: 1000}},
		"edit.go": {name: "edit.go", isFile: true, file: &scanner.FileInfo{Path: "edit.go", Ext: ".go", Size: 100, Added: 3, Removed: 1}},
		"new.go":  {name: "new.go", isFile: true, file: &scanner.FileInfo{Path: "new.go", Ext: ".go", Size: 50, Added: 5, IsNew: true}},
	}}

	var buf bytes.Buffer
	printTreeNode(&buf, root, "", true, map[string]bool{"big.go": true}, 1, 0)
	out := stripTreeANSI(buf.String())

	checks := []string{
		"⭐️ big.go",
		"✎ edit.go (+3 -1)",
		"(new) new.go (+5)",
	}
	for _, check := range checks {
		if !strings.Contains(out, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, out)
		}
	}

	if strings.Contains(out, time.Now().Format(time.RFC3339)) {
		t.Fatal("tree output should not contain timestamps")
	}
}

// Multi-file directories dropped a shared extension while single-file
// directories kept it, so the same tree showed "DetailView" and
// "SomeManager.swift" and a reader could not copy a path out of it.
func TestTreeKeepsExtensionsRegardlessOfSiblingCount(t *testing.T) {
	project := scanner.Project{
		Name: "demo",
		Root: t.TempDir(),
		Files: []scanner.FileInfo{
			{Path: "Features/DetailView.swift", Ext: ".swift", Size: 10},
			{Path: "Features/DetailViewModel.swift", Ext: ".swift", Size: 10},
			{Path: "Integrations/SomeManager.swift", Ext: ".swift", Size: 10},
		},
	}
	var buf bytes.Buffer
	Tree(&buf, project)
	out := stripTreeANSI(buf.String())
	for _, name := range []string{"DetailView.swift", "DetailViewModel.swift", "SomeManager.swift"} {
		if !strings.Contains(out, name) {
			t.Fatalf("expected %q in tree output, got:\n%s", name, out)
		}
	}
}
