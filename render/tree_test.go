package render

import (
	"bytes"
	"math/rand/v2"
	"reflect"
	"strings"
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

func TestTitleCase(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "single word", input: "render", want: "Render"},
		{name: "multiple words", input: "dependency flow", want: "Dependency Flow"},
		{name: "extra spaces collapsed", input: "go   module", want: "Go Module"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := titleCase(tt.input)
			if got != tt.want {
				t.Errorf("titleCase(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGetSystemName(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "skips src prefix", path: "src/auth/service", want: "Auth"},
		{name: "normalizes separators and dashes", path: "pkg\\payment-gateway\\v2", want: "Payment Gateway"},
		{name: "normalizes underscores", path: "internal/user_profile/api", want: "User Profile"},
		{name: "root marker fallback", path: ".", want: "."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getSystemName(tt.path)
			if got != tt.want {
				t.Errorf("getSystemName(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestFilterCodeFiles(t *testing.T) {
	tests := []struct {
		name  string
		files []scanner.FileInfo
		want  []scanner.FileInfo
	}{
		{
			name: "filters to code extensions and known code filenames",
			files: []scanner.FileInfo{
				{Path: "main.go", Ext: ".go"},
				{Path: "README.md", Ext: ".md"},
				{Path: "Dockerfile", Ext: ""},
				{Path: "assets/logo.png", Ext: ".png"},
			},
			want: []scanner.FileInfo{
				{Path: "main.go", Ext: ".go"},
				{Path: "Dockerfile", Ext: ""},
			},
		},
		{
			name: "returns original slice when no code files match",
			files: []scanner.FileInfo{
				{Path: "README.md", Ext: ".md"},
				{Path: "assets/logo.png", Ext: ".png"},
			},
			want: []scanner.FileInfo{
				{Path: "README.md", Ext: ".md"},
				{Path: "assets/logo.png", Ext: ".png"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterCodeFiles(tt.files)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("filterCodeFiles() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestAggregateByExtension(t *testing.T) {
	files := []scanner.FileInfo{
		{Path: "main.go", Ext: ".go", Size: 100},
		{Path: "service.go", Ext: ".go", Size: 40},
		{Path: "app.ts", Ext: ".ts", Size: 120},
		{Path: "Makefile", Ext: "", Size: 30},
	}

	got := aggregateByExtension(files)
	if len(got) != 3 {
		t.Fatalf("aggregateByExtension() len = %d, want 3", len(got))
	}

	if got[0].ext != ".go" || got[0].size != 140 || got[0].count != 2 {
		t.Errorf("first aggregate = %+v, want ext=.go size=140 count=2", got[0])
	}
	if got[1].ext != ".ts" || got[1].size != 120 || got[1].count != 1 {
		t.Errorf("second aggregate = %+v, want ext=.ts size=120 count=1", got[1])
	}
	if got[2].ext != "Makefile" || got[2].size != 30 || got[2].count != 1 {
		t.Errorf("third aggregate = %+v, want ext=Makefile size=30 count=1", got[2])
	}
}

func TestGetBuildingChar(t *testing.T) {
	tests := []struct {
		name string
		ext  string
		want rune
	}{
		{name: "go", ext: ".go", want: '▓'},
		{name: "javascript", ext: ".js", want: '░'},
		{name: "ruby", ext: ".rb", want: '▒'},
		{name: "shell", ext: ".sh", want: '█'},
		{name: "makefile", ext: "makefile", want: '█'},
		{name: "default", ext: ".unknown", want: '▓'},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getBuildingChar(tt.ext)
			if got != tt.want {
				t.Errorf("getBuildingChar(%q) = %q, want %q", tt.ext, got, tt.want)
			}
		})
	}
}

func TestCreateBuildings(t *testing.T) {
	t.Run("empty input", func(t *testing.T) {
		got := createBuildings(nil, 80)
		if got != nil {
			t.Errorf("createBuildings(nil, 80) = %#v, want nil", got)
		}
	})

	t.Run("assigns scaled heights and trims long extension labels", func(t *testing.T) {
		prevRng := rng
		t.Cleanup(func() {
			rng = prevRng
		})
		rng = rand.New(rand.NewPCG(42, 0))
		sorted := []extAgg{
			{ext: ".verylong", size: 1000, count: 1},
			{ext: ".go", size: 400, count: 1},
			{ext: ".ts", size: 100, count: 1},
		}

		got := createBuildings(sorted, 120)
		if len(got) != 3 {
			t.Fatalf("createBuildings() len = %d, want 3", len(got))
		}

		byExt := make(map[string]building, len(got))
		for _, b := range got {
			byExt[b.ext] = b
			if b.height < minHeight || b.height > maxHeight {
				t.Errorf("building %q height = %d, want within [%d,%d]", b.ext, b.height, minHeight, maxHeight)
			}
		}

		if byExt[".verylong"].height != maxHeight {
			t.Errorf("largest extension height = %d, want %d", byExt[".verylong"].height, maxHeight)
		}
		if byExt[".ts"].height != minHeight {
			t.Errorf("smallest extension height = %d, want %d", byExt[".ts"].height, minHeight)
		}
		if byExt[".verylong"].extLabel != ".very" {
			t.Errorf("long ext label = %q, want %q", byExt[".verylong"].extLabel, ".very")
		}
	})

	t.Run("drops buildings until layout fits width budget", func(t *testing.T) {
		rng = rand.New(rand.NewPCG(42, 0))
		sorted := []extAgg{
			{ext: ".go", size: 1000, count: 1},
			{ext: ".ts", size: 900, count: 1},
			{ext: ".js", size: 800, count: 1},
			{ext: ".rb", size: 700, count: 1},
			{ext: ".py", size: 600, count: 1},
		}

		got := createBuildings(sorted, 24)
		totalWidth := 0
		for _, b := range got {
			totalWidth += buildingWidth + b.gap
		}

		if totalWidth > 16 {
			t.Errorf("total building width = %d, want <= %d", totalWidth, 16)
		}
		if len(got) == 0 {
			t.Fatal("expected at least one building to remain after trimming")
		}
	})
}

func TestDepgraphNoSourceFiles(t *testing.T) {
	var buf bytes.Buffer
	Depgraph(&buf, scanner.DepsProject{
		Root:  "/tmp/example",
		Files: nil,
	})

	out := buf.String()
	if !strings.Contains(out, "No source files found.") {
		t.Fatalf("Depgraph output missing empty-source message, got %q", out)
	}
}
