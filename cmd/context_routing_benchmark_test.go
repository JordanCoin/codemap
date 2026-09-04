package cmd

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"codemap/analysis"
	"codemap/scanner"
)

var benchmarkContextEnvelope ContextEnvelope
var benchmarkContextMatches contextUniquePathIndex

func benchmarkContextRoutingFiles(count int) []scanner.FileInfo {
	files := make([]string, count)
	for i := range files {
		files[i] = fmt.Sprintf("Root/Area%02d/Feature%03d/Package%03d/file%05d.go", i%40, i%200, i%500, i)
	}
	sort.Strings(files)
	return routingFiles(files...)
}

func BenchmarkContextFileIndexCaseInsensitive(b *testing.B) {
	routing := benchmarkContextRoutingFiles(50_000)
	for _, benchmark := range []struct {
		name   string
		prefix string
	}{
		{name: "broad prefix", prefix: "root"},
		{name: "narrow prefix", prefix: "root/area39/feature199"},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				index := newContextFileIndex(routing, true)
				index.forPrefix(benchmark.prefix, 3, func(string) bool { return false })
			}
		})
	}
	b.Run("exact", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			index := newContextFileIndex(routing, true)
			_, _ = index.uniqueExact(index.key("Root/Area39/Feature199/Package499/file49999.go"))
		}
	})
	b.Run("basename", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			index := newContextFileIndex(routing, true)
			matches := index.uniqueBasenames([]string{index.key("file49999.go")})
			_, _ = matches.unique(index.key("file49999.go"))
		}
	})
}

func BenchmarkContextFileIndexCaseInsensitiveMixedCase(b *testing.B) {
	files := make([]string, 50_000)
	for i := range files {
		root := "Root"
		if i%2 == 0 {
			root = "alpha"
		}
		files[i] = fmt.Sprintf("%s/Area%02d/Feature%03d/file%05d.go", root, i%40, i%200, i)
	}
	sort.Strings(files)
	routing := routingFiles(files...)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		index := newContextFileIndex(routing, true)
		index.forPrefix("root", 3, func(string) bool { return false })
	}
}

func BenchmarkContextFileIndexCaseInsensitiveWindowsPaths(b *testing.B) {
	routing := benchmarkContextRoutingFiles(50_000)
	for i := range routing {
		routing[i].Path = strings.ReplaceAll(routing[i].Path, "/", `\`)
	}
	probe := newContextFileIndex(routing, true)
	probe.preparePaths()
	if !probe.useInventory {
		b.Fatal("ordered backslash-separated inventory used fallback index")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		index := newContextFileIndex(routing, true)
		index.forPrefix("root", 3, func(string) bool { return false })
	}
}

func BenchmarkContextFileIndexCaseInsensitiveWindowsBasename(b *testing.B) {
	routing := benchmarkContextRoutingFiles(50_000)
	for i := range routing {
		routing[i].Path = strings.ReplaceAll(routing[i].Path, "/", `\`)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		index := newContextFileIndex(routing, true)
		matches := index.uniqueBasenames([]string{"file49999.go"})
		_, _ = matches.unique("file49999.go")
	}
}

func BenchmarkContextFileIndexManyBasenames(b *testing.B) {
	keys := make([]string, 128)
	for index := range keys {
		keys[index] = fmt.Sprintf("file%05d.go", index*317)
	}
	for _, benchmark := range []struct {
		name            string
		caseInsensitive bool
	}{
		{name: "case sensitive"},
		{name: "case folded", caseInsensitive: true},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			routing := benchmarkContextRoutingFiles(50_000)
			if benchmark.caseInsensitive {
				for index := range routing {
					routing[index].Path = strings.Replace(routing[index].Path, "/file", "/FILE", 1)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				index := newContextFileIndex(routing, benchmark.caseInsensitive)
				benchmarkContextMatches = index.uniqueBasenames(keys)
			}
		})
	}
}

func BenchmarkContextFileIndexCaseSensitive(b *testing.B) {
	routing := benchmarkContextRoutingFiles(50_000)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		index := newContextFileIndex(routing, false)
		index.forPrefix("Root", 3, func(string) bool { return false })
	}
}

func BenchmarkBuildContextEnvelope(b *testing.B) {
	files := benchmarkContextRoutingFiles(5_000)
	imports := make(map[string][]string, len(files))
	importers := make(map[string][]string, len(files))
	for i := 1; i < len(files); i++ {
		current := files[i].Path
		previous := files[i-1].Path
		imports[current] = []string{previous}
		importers[previous] = []string{current}
	}
	graph := &scanner.FileGraph{
		Imports:   imports,
		Importers: importers,
		Coverage:  scanner.GraphCoverage{Status: analysis.CoverageComplete},
	}
	deps := testContextEnvelopeDeps(files, graph)
	root := b.TempDir()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		benchmarkContextEnvelope = buildContextEnvelopeWithDeps(
			context.Background(),
			root,
			"refactor Root/Area39/Feature199/Package499/file49999.go",
			true,
			deps,
		)
	}
}
