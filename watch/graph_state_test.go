package watch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"codemap/analysis"
	"codemap/config"
	"codemap/scanner"

	"github.com/fsnotify/fsnotify"
)

func TestGraphProvenanceValidation(t *testing.T) {
	root := t.TempDir()
	cfg := config.ProjectConfig{Only: []string{"go", "rust"}, Exclude: []string{"vendor", "generated"}}
	available := newGraphState(root, cfg, graphLifecycleAvailable, time.Unix(10, 0), []string{"dep.go", "main.go"})
	base := State{
		Graph:     &available,
		Imports:   map[string][]string{"main.go": {"dep.go"}},
		Importers: map[string][]string{"dep.go": {"main.go"}},
	}
	configuredCount := 2
	base.ConfiguredFileCount = &configuredCount

	graph, reason := validateCachedGraph(&base, root, cfg)
	if graph == nil || reason != "" || len(graph.Importers["dep.go"]) != 1 {
		t.Fatalf("valid cache = %#v, %q", graph, reason)
	}
	if graph, reason := ValidateCachedGraphForInventory(&base, root, cfg, []string{"main.go", "dep.go"}); graph == nil || reason != "" {
		t.Fatalf("matching inventory = %#v, %q", graph, reason)
	}
	edgeFree := base
	edgeFree.Coverage.Status = analysis.CoverageComplete
	edgeFree.Imports = map[string][]string{}
	edgeFree.Importers = map[string][]string{}
	if graph, reason := ValidateCachedGraph(&edgeFree, root, cfg); graph == nil || reason != "" {
		t.Fatalf("complete edge-free cache = %#v, %q", graph, reason)
	}
	if graph, reason := ValidateCachedGraphForInventory(&base, root, cfg, []string{"main.go", "new.go"}); graph != nil || reason != graphCacheInventoryMismatch {
		t.Fatalf("replacement inventory = %#v, %q; want nil, %q", graph, reason, graphCacheInventoryMismatch)
	}
	inconsistentCount := base
	count := 3
	inconsistentCount.ConfiguredFileCount = &count
	if graph, reason := ValidateCachedGraphForInventory(&inconsistentCount, root, cfg, []string{"main.go", "dep.go"}); graph != nil || reason != graphCacheInventoryMismatch {
		t.Fatalf("inconsistent count = %#v, %q; want nil, %q", graph, reason, graphCacheInventoryMismatch)
	}

	tests := []struct {
		name   string
		mutate func(*State) (string, config.ProjectConfig)
		want   string
	}{
		{name: "legacy", mutate: func(state *State) (string, config.ProjectConfig) { state.Graph = nil; return root, cfg }, want: graphCacheLegacy},
		{name: "stale", mutate: graphStatusMutation(root, cfg, graphLifecycleStale), want: string(graphLifecycleStale)},
		{name: "skipped size", mutate: graphStatusMutation(root, cfg, graphLifecycleSkippedSize), want: string(graphLifecycleSkippedSize)},
		{name: "failed", mutate: graphStatusMutation(root, cfg, graphLifecycleFailed), want: string(graphLifecycleFailed)},
		{name: "root mismatch", mutate: func(*State) (string, config.ProjectConfig) { return t.TempDir(), cfg }, want: graphCacheRootMismatch},
		{name: "filter mismatch", mutate: func(*State) (string, config.ProjectConfig) { return root, config.ProjectConfig{Only: []string{"go"}} }, want: graphCacheFilterMismatch},
		{name: "revision mismatch", mutate: func(state *State) (string, config.ProjectConfig) {
			state.Graph.BuilderRevision = "old"
			return root, cfg
		}, want: graphCacheRevisionMismatch},
		{name: "missing inventory fingerprint", mutate: func(state *State) (string, config.ProjectConfig) {
			state.Graph.InventoryFingerprint = ""
			return root, cfg
		}, want: graphCacheIncomplete},
		{name: "unavailable coverage", mutate: func(state *State) (string, config.ProjectConfig) {
			state.Coverage.Status = analysis.CoverageUnavailable
			return root, cfg
		}, want: graphCacheIncomplete},
		{name: "incomplete maps", mutate: func(state *State) (string, config.ProjectConfig) {
			state.Imports = map[string][]string{}
			state.Importers = map[string][]string{}
			return root, cfg
		}, want: "incomplete"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := base
			copyGraph := *base.Graph
			state.Graph = &copyGraph
			requestRoot, requestCfg := tt.mutate(&state)
			if graph, reason := validateCachedGraph(&state, requestRoot, requestCfg); graph != nil || reason != tt.want {
				t.Fatalf("validateCachedGraph() = %#v, %q; want nil, %q", graph, reason, tt.want)
			}
		})
	}
}

func TestConfiguredEventRefreshesDependencyGraph(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := testGraphStateDaemon(root)

	original := buildFileGraph
	t.Cleanup(func() { buildFileGraph = original })
	t.Cleanup(func() {
		close(d.done)
		if d.dependencyCancel != nil {
			d.dependencyCancel()
		}
		d.dependencyWorkerWG.Wait()
	})
	calls := 0
	buildFileGraph = func(context.Context, string, scanner.Filters) (*scanner.FileGraph, error) {
		calls++
		return &scanner.FileGraph{
			Imports:   map[string][]string{"main.go": {"dep.go"}},
			Importers: map[string][]string{"dep.go": {"main.go"}},
		}, nil
	}

	if !d.handleEvent(fsnotify.Event{Name: path, Op: fsnotify.Write}) {
		t.Fatal("configured write did not invalidate the graph")
	}
	if d.graph.GraphState.Status != graphLifecycleStale {
		t.Fatalf("after write graph state = %#v, want stale", d.graph.GraphState)
	}
	d.refreshDependencies()
	result := <-d.dependencyResults
	d.handleDependencyGraphResult(result)

	if calls != 1 {
		t.Fatalf("dependency builds = %d, want 1", calls)
	}
	if d.graph.GraphState.Status != graphLifecycleAvailable || !d.graph.HasDeps {
		t.Fatalf("after refresh graph state = %#v, has deps %t", d.graph.GraphState, d.graph.HasDeps)
	}
}

func TestDependencyRefreshBuildsOffEventLoop(t *testing.T) {
	root := t.TempDir()
	d := testGraphStateDaemon(root)
	started := make(chan struct{})
	release := make(chan struct{})
	original := buildFileGraph
	t.Cleanup(func() { buildFileGraph = original })
	t.Cleanup(func() {
		close(d.done)
		if d.dependencyCancel != nil {
			d.dependencyCancel()
		}
		d.dependencyWorkerWG.Wait()
	})
	buildFileGraph = func(context.Context, string, scanner.Filters) (*scanner.FileGraph, error) {
		close(started)
		<-release
		return &scanner.FileGraph{}, nil
	}

	d.markGraphLifecycle(graphLifecycleStale)
	finished := make(chan struct{})
	go func() {
		d.refreshDependencies()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("refreshDependencies blocked on graph construction")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("dependency worker did not start")
	}
	close(release)
}

func TestDependencyGraphBuildRecoversPanics(t *testing.T) {
	graph, err := buildDependencyGraph(context.Background(), t.TempDir(), func(context.Context, string, scanner.Filters) (*scanner.FileGraph, error) {
		panic("test panic")
	})
	if graph != nil || err == nil || err.Error() != "dependency graph build panicked: test panic" {
		t.Fatalf("panic result = graph=%#v err=%v", graph, err)
	}
}

func TestDependencyRefreshRejectsResultAfterInvalidation(t *testing.T) {
	root := t.TempDir()
	d := testGraphStateDaemon(root)
	started := make(chan struct{})
	release := make(chan struct{})
	original := buildFileGraph
	t.Cleanup(func() { buildFileGraph = original })
	t.Cleanup(func() {
		close(d.done)
		if d.dependencyCancel != nil {
			d.dependencyCancel()
		}
		d.dependencyWorkerWG.Wait()
	})
	buildFileGraph = func(context.Context, string, scanner.Filters) (*scanner.FileGraph, error) {
		close(started)
		<-release
		return &scanner.FileGraph{Importers: map[string][]string{"dep.go": {"main.go"}}}, nil
	}

	d.markGraphLifecycle(graphLifecycleStale)
	d.refreshDependencies()
	<-started
	d.markGraphLifecycle(graphLifecycleStale)
	close(release)
	d.handleDependencyGraphResult(<-d.dependencyResults)

	d.graph.mu.RLock()
	defer d.graph.mu.RUnlock()
	if d.graph.HasDeps || d.graph.FileGraph != nil || d.graph.GraphState.Status != graphLifecycleStale {
		t.Fatalf("stale worker result was applied: state=%#v hasDeps=%t graph=%#v", d.graph.GraphState, d.graph.HasDeps, d.graph.FileGraph)
	}
}

func TestDependencyRefreshCoalescesInvalidations(t *testing.T) {
	root := t.TempDir()
	d := testGraphStateDaemon(root)
	var calls atomic.Int32
	secondStarted := make(chan struct{})
	release := make(chan struct{})
	original := buildFileGraph
	t.Cleanup(func() { buildFileGraph = original })
	t.Cleanup(func() {
		close(d.done)
		if d.dependencyCancel != nil {
			d.dependencyCancel()
		}
		d.dependencyWorkerWG.Wait()
	})
	buildFileGraph = func(context.Context, string, scanner.Filters) (*scanner.FileGraph, error) {
		if calls.Add(1) == 2 {
			close(secondStarted)
		}
		if calls.Load() == 1 {
			<-release
			return nil, errors.New("first dependency build failed")
		}
		return &scanner.FileGraph{}, nil
	}

	d.markGraphLifecycle(graphLifecycleStale)
	d.refreshDependencies()
	d.markGraphLifecycle(graphLifecycleStale)
	d.refreshDependencies()
	close(release)
	d.handleDependencyGraphResult(<-d.dependencyResults)
	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		t.Fatal("pending invalidation did not schedule a second build")
	}
	d.handleDependencyGraphResult(<-d.dependencyResults)
	if got := calls.Load(); got != 2 {
		t.Fatalf("dependency builds = %d, want 2", got)
	}
}

func TestGraphFilterFingerprintIsDeterministic(t *testing.T) {
	left := config.ProjectConfig{Only: []string{"rust", "go"}, Exclude: []string{"vendor", "generated"}}
	right := config.ProjectConfig{Only: []string{"go", "rust"}, Exclude: []string{"generated", "vendor"}}
	if graphFilterFingerprint(left) != graphFilterFingerprint(right) {
		t.Fatal("filter order changed fingerprint")
	}
	if graphFilterFingerprint(left) == graphFilterFingerprint(config.ProjectConfig{Only: []string{"go"}}) {
		t.Fatal("filter change did not change fingerprint")
	}
}

func TestConfiguredInventoryFingerprintIsDeterministic(t *testing.T) {
	left := []string{"src/main.go", `pkg\types.go`, "src/main.go"}
	right := []string{"pkg/types.go", "src/main.go"}
	if ConfiguredInventoryFingerprint(left) != ConfiguredInventoryFingerprint(right) {
		t.Fatal("inventory order, separators, or duplicates changed fingerprint")
	}
	if ConfiguredInventoryFingerprint(left) == ConfiguredInventoryFingerprint([]string{"src/main.go", "pkg/new.go"}) {
		t.Fatal("same-count replacement did not change fingerprint")
	}
}

func TestGraphStateBuildTransitions(t *testing.T) {
	root := t.TempDir()
	d := testGraphStateDaemon(root)

	d.computeDepsWith(func(context.Context, string) (*scanner.FileGraph, error) { return nil, errors.New("scanner failed") })
	if d.graph.GraphState.Status != graphLifecycleFailed || d.graph.HasDeps {
		t.Fatalf("failed state = %#v, has deps %t", d.graph.GraphState, d.graph.HasDeps)
	}
	d.computeDepsWith(func(context.Context, string) (*scanner.FileGraph, error) {
		return &scanner.FileGraph{Coverage: scanner.GraphCoverage{Status: analysis.CoverageUnavailable}}, nil
	})
	if d.graph.GraphState.Status != graphLifecycleFailed || d.graph.HasDeps {
		t.Fatalf("empty graph state = %#v, has deps %t", d.graph.GraphState, d.graph.HasDeps)
	}

	d.computeDepsWith(func(context.Context, string) (*scanner.FileGraph, error) {
		return &scanner.FileGraph{Imports: map[string][]string{"main.go": {"dep.go"}}, Importers: map[string][]string{"dep.go": {"main.go"}}}, nil
	})
	if d.graph.GraphState.Status != graphLifecycleAvailable || !d.graph.HasDeps || d.graph.GraphState.CompletedAt.IsZero() {
		t.Fatalf("retry state = %#v, has deps %t", d.graph.GraphState, d.graph.HasDeps)
	}

	d.markGraphLifecycle(graphLifecycleSkippedSize)
	if d.graph.GraphState.Status != graphLifecycleSkippedSize || d.graph.HasDeps {
		t.Fatalf("skipped state = %#v, has deps %t", d.graph.GraphState, d.graph.HasDeps)
	}

	d.graph.mu.Lock()
	d.graph.ConfiguredFiles = map[string]struct{}{"main.go": {}, "dep.go": {}}
	d.graph.mu.Unlock()
	d.computeDepsWith(func(context.Context, string) (*scanner.FileGraph, error) {
		d.graph.mu.Lock()
		delete(d.graph.ConfiguredFiles, "dep.go")
		d.graph.ConfiguredFiles["replacement.go"] = struct{}{}
		d.graph.mu.Unlock()
		return &scanner.FileGraph{Imports: map[string][]string{"main.go": {"dep.go"}}, Importers: map[string][]string{"dep.go": {"main.go"}}}, nil
	})
	if d.graph.GraphState.Status != graphLifecycleStale || d.graph.HasDeps {
		t.Fatalf("inventory changed during build = %#v, has deps %t", d.graph.GraphState, d.graph.HasDeps)
	}
}

func TestGraphPublicationRejectsInterleavedInvalidators(t *testing.T) {
	graph := func(context.Context, string) (*scanner.FileGraph, error) {
		return &scanner.FileGraph{Imports: map[string][]string{"main.go": {"dep.go"}}, Importers: map[string][]string{"dep.go": {"main.go"}}}, nil
	}

	t.Run("same-path write generation", func(t *testing.T) {
		root := t.TempDir()
		d := testGraphStateDaemon(root)
		d.computeDepsWithBeforePublish(graph, func() {
			d.markGraphLifecycle(graphLifecycleStale)
		})
		if d.graph.GraphState.Status != graphLifecycleStale || d.graph.HasDeps {
			t.Fatalf("interleaved write = %#v, has deps %t", d.graph.GraphState, d.graph.HasDeps)
		}
	})

	t.Run("filter edit", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
			t.Fatal(err)
		}
		configPath := filepath.Join(root, ".codemap", "config.json")
		if err := os.WriteFile(configPath, []byte(`{"only":["go"]}`), 0o644); err != nil {
			t.Fatal(err)
		}
		d := testGraphStateDaemon(root)
		d.computeDepsWithBeforePublish(graph, func() {
			if err := os.WriteFile(configPath, []byte(`{"only":["rust"]}`), 0o644); err != nil {
				t.Fatal(err)
			}
		})
		if d.graph.GraphState.Status != graphLifecycleStale || d.graph.HasDeps {
			t.Fatalf("interleaved filter edit = %#v, has deps %t", d.graph.GraphState, d.graph.HasDeps)
		}
	})
}

func TestConfiguredEventInvalidatesGraph(t *testing.T) {
	for _, tt := range []struct {
		name string
		op   fsnotify.Op
	}{
		{name: "create", op: fsnotify.Create},
		{name: "write", op: fsnotify.Write},
		{name: "remove", op: fsnotify.Remove},
		{name: "rename", op: fsnotify.Rename},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "main.go")
			if tt.op == fsnotify.Create || tt.op == fsnotify.Write {
				if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			d := testGraphStateDaemon(root)
			d.eventLog = filepath.Join(root, ".codemap", "events.log")
			d.graph.ConfiguredFiles["main.go"] = struct{}{}

			d.handleEvent(fsnotify.Event{Name: path, Op: tt.op})

			if d.graph.GraphState.Status != graphLifecycleStale || d.graph.HasDeps {
				t.Fatalf("event left graph usable: %#v, has deps %t", d.graph.GraphState, d.graph.HasDeps)
			}
			state := ReadState(root)
			if state == nil || state.Graph == nil || state.Graph.Status != graphLifecycleStale {
				t.Fatalf("persisted state = %#v", state)
			}
		})
	}
}

func graphStatusMutation(root string, cfg config.ProjectConfig, status GraphLifecycle) func(*State) (string, config.ProjectConfig) {
	return func(state *State) (string, config.ProjectConfig) {
		state.Graph.Status = status
		return root, cfg
	}
}

func testGraphStateDaemon(root string) *Daemon {
	state := newGraphState(root, config.ProjectConfig{}, graphLifecycleAvailable, time.Now(), []string{"main.go"})
	return &Daemon{
		root: root,
		done: make(chan struct{}),
		graph: &Graph{
			Root:            root,
			Files:           map[string]*scanner.FileInfo{"main.go": {Path: "main.go", Ext: ".go"}},
			ConfiguredFiles: map[string]struct{}{"main.go": {}},
			FileGraph:       &scanner.FileGraph{Imports: map[string][]string{"main.go": {"dep.go"}}},
			DepCtx:          map[string]*DepContext{},
			State:           map[string]*FileState{"main.go": {Lines: 1}},
			WorkingSet:      NewWorkingSet(),
			HasDeps:         true,
			GraphState:      state,
		},
	}
}
