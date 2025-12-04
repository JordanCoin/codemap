package render

import (
	"testing"

	"codemap/scanner"
)

func TestBuildTreeStructure(t *testing.T) {
	files := []scanner.FileInfo{
		{Path: "main.go", Size: 100},
		{Path: "go.mod", Size: 50},
		{Path: "src/app.go", Size: 200},
		{Path: "src/util/helper.go", Size: 150},
		{Path: "test/main_test.go", Size: 80},
	}

	root := buildTreeStructure(files)

	// Root should have children
	if len(root.children) == 0 {
		t.Error("Root should have children")
	}

	// Check main.go exists at root
	if child, ok := root.children["main.go"]; !ok {
		t.Error("Expected main.go at root")
	} else if !child.isFile {
		t.Error("main.go should be a file")
	}

	// Check src directory exists
	if child, ok := root.children["src"]; !ok {
		t.Error("Expected src directory")
	} else if child.isFile {
		t.Error("src should be a directory")
	}
}

func TestBuildTreeStructureEmpty(t *testing.T) {
	files := []scanner.FileInfo{}
	root := buildTreeStructure(files)

	if root == nil {
		t.Fatal("Root should not be nil")
	}
	if len(root.children) != 0 {
		t.Error("Empty file list should produce empty tree")
	}
}

func TestBuildTreeStructureDeepNesting(t *testing.T) {
	files := []scanner.FileInfo{
		{Path: "a/b/c/d/e/file.go", Size: 100},
	}

	root := buildTreeStructure(files)

	// Navigate to the file
	current := root
	for _, dir := range []string{"a", "b", "c", "d", "e"} {
		child, ok := current.children[dir]
		if !ok {
			t.Errorf("Expected directory %s", dir)
			return
		}
		if child.isFile {
			t.Errorf("%s should be a directory", dir)
		}
		current = child
	}

	// Check file exists
	if file, ok := current.children["file.go"]; !ok {
		t.Error("Expected file.go")
	} else if !file.isFile {
		t.Error("file.go should be a file")
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		size     int64
		expected string
	}{
		{0, "0.0B"},
		{100, "100.0B"},
		{1023, "1023.0B"},
		{1024, "1.0KB"},
		{1536, "1.5KB"},
		{1024 * 1024, "1.0MB"},
		{1024 * 1024 * 1024, "1.0GB"},
		{1024 * 1024 * 1024 * 1024, "1.0TB"},
		{5 * 1024 * 1024, "5.0MB"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := formatSize(tt.size)
			if got != tt.expected {
				t.Errorf("formatSize(%d) = %q, want %q", tt.size, got, tt.expected)
			}
		})
	}
}

func TestGetDirStats(t *testing.T) {
	// Create a tree with known sizes
	root := &treeNode{
		children: map[string]*treeNode{
			"dir1": {
				children: map[string]*treeNode{
					"file1.go": {isFile: true, file: &scanner.FileInfo{Size: 100}},
					"file2.go": {isFile: true, file: &scanner.FileInfo{Size: 200}},
				},
			},
			"file3.go": {isFile: true, file: &scanner.FileInfo{Size: 50}},
		},
	}

	count, size := getDirStats(root)

	if count != 3 {
		t.Errorf("Expected 3 files, got %d", count)
	}
	if size != 350 {
		t.Errorf("Expected total size 350, got %d", size)
	}
}

func TestGetDirStatsSingleFile(t *testing.T) {
	node := &treeNode{
		isFile: true,
		file:   &scanner.FileInfo{Size: 123},
	}

	count, size := getDirStats(node)

	if count != 1 {
		t.Errorf("Expected 1 file, got %d", count)
	}
	if size != 123 {
		t.Errorf("Expected size 123, got %d", size)
	}
}

func TestGetDirStatsEmptyDir(t *testing.T) {
	node := &treeNode{
		children: map[string]*treeNode{},
	}

	count, size := getDirStats(node)

	if count != 0 {
		t.Errorf("Expected 0 files, got %d", count)
	}
	if size != 0 {
		t.Errorf("Expected size 0, got %d", size)
	}
}

func TestGetTopLargeFiles(t *testing.T) {
	files := []scanner.FileInfo{
		{Path: "small.go", Size: 100, Ext: ".go"},
		{Path: "medium.go", Size: 500, Ext: ".go"},
		{Path: "large.go", Size: 1000, Ext: ".go"},
		{Path: "huge.go", Size: 2000, Ext: ".go"},
		{Path: "giant.go", Size: 3000, Ext: ".go"},
		{Path: "tiny.go", Size: 50, Ext: ".go"},
		{Path: "massive.go", Size: 5000, Ext: ".go"},
	}

	top := getTopLargeFiles(files)

	// Should have 5 entries
	if len(top) != 5 {
		t.Errorf("Expected 5 top files, got %d", len(top))
	}

	// The 5 largest should be: massive, giant, huge, large, medium
	expectedLarge := []string{"massive.go", "giant.go", "huge.go", "large.go", "medium.go"}
	for _, path := range expectedLarge {
		if !top[path] {
			t.Errorf("Expected %s to be in top large files", path)
		}
	}

	// Smaller files should not be included
	if top["small.go"] {
		t.Error("small.go should not be in top large files")
	}
	if top["tiny.go"] {
		t.Error("tiny.go should not be in top large files")
	}
}

func TestGetTopLargeFilesExcludesAssets(t *testing.T) {
	files := []scanner.FileInfo{
		{Path: "code.go", Size: 100, Ext: ".go"},
		{Path: "huge_image.png", Size: 10000000, Ext: ".png"},
		{Path: "big_video.mp4", Size: 50000000, Ext: ".mp4"},
	}

	top := getTopLargeFiles(files)

	// Assets should be excluded
	if top["huge_image.png"] {
		t.Error("PNG should be excluded from top large files")
	}
	if top["big_video.mp4"] {
		t.Error("MP4 should be excluded from top large files")
	}

	// code.go should be the only "large" file
	if !top["code.go"] {
		t.Error("code.go should be in top large files")
	}
}

func TestGetTopLargeFilesFewerThan5(t *testing.T) {
	files := []scanner.FileInfo{
		{Path: "one.go", Size: 100, Ext: ".go"},
		{Path: "two.go", Size: 200, Ext: ".go"},
	}

	top := getTopLargeFiles(files)

	if len(top) != 2 {
		t.Errorf("Expected 2 files, got %d", len(top))
	}
}

func TestTreeNodeStructure(t *testing.T) {
	// Test treeNode creation
	node := &treeNode{
		name:   "test",
		isFile: false,
		children: map[string]*treeNode{
			"child.go": {
				name:   "child.go",
				isFile: true,
				file:   &scanner.FileInfo{Path: "test/child.go", Size: 100},
			},
		},
	}

	if node.name != "test" {
		t.Errorf("Expected name 'test', got %s", node.name)
	}
	if node.isFile {
		t.Error("Node should not be a file")
	}
	if len(node.children) != 1 {
		t.Errorf("Expected 1 child, got %d", len(node.children))
	}
}
