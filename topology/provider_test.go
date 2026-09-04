package topology

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"codemap/config"
	"codemap/internal/projectpath"
	"codemap/scanner"
)

type stubProvider struct {
	name      string
	version   string
	languages []string
	manifests []string
	build     func(context.Context, Inventory) (Fragment, error)
}

type cancelAfterTopologyChecks struct {
	context.Context
	remaining int
}

func (c *cancelAfterTopologyChecks) Err() error {
	if c.remaining <= 0 {
		return context.Canceled
	}
	c.remaining--
	return nil
}

func (p stubProvider) Name() string        { return p.name }
func (p stubProvider) Version() string     { return p.version }
func (p stubProvider) Languages() []string { return append([]string(nil), p.languages...) }
func (p stubProvider) Manifests() ManifestSelector {
	return ManifestSelector{Names: append([]string(nil), p.manifests...)}
}
func (p stubProvider) Build(ctx context.Context, inventory Inventory) (Fragment, error) {
	if p.build == nil {
		return Fragment{Provider: p.name, Coverage: Coverage{Status: CoverageComplete}}, nil
	}
	return p.build(ctx, inventory)
}

func TestBuildProjectGraphPreservesEveryFileGraphField(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "go.mod", "module example.com/topology\n\ngo 1.24.0\n")
	writeTopologyFixture(t, root, "dep/dep.go", "package dep\n\nfunc Value() int { return 1 }\n")
	writeTopologyFixture(t, root, "main.go", "package main\n\nimport \"example.com/topology/dep\"\n\nfunc main() { _ = dep.Value() }\n")

	direct, err := buildFileGraph(root)
	if err != nil {
		t.Fatal(err)
	}
	project, err := BuildProjectGraphWithProviders(context.Background(), root, []Provider{stubProvider{
		name:      "empty",
		version:   "1",
		languages: []string{"go"},
		manifests: []string{"go.mod"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(project.Files, direct) {
		t.Fatalf("file graph changed after topology construction:\ndirect:  %#v\nproject: %#v", direct, project.Files)
	}
}

func TestBuildGraphUsesProjectRuntimeCache(t *testing.T) {
	root, setup := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTopologyFixture(t, root, "main.go", "package main\n")
	projectpath.SetSetupRoot(setup)
	t.Cleanup(projectpath.ResetSetupRoot)

	calls := 0
	provider := stubProvider{
		name:      "cache",
		version:   "1",
		languages: []string{"go"},
		build: func(context.Context, Inventory) (Fragment, error) {
			calls++
			return Fragment{Provider: "cache", Coverage: Coverage{Status: CoverageComplete}}, nil
		},
	}
	first, identity, err := BuildGraphWithProviders(context.Background(), root, []Provider{provider})
	if err != nil {
		t.Fatal(err)
	}
	runtimeDir, err := projectpath.CheckedRuntimeCodemapDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteCacheAt(runtimeDir, CacheEnvelope{
		Schema:      CacheSchemaVersion,
		GeneratedAt: time.Now().UTC(),
		Identity:    identity,
		Graph:       first,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(CachePathAt(runtimeDir)); err != nil {
		t.Fatalf("runtime topology cache missing: %v", err)
	}
	if _, err := os.Stat(CachePath(root)); !os.IsNotExist(err) {
		t.Fatalf("project-local topology cache = %v, want no legacy cache", err)
	}
	if _, _, err := BuildGraphWithProviders(context.Background(), root, []Provider{provider}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("provider calls = %d, want one build followed by a runtime-cache hit", calls)
	}
}

func TestBuildProjectGraphIsolatesProviderFailures(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "main.go", "package main\n")
	goodNode := testNode("good:settings.gradle.kts:app", "app")
	good := stubProvider{
		name:      "good",
		version:   "1",
		languages: []string{"go"},
		build: func(context.Context, Inventory) (Fragment, error) {
			return Fragment{
				Provider: "good",
				Nodes:    []Node{goodNode},
				Members:  map[ID][]string{goodNode.ID: {"main.go"}},
				Coverage: Coverage{Status: CoverageComplete},
			}, nil
		},
	}
	bad := stubProvider{
		name:      "bad",
		version:   "1",
		languages: []string{"go"},
		build: func(context.Context, Inventory) (Fragment, error) {
			return Fragment{}, errors.New("malformed manifest")
		},
	}

	project, err := BuildProjectGraphWithProviders(context.Background(), root, []Provider{good, bad})
	if err != nil {
		t.Fatal(err)
	}
	if project.Files == nil {
		t.Fatal("provider failure removed the file graph")
	}
	if _, ok := project.Topology.Nodes[goodNode.ID]; !ok {
		t.Fatalf("successful provider node missing: %#v", project.Topology)
	}
	if project.Topology.Coverage.Status != CoveragePartial {
		t.Fatalf("coverage = %q, want partial", project.Topology.Coverage.Status)
	}
	if !hasIssueCode(project.Topology.Coverage.Issues, "provider-failed") {
		t.Fatalf("coverage issues = %#v, want provider-failed", project.Topology.Coverage.Issues)
	}
}

func TestBuildProjectGraphPropagatesCancellation(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "main.go", "package main\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	provider := stubProvider{
		name:      "cancel",
		version:   "1",
		languages: []string{"go"},
		build: func(context.Context, Inventory) (Fragment, error) {
			t.Fatal("provider should not run after cancellation")
			return Fragment{}, nil
		},
	}

	project, err := BuildProjectGraphWithProviders(ctx, root, []Provider{provider})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if project == nil || project.Files == nil {
		t.Fatal("cancellation after file scan should return the successful file graph")
	}
}

func TestBuildGraphPropagatesInventoryCancellation(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "main.go", "package main\n")
	ctx := &cancelAfterTopologyChecks{Context: context.Background(), remaining: 1}

	_, _, err := BuildGraphWithProviders(ctx, root, []Provider{stubProvider{
		name:      "cancel",
		version:   "1",
		languages: []string{"go"},
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestBuildProjectGraphGatesProvidersByOnlyLanguages(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, ".codemap/config.json", `{"only":["go"]}`)
	writeTopologyFixture(t, root, "main.go", "package main\n")
	calls := 0
	provider := stubProvider{
		name:      "swiftpm",
		version:   "1",
		languages: []string{"swift"},
		manifests: []string{"Package.swift"},
		build: func(context.Context, Inventory) (Fragment, error) {
			calls++
			return Fragment{}, nil
		},
	}

	project, err := BuildProjectGraphWithProviders(context.Background(), root, []Provider{provider})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("swift provider calls = %d, want zero", calls)
	}
	if project.Topology.Coverage.Status != CoverageUnavailable {
		t.Fatalf("coverage = %q, want unavailable", project.Topology.Coverage.Status)
	}
}

func TestDiscoverManifestsHonorsGitignoreAndExclude(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, ".gitignore", "ignored/\n")
	writeTopologyFixture(t, root, ".codemap/config.json", `{"exclude":["excluded"]}`)
	writeTopologyFixture(t, root, "pom.xml", "<project />")
	writeTopologyFixture(t, root, "nested/pom.xml", "<project />")
	writeTopologyFixture(t, root, "ignored/pom.xml", "<project />")
	writeTopologyFixture(t, root, "excluded/pom.xml", "<project />")

	got, err := discoverManifests(context.Background(), root, []Provider{stubProvider{
		name:      "jvm",
		version:   "1",
		languages: []string{"java"},
		manifests: []string{"pom.xml"},
	}}, config.Load(root))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"nested/pom.xml", "pom.xml"}
	for i := range want {
		want[i] = filepath.FromSlash(want[i])
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("manifests = %#v, want %#v", got, want)
	}
}

func TestDiscoverInventoryFindsManifestsBelowSourceIgnoredDirectories(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "Main.java", "class Main {}\n")
	writeTopologyFixture(t, root, "main.go", "package main\n")
	writeTopologyFixture(t, root, "pom.xml", "<project />")
	writeTopologyFixture(t, root, "vendor/pom.xml", "<project />")
	writeTopologyFixture(t, root, "vendor/generated.go", "package generated\n")
	writeTopologyFixture(t, root, "vendor/.gitignore", "ignored/\n")
	writeTopologyFixture(t, root, "vendor/ignored/pom.xml", "<project />")
	writeTopologyFixture(t, root, "build/pom.xml", "<project />")
	writeTopologyFixture(t, root, "build/generated.go", "package generated\n")

	files, manifests, err := discoverInventory(context.Background(), root, []Provider{stubProvider{
		name:      "jvm",
		version:   "1",
		languages: []string{"java"},
		manifests: []string{"pom.xml"},
	}}, config.ProjectConfig{}, true)
	if err != nil {
		t.Fatal(err)
	}
	wantFiles := []string{"Main.java"}
	gotFiles := make([]string, len(files))
	for i, file := range files {
		gotFiles[i] = filepath.ToSlash(file.Path)
	}
	if !reflect.DeepEqual(gotFiles, wantFiles) {
		t.Fatalf("files = %#v, want %#v", gotFiles, wantFiles)
	}
	wantManifests := []string{"build/pom.xml", "pom.xml", "vendor/pom.xml"}
	for i := range manifests {
		manifests[i] = filepath.ToSlash(manifests[i])
	}
	if !reflect.DeepEqual(manifests, wantManifests) {
		t.Fatalf("manifests = %#v, want %#v", manifests, wantManifests)
	}

	filteredFiles, filteredManifests, err := discoverInventory(context.Background(), root, []Provider{stubProvider{
		name: "jvm", version: "1", languages: []string{"java"}, manifests: []string{"pom.xml"},
	}}, config.ProjectConfig{Only: []string{"java"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(filteredFiles) != 1 || filepath.ToSlash(filteredFiles[0].Path) != "Main.java" || !reflect.DeepEqual(filteredManifests, manifests) {
		t.Fatalf("filtered inventory = (%#v, %#v), want the Java file and unchanged manifests", filteredFiles, filteredManifests)
	}
}

func TestDiscoverManifestsStopsOnCancellation(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "pom.xml", "<project />")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := discoverManifests(ctx, root, []Provider{stubProvider{
		name:      "jvm",
		version:   "1",
		languages: []string{"java"},
		manifests: []string{"pom.xml"},
	}}, config.ProjectConfig{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestDiscoverInventoryReportsManifestWalkLimit(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "pom.xml", "<project />")

	_, _, err := discoverInventoryWithLimit(context.Background(), root, []Provider{stubProvider{
		name: "jvm", version: "1", languages: []string{"java"}, manifests: []string{"pom.xml"},
	}}, config.ProjectConfig{}, false, 1)
	if !errors.Is(err, errManifestWalkLimit) {
		t.Fatalf("error = %v, want manifest walk limit", err)
	}
}

func TestRegisteredProvidersAreNameSorted(t *testing.T) {
	providerRegistryMu.Lock()
	original := append([]Provider(nil), providerRegistry...)
	providerRegistry = nil
	providerRegistryMu.Unlock()
	t.Cleanup(func() {
		providerRegistryMu.Lock()
		providerRegistry = original
		providerRegistryMu.Unlock()
	})

	RegisterProvider(stubProvider{name: "zeta", version: "1"})
	RegisterProvider(stubProvider{name: "alpha", version: "1"})
	got := RegisteredProviders()
	if len(got) != 2 || got[0].Name() != "alpha" || got[1].Name() != "zeta" {
		t.Fatalf("registered providers = %#v", providerNames(got))
	}

	got[0] = stubProvider{name: "mutated", version: "1"}
	again := RegisteredProviders()
	if again[0].Name() != "alpha" {
		t.Fatalf("registered provider snapshot was mutable: %#v", providerNames(again))
	}
}

func buildFileGraph(root string) (*scanner.FileGraph, error) {
	return scanner.BuildFileGraph(context.Background(), root, scanner.ConfiguredFilters(root))
}

func writeTopologyFixture(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func hasIssueCode(issues []Issue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func providerNames(providers []Provider) []string {
	names := make([]string, len(providers))
	for i, provider := range providers {
		names[i] = provider.Name()
	}
	return names
}
