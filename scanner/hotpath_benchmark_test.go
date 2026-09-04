package scanner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

var (
	benchmarkScannedFiles []FileInfo
	benchmarkFileIndex    *fileIndex
	benchmarkGraph        *FileGraph
	benchmarkResolved     []string
)

func BenchmarkScanFiles(b *testing.B) {
	root, _ := benchmarkScannerTree(b, 5_000)
	cache := NewGitIgnoreCache(root)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		var err error
		benchmarkScannedFiles, err = ScanFiles(context.Background(), root, cache, nil, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBuildFileGraphFromAnalyses(b *testing.B) {
	root, files := benchmarkScannerTree(b, 5_000)
	analyses := make([]FileAnalysis, 0, len(files))
	for _, file := range files {
		analyses = append(analyses, FileAnalysis{
			Path:     file.Path,
			Language: "go",
			Imports:  []string{"example.com/bench/shared"},
		})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		var err error
		benchmarkGraph, err = BuildFileGraphFromAnalyses(context.Background(), root, analyses, Filters{})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBuildFileIndex(b *testing.B) {
	files := benchmarkFileInventory(50_000)
	benchmarkBuildFileIndex(b, files)
}

func BenchmarkBuildFileIndexSparseDirectories(b *testing.B) {
	files := make([]FileInfo, 50_000)
	for i := range files {
		files[i] = FileInfo{
			Path: fmt.Sprintf("pkg/area-%05d/file.go", i),
			Size: 128,
			Ext:  ".go",
		}
	}
	benchmarkBuildFileIndex(b, files)
}

func benchmarkBuildFileIndex(b *testing.B, files []FileInfo) {
	b.Helper()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		var err error
		benchmarkFileIndex, err = buildFileIndexContext(context.Background(), files, "example.com/bench")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTryExactMatchExplicitPath(b *testing.B) {
	idx := buildFileIndex(benchmarkFileInventory(50_000), "")
	path := "pkg/area-199/feature-499/file-49999.go"
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		benchmarkResolved = tryExactMatch(path, idx, "go")
	}
}

func benchmarkScannerTree(b *testing.B, count int) (string, []FileInfo) {
	b.Helper()
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/bench\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "shared"), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "shared", "shared.go"), []byte("package shared\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	files := make([]FileInfo, 0, count)
	for i := range count {
		dir := filepath.Join(root, "pkg", fmt.Sprintf("area-%03d", i/100))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatal(err)
		}
		path := filepath.Join(dir, fmt.Sprintf("file-%05d.go", i))
		if err := os.WriteFile(path, []byte("package bench\n"), 0o644); err != nil {
			b.Fatal(err)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			b.Fatal(err)
		}
		files = append(files, FileInfo{Path: rel, Size: 14, Ext: ".go"})
	}
	return root, files
}

func benchmarkFileInventory(count int) []FileInfo {
	files := make([]FileInfo, count)
	for i := range files {
		files[i] = FileInfo{
			Path: fmt.Sprintf("pkg/area-%03d/feature-%03d/file-%05d.go", i%200, i%500, i),
			Size: 128,
			Ext:  ".go",
		}
	}
	return files
}
