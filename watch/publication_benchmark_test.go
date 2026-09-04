package watch

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"codemap/config"
	"codemap/scanner"
)

var benchmarkPublishedState State

func BenchmarkStatePublisherSnapshot(b *testing.B) {
	publisher := benchmarkStatePublisher(b, 5_000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		benchmarkPublishedState = publisher.snapshot(uint64(i + 1))
	}
}

func BenchmarkStatePublisherPublish(b *testing.B) {
	publisher := benchmarkStatePublisher(b, 5_000)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		publisher.markDirty(time.Now())
		if err := publisher.publish(); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkStatePublisher(b *testing.B, count int) *statePublisher {
	b.Helper()
	root := b.TempDir()
	files := make(map[string]*scanner.FileInfo, count)
	configured := make(map[string]struct{}, count)
	imports := make(map[string][]string, count)
	importers := make(map[string][]string, count)
	for i := range count {
		path := fmt.Sprintf("pkg/area-%03d/file-%05d.go", i%100, i)
		files[path] = &scanner.FileInfo{Path: path, Size: 128, Ext: ".go"}
		configured[path] = struct{}{}
		if i > 0 {
			previous := fmt.Sprintf("pkg/area-%03d/file-%05d.go", (i-1)%100, i-1)
			imports[path] = []string{previous}
			importers[previous] = append(importers[previous], path)
		}
	}
	daemon := &Daemon{
		root: root,
		graph: &Graph{
			Root:            root,
			Files:           files,
			ConfiguredFiles: configured,
			FileGraph:       &scanner.FileGraph{Root: root, Imports: imports, Importers: importers},
			State:           make(map[string]*FileState),
			WorkingSet:      NewWorkingSet(),
			HasDeps:         true,
			GraphState:      newGraphState(root, config.ProjectConfig{}, graphLifecycleAvailable, time.Now(), nil),
		},
	}
	return newStatePublisher(daemon, filepath.Join(root, "state.json"), "benchmark-instance")
}
