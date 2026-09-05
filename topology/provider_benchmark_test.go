package topology

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"codemap/config"
	"codemap/scanner"
)

var (
	benchmarkManifests []string
	benchmarkIdentity  CacheIdentity
	benchmarkGraph     *Graph
)

func BenchmarkBuildGraphWithProviders(b *testing.B) {
	root, _, provider := benchmarkTopologyTree(b, 5_000)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		var err error
		benchmarkGraph, benchmarkIdentity, err = BuildGraphWithProviders(context.Background(), root, []Provider{provider})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDiscoverManifests(b *testing.B) {
	root, _, provider := benchmarkTopologyTree(b, 5_000)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		var err error
		benchmarkManifests, err = discoverManifests(context.Background(), root, []Provider{provider}, config.ProjectConfig{})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBuildCacheIdentity(b *testing.B) {
	root, files, provider := benchmarkTopologyTree(b, 5_000)
	manifests, err := discoverManifests(context.Background(), root, []Provider{provider}, config.ProjectConfig{})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		benchmarkIdentity, err = BuildCacheIdentity(root, files, manifests, []Provider{provider})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkTopologyTree(b *testing.B, count int) (string, []scanner.FileInfo, Provider) {
	b.Helper()
	root := b.TempDir()
	files := make([]scanner.FileInfo, 0, count)
	for i := range count {
		dir := filepath.Join(root, fmt.Sprintf("module-%03d", i/100))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatal(err)
		}
		name := fmt.Sprintf("file-%05d.go", i)
		if i%100 == 0 {
			name = "bench.module"
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("module benchmark\n"), 0o644); err != nil {
			b.Fatal(err)
		}
		if name == "bench.module" {
			continue
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			b.Fatal(err)
		}
		files = append(files, scanner.FileInfo{Path: rel, Size: 17, Ext: filepath.Ext(rel)})
	}
	provider := stubProvider{name: "benchmark", version: "1", languages: []string{"go"}, manifests: []string{"bench.module"}}
	return root, files, provider
}
