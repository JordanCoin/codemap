package cmd

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"codemap/scanner"
)

func benchmarkContextRoutingFiles() []scanner.FileInfo {
	files := make([]string, 50_000)
	for i := range files {
		files[i] = fmt.Sprintf("Root/Area%02d/Feature%03d/Package%03d/file%05d.go", i%40, i%200, i%500, i)
	}
	sort.Strings(files)
	return routingFiles(files...)
}

func BenchmarkContextFileIndexCaseInsensitive(b *testing.B) {
	routing := benchmarkContextRoutingFiles()
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
	routing := benchmarkContextRoutingFiles()
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
	routing := benchmarkContextRoutingFiles()
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

func BenchmarkContextFileIndexCaseSensitive(b *testing.B) {
	routing := benchmarkContextRoutingFiles()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		index := newContextFileIndex(routing, false)
		index.forPrefix("Root", 3, func(string) bool { return false })
	}
}
