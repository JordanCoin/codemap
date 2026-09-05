package render

import (
	"fmt"
	"io"
	"path/filepath"
	"testing"

	"codemap/scanner"
)

var benchmarkTree *treeNode

func BenchmarkBuildTreeStructure(b *testing.B) {
	files := benchmarkRenderFiles(50_000)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		benchmarkTree = buildTreeStructure(files)
	}
}

func BenchmarkTree(b *testing.B) {
	project := scanner.Project{Root: "benchmark", Files: benchmarkRenderFiles(5_000)}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		Tree(io.Discard, project)
	}
}

func BenchmarkSkyline(b *testing.B) {
	project := scanner.Project{Root: "benchmark", Files: benchmarkRenderFiles(50_000)}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		Skyline(io.Discard, project, false)
	}
}

func benchmarkRenderFiles(count int) []scanner.FileInfo {
	files := make([]scanner.FileInfo, count)
	extensions := []string{".go", ".ts", ".py", ".rs", ".java"}
	for i := range files {
		ext := extensions[i%len(extensions)]
		files[i] = scanner.FileInfo{
			Path: filepath.Join(fmt.Sprintf("area-%03d", i%100), fmt.Sprintf("feature-%03d", i%500), fmt.Sprintf("file-%05d%s", i, ext)),
			Size: int64(64 + i%4096),
			Ext:  ext,
		}
	}
	return files
}
