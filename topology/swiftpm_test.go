package topology

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"codemap/scanner"
)

func TestSwiftPMBuildsTargetsMembershipAndDependencies(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "Package.swift", `
import PackageDescription
let package = Package(
    name: "Demo",
    products: [
        .library(name: "CoreBundle", targets: ["Core", "Support"]),
        .executable(name: "demo", targets: ["App"]),
    ],
    targets: [
        .target(name: "Core", dependencies: ["Support"], exclude: ["Ignored.swift"]),
        .target(name: "Support", path: "Sources/Shared", sources: ["Support.swift"]),
        .executableTarget(
            name: "App",
            dependencies: [
                .target(name: "Core", condition: .when(platforms: [.macOS])),
                .product(name: "Remote", package: "remote-package"),
            ]
        ),
        .testTarget(name: "CoreTests", dependencies: [.product(name: "CoreBundle")]),
        .macro(name: "DemoMacros"),
        .plugin(name: "DemoPlugin", capability: .buildTool()),
        .systemLibrary(name: "CLibrary"),
        .binaryTarget(name: "BinaryKit", path: "Binaries/BinaryKit.xcframework"),
    ]
)`)
	files := []scanner.FileInfo{
		{Path: filepath.FromSlash("Sources/Core/Core.swift"), Ext: ".swift"},
		{Path: filepath.FromSlash("Sources/Core/Ignored.swift"), Ext: ".swift"},
		{Path: filepath.FromSlash("Sources/Shared/Support.swift"), Ext: ".swift"},
		{Path: filepath.FromSlash("Sources/Shared/Unlisted.swift"), Ext: ".swift"},
		{Path: filepath.FromSlash("Sources/App/main.swift"), Ext: ".swift"},
		{Path: filepath.FromSlash("Tests/CoreTests/CoreTests.swift"), Ext: ".swift"},
		{Path: filepath.FromSlash("Sources/DemoMacros/Macro.swift"), Ext: ".swift"},
		{Path: filepath.FromSlash("Plugins/DemoPlugin/plugin.swift"), Ext: ".swift"},
		{Path: filepath.FromSlash("Sources/CLibrary/include/library.h"), Ext: ".h"},
		{Path: filepath.FromSlash("Binaries/BinaryKit.xcframework/Info.plist"), Ext: ".plist"},
	}
	for _, file := range files {
		writeTopologyFixture(t, root, filepath.ToSlash(file.Path), "// fixture\n")
	}

	fragment, err := (swiftPMProvider{}).Build(context.Background(), Inventory{
		Root: root, Files: files, Manifests: []string{"Package.swift"},
	})
	if err != nil {
		t.Fatal(err)
	}
	graph := MergeFragments(root, []Fragment{fragment})
	if graph.Coverage.Status != CoverageComplete {
		t.Fatalf("coverage = %q: %#v", graph.Coverage.Status, graph.Coverage.Issues)
	}
	for name, kind := range map[string]NodeKind{
		"Core": "swift-target", "Support": "swift-target", "App": "swift-executable",
		"CoreTests": "swift-test", "DemoMacros": "swift-macro", "DemoPlugin": "swift-plugin",
		"CLibrary": "swift-system-library", "BinaryKit": "swift-binary",
	} {
		id := swiftPMID("Package.swift", name)
		if node, ok := graph.Nodes[id]; !ok || node.Kind != kind {
			t.Fatalf("node %s = %#v", id, node)
		}
	}
	coreID := swiftPMID("Package.swift", "Core")
	supportID := swiftPMID("Package.swift", "Support")
	appID := swiftPMID("Package.swift", "App")
	testsID := swiftPMID("Package.swift", "CoreTests")
	assertSwiftPMEdge(t, graph.Dependencies[coreID], supportID, EdgeDependency, "byName")
	edge := findTopologyEdge(graph.Dependencies[appID], coreID, "target")
	if edge == nil || !edge.Conditional {
		t.Fatalf("conditional target edge = %#v", edge)
	}
	assertSwiftPMEdge(t, graph.Dependencies[testsID], coreID, EdgeDependency, "product")
	assertSwiftPMEdge(t, graph.Dependencies[testsID], supportID, EdgeDependency, "product")
	if got := graph.Members[coreID]; !reflect.DeepEqual(got, []string{filepath.FromSlash("Sources/Core/Core.swift")}) {
		t.Fatalf("Core members = %#v", got)
	}
	if got := graph.Members[supportID]; !reflect.DeepEqual(got, []string{filepath.FromSlash("Sources/Shared/Support.swift")}) {
		t.Fatalf("Support members = %#v", got)
	}
}

func TestSwiftPMFailsClosedForAmbiguousComputedAndEscapingDeclarations(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "Package.swift", `
import PackageDescription
let computed = "Computed"
let package = Package(
    name: "Demo",
    products: [
        .library(name: "Duplicate", targets: ["One"]),
        .library(name: "Duplicate", targets: ["Two"]),
    ],
    targets: [
        .target(name: "One"),
        .target(name: "Two"),
        .target(name: "App", dependencies: [.product(name: "Duplicate")]),
        .target(name: computed),
        .target(name: "Outside", path: "../outside"),
    ]
)`)
	fragment, err := (swiftPMProvider{}).Build(context.Background(), Inventory{
		Root: root, Manifests: []string{"Package.swift"},
	})
	if err != nil {
		t.Fatal(err)
	}
	graph := MergeFragments(root, []Fragment{fragment})
	if graph.Coverage.Status != CoveragePartial {
		t.Fatalf("coverage = %q, want partial", graph.Coverage.Status)
	}
	for _, code := range []string{"ambiguous-swiftpm-product", "computed-swiftpm-target-name", "invalid-node-path"} {
		if !hasIssueCode(graph.Coverage.Issues, code) {
			t.Fatalf("issues = %#v, want %s", graph.Coverage.Issues, code)
		}
	}
	appID := swiftPMID("Package.swift", "App")
	if len(graph.Dependencies[appID]) != 0 {
		t.Fatalf("ambiguous product emitted edges: %#v", graph.Dependencies[appID])
	}
	if _, ok := graph.Nodes[swiftPMID("Package.swift", "Outside")]; ok {
		t.Fatal("escaping target retained")
	}
}

func TestSwiftPMParserSkipsStringsAndAcceptsLiteralStringForms(t *testing.T) {
	parsed := parseSwiftPMManifest("Package.swift", []byte(`
let documentation = """Package(name: "Fake", targets: ["Fake"])"""
let package = Package(
    name: #"Demo"#,
    targets: [
        .target(name: #"Core"#, sources: ["""Core.swift"""]),
    ]
)
`))
	if len(parsed.issues) != 0 {
		t.Fatalf("issues = %#v", parsed.issues)
	}
	if len(parsed.targets) != 1 || parsed.targets[0].name != "Core" {
		t.Fatalf("targets = %#v", parsed.targets)
	}
	if got := parsed.targets[0].memberRoots; !reflect.DeepEqual(got, []string{"Sources/Core/Core.swift"}) {
		t.Fatalf("member roots = %#v", got)
	}

	if _, ok := swiftLiteralString(`"Core" + "Other"`); ok {
		t.Fatal("accepted a computed string as a literal")
	}
}

func TestSwiftPMProviderMetadata(t *testing.T) {
	provider := swiftPMProvider{}
	if provider.Name() != "swiftpm" || provider.Version() == "" {
		t.Fatalf("provider metadata = %q %q", provider.Name(), provider.Version())
	}
	if got := provider.Manifests().Names; !reflect.DeepEqual(got, []string{"Package.swift"}) {
		t.Fatalf("manifests = %#v", got)
	}
}

func findTopologyEdge(edges []Edge, target ID, scope EdgeScope) *Edge {
	for i := range edges {
		if edges[i].To == target && edges[i].Kind == EdgeDependency && edges[i].Scope == scope {
			return &edges[i]
		}
	}
	return nil
}

func assertSwiftPMEdge(t *testing.T, edges []Edge, target ID, kind EdgeKind, scope EdgeScope) {
	t.Helper()
	for _, edge := range edges {
		if edge.To == target && edge.Kind == kind && edge.Scope == scope {
			return
		}
	}
	t.Fatalf("edge to %q kind=%q scope=%q missing from %#v", target, kind, scope, edges)
}
