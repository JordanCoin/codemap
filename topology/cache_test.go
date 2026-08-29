package topology

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"codemap/internal/projectpath"
	"codemap/scanner"
)

func TestReadCacheAcceptsExactIdentity(t *testing.T) {
	root := t.TempDir()
	graph := cachedTestGraph(root)
	identity := CacheIdentity{
		Filters:          "filters",
		Manifests:        "manifests",
		ConfiguredFiles:  "files",
		ProviderVersions: "providers",
	}
	if err := WriteCache(root, CacheEnvelope{
		Schema:      CacheSchemaVersion,
		GeneratedAt: time.Unix(10, 0).UTC(),
		Identity:    identity,
		Graph:       graph,
	}); err != nil {
		t.Fatal(err)
	}

	got, ok := ReadCache(root, identity)
	if !ok {
		t.Fatal("exact cache identity was not accepted")
	}
	if !reflect.DeepEqual(got, graph) {
		t.Fatalf("cached graph = %#v, want %#v", got, graph)
	}
}

func TestReadCacheMissesMalformedStaleAndNewerSchemas(t *testing.T) {
	identity := CacheIdentity{Filters: "expected"}
	tests := []struct {
		name string
		data string
	}{
		{name: "malformed", data: `{"schema":`},
		{name: "stale", data: `{"schema":1,"identity":{"filters":"old"},"graph":{"nodes":{}}}`},
		{name: "newer", data: `{"schema":2,"identity":{"filters":"expected"},"graph":{"nodes":{}}}`},
		{name: "missing graph", data: `{"schema":1,"identity":{"filters":"expected"}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeTopologyFixture(t, root, ".codemap/topology-state.json", tt.data)
			before := readFixture(t, CachePath(root))

			if graph, ok := ReadCache(root, identity); ok || graph != nil {
				t.Fatalf("cache hit = %v graph = %#v, want miss", ok, graph)
			}
			after := readFixture(t, CachePath(root))
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("cache read rewrote fixture: before %q after %q", before, after)
			}
		})
	}
}

func TestReadCacheDoesNotTouchFileState(t *testing.T) {
	root := t.TempDir()
	fileState := []byte(`{"updated_at":"legacy","hubs":["main.go"]}`)
	writeTopologyFixture(t, root, ".codemap/state.json", string(fileState))
	graph := cachedTestGraph(root)
	identity := CacheIdentity{Filters: "filters"}

	if err := WriteCache(root, CacheEnvelope{
		Schema:      CacheSchemaVersion,
		GeneratedAt: time.Unix(10, 0).UTC(),
		Identity:    identity,
		Graph:       graph,
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadCache(root, identity); !ok {
		t.Fatal("expected topology cache hit")
	}
	if got := readFixture(t, filepath.Join(root, ".codemap", "state.json")); !reflect.DeepEqual(got, fileState) {
		t.Fatalf("legacy state changed: got %q want %q", got, fileState)
	}
}

func TestReadCacheMissesTransientProviderFailures(t *testing.T) {
	root := t.TempDir()
	graph := cachedTestGraph(root)
	graph.Coverage.Status = CoveragePartial
	graph.Coverage.Issues = []Issue{{Provider: "jvm", Code: "provider-failed", Message: "temporary read failure"}}
	identity := CacheIdentity{Filters: "filters"}
	if err := WriteCache(root, CacheEnvelope{
		Schema:      CacheSchemaVersion,
		GeneratedAt: time.Now().UTC(),
		Identity:    identity,
		Graph:       graph,
	}); err != nil {
		t.Fatal(err)
	}

	if cached, ok := ReadCache(root, identity); ok || cached != nil {
		t.Fatalf("transient provider failure was accepted from cache: %#v", cached)
	}
}

func TestWriteCacheAtomicallyReplacesTopologyState(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, ".codemap/topology-state.json", `{"schema":1,"old":true}`)
	graph := cachedTestGraph(root)
	identity := CacheIdentity{Filters: "new"}

	if err := WriteCache(root, CacheEnvelope{
		Schema:      CacheSchemaVersion,
		GeneratedAt: time.Unix(20, 0).UTC(),
		Identity:    identity,
		Graph:       graph,
	}); err != nil {
		t.Fatal(err)
	}

	data := readFixture(t, CachePath(root))
	var envelope CacheEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("written cache is invalid JSON: %v", err)
	}
	if envelope.Identity != identity || envelope.Graph == nil {
		t.Fatalf("written envelope = %#v", envelope)
	}
	matches, err := filepath.Glob(filepath.Join(root, ".codemap", ".topology-state-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary cache files remain: %#v", matches)
	}
}

func TestCacheIdentityChangesForFiltersManifestsFilesAndProviderVersions(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, ".codemap/config.json", `{"only":["go"],"exclude":["generated"]}`)
	writeTopologyFixture(t, root, "go.mod", "module example.com/cache\n")
	files := []scanner.FileInfo{{Path: "main.go"}}
	providers := []Provider{stubProvider{name: "go", version: "1"}}

	baseline, err := BuildCacheIdentity(root, files, []string{"go.mod"}, providers)
	if err != nil {
		t.Fatal(err)
	}

	writeTopologyFixture(t, root, ".codemap/config.json", `{"only":["go"],"exclude":["vendor"]}`)
	filtered, err := BuildCacheIdentity(root, files, []string{"go.mod"}, providers)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.Filters == filtered.Filters {
		t.Fatal("filter change did not change identity")
	}

	writeTopologyFixture(t, root, ".codemap/config.json", `{"only":["go"],"exclude":["generated"]}`)
	writeTopologyFixture(t, root, "go.mod", "module example.com/changed\n")
	manifest, err := BuildCacheIdentity(root, files, []string{"go.mod"}, providers)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.Manifests == manifest.Manifests {
		t.Fatal("manifest change did not change identity")
	}

	writeTopologyFixture(t, root, "go.mod", "module example.com/cache\n")
	fileSet, err := BuildCacheIdentity(root, append(files, scanner.FileInfo{Path: "other.go"}), []string{"go.mod"}, providers)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.ConfiguredFiles == fileSet.ConfiguredFiles {
		t.Fatal("configured file-set change did not change identity")
	}

	metadata, err := BuildCacheIdentity(root, []scanner.FileInfo{{Path: "main.go", Size: 42, Ext: ".go"}}, []string{"go.mod"}, providers)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.ConfiguredFiles == metadata.ConfiguredFiles {
		t.Fatal("configured file metadata change did not change identity")
	}

	writeTopologyFixture(t, root, ".codemap/config.json", `{"only":["go"],"exclude":["generated"],"depth":2}`)
	configChanged, err := BuildCacheIdentity(root, files, []string{"go.mod"}, providers)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.Filters == configChanged.Filters {
		t.Fatal("provider-visible config change did not change identity")
	}

	version, err := BuildCacheIdentity(root, files, []string{"go.mod"}, []Provider{stubProvider{name: "go", version: "2"}})
	if err != nil {
		t.Fatal(err)
	}
	if baseline.ProviderVersions == version.ProviderVersions {
		t.Fatal("provider-version change did not change identity")
	}
}

func TestBuildProjectGraphReadsOnlyAnExactCacheIdentity(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "main.go", "package main\n")
	writeTopologyFixture(t, root, "go.mod", "module example.com/cache\n")
	node := testNode("cache:go.mod:app", "app")
	node.Manifest = "go.mod"
	node.Root = "."
	calls := 0
	provider := stubProvider{
		name:      "cache",
		version:   "1",
		languages: []string{"go"},
		manifests: []string{"go.mod"},
		build: func(context.Context, Inventory) (Fragment, error) {
			calls++
			return Fragment{
				Provider: "cache",
				Nodes:    []Node{node},
				Members:  map[ID][]string{node.ID: {"main.go"}},
				Coverage: Coverage{Status: CoverageComplete},
			}, nil
		},
	}

	first, err := BuildProjectGraphWithProviders(context.Background(), root, []Provider{provider})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("provider calls = %d, want 1", calls)
	}
	runtimeDir, err := projectpath.CheckedRuntimeCodemapDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteCacheAt(runtimeDir, CacheEnvelope{
		Schema:      CacheSchemaVersion,
		GeneratedAt: time.Now().UTC(),
		Identity:    first.TopologyIdentity,
		Graph:       first.Topology,
	}); err != nil {
		t.Fatal(err)
	}
	second, err := BuildProjectGraphWithProviders(context.Background(), root, []Provider{provider})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("exact cache hit called provider; calls = %d", calls)
	}
	if !reflect.DeepEqual(second.Topology, first.Topology) {
		t.Fatalf("cache graph = %#v, want %#v", second.Topology, first.Topology)
	}

	writeTopologyFixture(t, root, "other.go", "package main\n")
	if _, err := BuildProjectGraphWithProviders(context.Background(), root, []Provider{provider}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("file-set cache miss calls = %d, want 2", calls)
	}
}

func TestBuildGraphRetriesAfterTransientProviderFailure(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "main.go", "package main\n")
	writeTopologyFixture(t, root, "go.mod", "module example.com/retry\n")
	node := testNode("retry:go.mod:app", "app")
	node.Manifest = "go.mod"
	node.Root = "."
	calls := 0
	provider := stubProvider{
		name:      "retry",
		version:   "1",
		languages: []string{"go"},
		manifests: []string{"go.mod"},
		build: func(context.Context, Inventory) (Fragment, error) {
			calls++
			if calls == 1 {
				return Fragment{}, errors.New("temporary manifest read failure")
			}
			return Fragment{
				Provider: "retry",
				Nodes:    []Node{node},
				Members:  map[ID][]string{node.ID: {"main.go"}},
				Coverage: Coverage{Status: CoverageComplete},
			}, nil
		},
	}

	first, identity, err := BuildGraphWithProviders(context.Background(), root, []Provider{provider})
	if err != nil {
		t.Fatal(err)
	}
	if first.Coverage.Status != CoverageUnavailable && first.Coverage.Status != CoveragePartial {
		t.Fatalf("first coverage = %q", first.Coverage.Status)
	}
	if err := WriteCache(root, CacheEnvelope{
		Schema:      CacheSchemaVersion,
		GeneratedAt: time.Now().UTC(),
		Identity:    identity,
		Graph:       first,
	}); err != nil {
		t.Fatal(err)
	}

	second, _, err := BuildGraphWithProviders(context.Background(), root, []Provider{provider})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("provider calls = %d, want retry after transient failure", calls)
	}
	if second.Coverage.Status != CoverageComplete {
		t.Fatalf("retry coverage = %q, want complete", second.Coverage.Status)
	}
}

func cachedTestGraph(root string) *Graph {
	node := testNode("cache:settings.gradle.kts:app", "app")
	return MergeFragments(root, []Fragment{{
		Provider: "cache",
		Nodes:    []Node{node},
		Members:  map[ID][]string{node.ID: {"modules/app/src/Main.kt"}},
		Coverage: Coverage{Status: CoverageComplete},
	}})
}

func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
