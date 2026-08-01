package watch

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codemap/internal/projectpath"
	"codemap/limits"
	"codemap/scanner"
	"codemap/topology"

	"github.com/fsnotify/fsnotify"
)

func TestDaemonTopologyFailurePreservesFileGraph(t *testing.T) {
	fileGraph := &scanner.FileGraph{
		Root:      "/repo",
		Imports:   map[string][]string{"main.go": {"dep.go"}},
		Importers: map[string][]string{"dep.go": {"main.go"}},
	}
	daemon := topologyTestDaemon(t)
	daemon.graph.FileGraph = fileGraph
	original := buildTopologyGraph
	buildTopologyGraph = func(context.Context, string) (*topology.Graph, topology.CacheIdentity, error) {
		return nil, topology.CacheIdentity{}, errors.New("bad manifest")
	}
	t.Cleanup(func() { buildTopologyGraph = original })

	daemon.computeTopology()

	if daemon.graph.FileGraph != fileGraph {
		t.Fatal("topology failure replaced the file graph")
	}
	if daemon.graph.Topology != nil {
		t.Fatalf("topology failure retained graph: %#v", daemon.graph.Topology)
	}
}

func TestLargeRepoStartupSkipsTopology(t *testing.T) {
	daemon := topologyTestDaemon(t)
	calls := installTopologyBuildCounter(t)

	daemon.computeInitialGraphs(limits.LargeRepoFileCount + 1)

	if *calls != 0 {
		t.Fatalf("large-repo startup built topology %d times", *calls)
	}
}

func TestManifestWriteRebuildsTopology(t *testing.T) {
	daemon := topologyTestDaemon(t)
	path := filepath.Join(daemon.root, "pom.xml")
	if err := os.WriteFile(path, []byte("<project />"), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := installTopologyBuildCounter(t)
	originalManifest := isTopologyManifest
	isTopologyManifest = func(string) bool { return true }
	t.Cleanup(func() { isTopologyManifest = originalManifest })

	daemon.handleEvent(fsnotify.Event{Name: path, Op: fsnotify.Write})

	if *calls != 1 {
		t.Fatalf("topology builds = %d, want 1", *calls)
	}
}

func TestOrdinarySourceWriteReusesTopology(t *testing.T) {
	daemon := topologyTestDaemon(t)
	path := filepath.Join(daemon.root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := installTopologyBuildCounter(t)

	daemon.handleEvent(fsnotify.Event{Name: path, Op: fsnotify.Write})

	if *calls != 0 {
		t.Fatalf("ordinary write rebuilt topology %d times", *calls)
	}
}

func TestSourceCreateAndRemoveRebuildMembership(t *testing.T) {
	daemon := topologyTestDaemon(t)
	path := filepath.Join(daemon.root, "new.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := installTopologyBuildCounter(t)

	daemon.handleEvent(fsnotify.Event{Name: path, Op: fsnotify.Create})
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	daemon.handleEvent(fsnotify.Event{Name: path, Op: fsnotify.Remove})

	if *calls != 2 {
		t.Fatalf("topology builds = %d, want create and remove rebuilds", *calls)
	}
}

func TestTopologyConfigEventRebuildsWithoutIngestingStateWrites(t *testing.T) {
	daemon := topologyTestDaemon(t)
	calls := installTopologyBuildCounter(t)

	configPath := filepath.Join(daemon.root, ".codemap", "config.json")
	if handled := daemon.handleTopologyControlEvent(fsnotify.Event{Name: configPath, Op: fsnotify.Write}); !handled {
		t.Fatal("config event was not handled as topology control")
	}
	statePath := filepath.Join(daemon.root, ".codemap", "state.json")
	if handled := daemon.handleTopologyControlEvent(fsnotify.Event{Name: statePath, Op: fsnotify.Write}); handled {
		t.Fatal("state write was treated as topology control")
	}
	if *calls != 1 {
		t.Fatalf("topology builds = %d, want one config rebuild", *calls)
	}
	if len(daemon.graph.Events) != 0 {
		t.Fatalf("control event leaked into file events: %#v", daemon.graph.Events)
	}
}

func TestTopologyConfigEventUsesSelectedSetupDirectory(t *testing.T) {
	root, setup := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	projectpath.SetSetupRoot(setup)
	t.Cleanup(projectpath.ResetSetupRoot)

	daemon, err := NewDaemon(root, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = daemon.watcher.Close() })
	calls := installTopologyBuildCounter(t)
	configPath := filepath.Join(setup, ".codemap", "config.json")
	if _, handled := daemon.filterControlEvent(configPath); !handled {
		t.Fatal("setup-root config event was not classified as control")
	}
	if handled := daemon.handleTopologyControlEvent(fsnotify.Event{Name: configPath, Op: fsnotify.Write}); !handled {
		t.Fatal("setup-root config event was not handled as topology control")
	}
	if *calls != 1 {
		t.Fatalf("topology builds = %d, want one setup-root config rebuild", *calls)
	}
}

func TestTopologyCacheUsesDaemonRuntimeDirectory(t *testing.T) {
	daemon := topologyTestDaemon(t)
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	daemon.runtimeDir = runtimeDir
	originalBuild := buildTopologyGraph
	originalWrite := writeTopologyCache
	buildTopologyGraph = func(context.Context, string) (*topology.Graph, topology.CacheIdentity, error) {
		return &topology.Graph{
			Nodes:        map[topology.ID]topology.Node{},
			Dependencies: map[topology.ID][]topology.Edge{},
			Dependents:   map[topology.ID][]topology.Edge{},
			Members:      map[topology.ID][]string{},
			Owners:       map[string][]topology.ID{},
			Coverage:     topology.Coverage{Status: topology.CoverageComplete},
		}, topology.CacheIdentity{Filters: "runtime"}, nil
	}
	writeTopologyCache = topology.WriteCacheAt
	t.Cleanup(func() {
		buildTopologyGraph = originalBuild
		writeTopologyCache = originalWrite
	})

	daemon.computeTopology()

	if _, err := os.Stat(topology.CachePathAt(runtimeDir)); err != nil {
		t.Fatalf("runtime topology cache missing: %v", err)
	}
	if _, err := os.Stat(topology.CachePath(daemon.root)); !os.IsNotExist(err) {
		t.Fatalf("project-local topology cache = %v, want no cache outside runtime", err)
	}
}

func TestTopologyCacheNeverChangesLegacyStateShape(t *testing.T) {
	daemon := topologyTestDaemon(t)
	node := topology.Node{ID: "jvm:pom.xml:app", Name: "app"}
	daemon.graph.Topology = &topology.Graph{
		Nodes:        map[topology.ID]topology.Node{node.ID: node},
		Dependencies: map[topology.ID][]topology.Edge{},
		Dependents:   map[topology.ID][]topology.Edge{},
		Members:      map[topology.ID][]string{node.ID: {"main.go"}},
		Owners:       map[string][]topology.ID{"main.go": {node.ID}},
		Coverage:     topology.Coverage{Status: topology.CoverageComplete},
	}
	daemon.writeState()

	data, err := os.ReadFile(daemon.publisher.path)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"topology", "modules", "owners", "affected_modules"} {
		if _, ok := state[forbidden]; ok {
			t.Fatalf("legacy state gained %q: %s", forbidden, data)
		}
	}
}

func TestDaemonDoesNotCacheTransientProviderFailure(t *testing.T) {
	daemon := topologyTestDaemon(t)
	originalBuild := buildTopologyGraph
	originalWrite := writeTopologyCache
	writes := 0
	buildTopologyGraph = func(context.Context, string) (*topology.Graph, topology.CacheIdentity, error) {
		return &topology.Graph{
			Nodes:        map[topology.ID]topology.Node{},
			Dependencies: map[topology.ID][]topology.Edge{},
			Dependents:   map[topology.ID][]topology.Edge{},
			Members:      map[topology.ID][]string{},
			Owners:       map[string][]topology.ID{},
			Coverage: topology.Coverage{
				Status: topology.CoveragePartial,
				Issues: []topology.Issue{{Provider: "jvm", Code: "provider-failed", Message: "temporary failure"}},
			},
		}, topology.CacheIdentity{Filters: "test"}, nil
	}
	writeTopologyCache = func(string, topology.CacheEnvelope) error {
		writes++
		return nil
	}
	t.Cleanup(func() {
		buildTopologyGraph = originalBuild
		writeTopologyCache = originalWrite
	})

	daemon.computeTopology()

	if writes != 0 {
		t.Fatalf("transient provider failure wrote %d cache entries", writes)
	}
}

func TestOldWatchStateLoadsWithoutTopology(t *testing.T) {
	root := t.TempDir()
	stateDir := projectpath.ProjectRuntimeDir(root)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"updated_at":"` + time.Now().UTC().Format(time.RFC3339Nano) + `","file_count":1,"hubs":["main.go"],"importers":{},"imports":{},"recent_events":[]}`
	if err := os.WriteFile(filepath.Join(stateDir, "state.json"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	state := ReadState(root)
	if state == nil || state.FileCount != 1 || len(state.Hubs) != 1 {
		t.Fatalf("legacy state = %#v", state)
	}
}

func topologyTestDaemon(t *testing.T) *Daemon {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = watcher.Close() })
	return &Daemon{
		root:       root,
		configDir:  filepath.Join(root, ".codemap"),
		runtimeDir: filepath.Join(root, ".codemap"),
		watcher:    watcher,
		gitCache:   scanner.NewGitIgnoreCache(root),
		eventLog:   filepath.Join(root, ".codemap", "events.log"),
		graph: &Graph{
			Root:       root,
			Files:      make(map[string]*scanner.FileInfo),
			DepCtx:     make(map[string]*DepContext),
			State:      make(map[string]*FileState),
			Events:     make([]Event, 0),
			WorkingSet: NewWorkingSet(),
		},
	}
}

func installTopologyBuildCounter(t *testing.T) *int {
	t.Helper()
	calls := 0
	originalBuild := buildTopologyGraph
	originalWrite := writeTopologyCache
	buildTopologyGraph = func(context.Context, string) (*topology.Graph, topology.CacheIdentity, error) {
		calls++
		return &topology.Graph{
			Nodes:        map[topology.ID]topology.Node{},
			Dependencies: map[topology.ID][]topology.Edge{},
			Dependents:   map[topology.ID][]topology.Edge{},
			Members:      map[topology.ID][]string{},
			Owners:       map[string][]topology.ID{},
			Coverage:     topology.Coverage{Status: topology.CoverageComplete},
		}, topology.CacheIdentity{Filters: "test"}, nil
	}
	writeTopologyCache = func(string, topology.CacheEnvelope) error { return nil }
	t.Cleanup(func() {
		buildTopologyGraph = originalBuild
		writeTopologyCache = originalWrite
	})
	return &calls
}
