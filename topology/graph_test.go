package topology

import (
	"path/filepath"
	"reflect"
	"testing"
)

func testNode(id, name string) Node {
	return Node{
		ID:       ID(id),
		Kind:     NodeKind("module"),
		Name:     name,
		Manifest: "settings.gradle.kts",
		Root:     filepath.FromSlash("modules/" + name),
		Provider: "test",
	}
}

func TestMergeFragmentsBuildsDeterministicIndexes(t *testing.T) {
	root := t.TempDir()
	a := testNode("test:settings.gradle.kts:a", "a")
	b := testNode("test:settings.gradle.kts:b", "b")
	graph := MergeFragments(root, []Fragment{{
		Provider: "test",
		Nodes:    []Node{b, a},
		Edges: []Edge{{
			From:     b.ID,
			To:       a.ID,
			Kind:     EdgeDependency,
			Evidence: Evidence{Manifest: "settings.gradle.kts", Line: 7},
		}},
		Members: map[ID][]string{
			b.ID: {filepath.FromSlash("modules/b/src/B.kt")},
			a.ID: {filepath.FromSlash("modules/a/src/A.kt")},
		},
		Coverage: Coverage{Status: CoverageComplete},
	}})

	if graph.Coverage.Status != CoverageComplete {
		t.Fatalf("coverage = %q, want complete: %#v", graph.Coverage.Status, graph.Coverage.Issues)
	}
	if got := graph.Dependencies[b.ID]; len(got) != 1 || got[0].To != a.ID {
		t.Fatalf("dependencies[%q] = %#v", b.ID, got)
	}
	if got := graph.Dependents[a.ID]; len(got) != 1 || got[0].From != b.ID {
		t.Fatalf("dependents[%q] = %#v", a.ID, got)
	}
	wantOwners := []ID{a.ID}
	if got := graph.OwnersForFile(filepath.FromSlash("modules/a/src/A.kt")); !reflect.DeepEqual(got, wantOwners) {
		t.Fatalf("owners = %#v, want %#v", got, wantOwners)
	}

	reversed := MergeFragments(root, []Fragment{{
		Provider: "test",
		Nodes:    []Node{a, b},
		Edges: []Edge{{
			From:     b.ID,
			To:       a.ID,
			Kind:     EdgeDependency,
			Evidence: Evidence{Manifest: "settings.gradle.kts", Line: 7},
		}},
		Members: map[ID][]string{
			a.ID: {filepath.FromSlash("modules/a/src/A.kt")},
			b.ID: {filepath.FromSlash("modules/b/src/B.kt")},
		},
		Coverage: Coverage{Status: CoverageComplete},
	}})
	if !reflect.DeepEqual(graph, reversed) {
		t.Fatalf("merge is order-dependent:\nfirst:  %#v\nsecond: %#v", graph, reversed)
	}
}

func TestMergeFragmentsRejectsEscapingPathsAndUnknownEndpoints(t *testing.T) {
	root := t.TempDir()
	valid := testNode("test:settings.gradle.kts:valid", "valid")
	escaping := testNode("test:settings.gradle.kts:escape", "escape")
	escaping.Root = filepath.Join("..", "escape")

	graph := MergeFragments(root, []Fragment{{
		Provider: "test",
		Nodes:    []Node{valid, escaping},
		Edges: []Edge{{
			From:     valid.ID,
			To:       ID("test:settings.gradle.kts:missing"),
			Kind:     EdgeDependency,
			Evidence: Evidence{Manifest: "settings.gradle.kts"},
		}},
		Members: map[ID][]string{
			valid.ID: {filepath.Join("..", "outside.kt")},
		},
		Coverage: Coverage{Status: CoverageComplete},
	}})

	if graph.Coverage.Status != CoveragePartial {
		t.Fatalf("coverage = %q, want partial", graph.Coverage.Status)
	}
	if _, ok := graph.Nodes[escaping.ID]; ok {
		t.Fatalf("escaping node %q was retained", escaping.ID)
	}
	if len(graph.Dependencies[valid.ID]) != 0 {
		t.Fatalf("unknown endpoint edge was retained: %#v", graph.Dependencies[valid.ID])
	}
	if len(graph.Coverage.Issues) < 3 {
		t.Fatalf("issues = %#v, want path, member, and endpoint issues", graph.Coverage.Issues)
	}
}

func TestMergeFragmentsRejectsMissingNodePaths(t *testing.T) {
	root := t.TempDir()
	missing := testNode("test:missing", "missing")
	missing.Manifest = ""
	missing.Root = ""

	graph := MergeFragments(root, []Fragment{{
		Provider: "test",
		Nodes:    []Node{missing},
		Coverage: Coverage{Status: CoverageComplete},
	}})

	if graph.Coverage.Status != CoverageUnavailable {
		t.Fatalf("coverage = %q, want unavailable", graph.Coverage.Status)
	}
	if _, ok := graph.Nodes[missing.ID]; ok {
		t.Fatalf("node with missing paths was retained: %#v", graph.Nodes[missing.ID])
	}
	if !hasIssueCode(graph.Coverage.Issues, "invalid-node-path") {
		t.Fatalf("issues = %#v, want invalid-node-path", graph.Coverage.Issues)
	}
}

func TestMergeFragmentsRejectsUnknownEdgeKinds(t *testing.T) {
	root := t.TempDir()
	a := testNode("test:a", "a")
	b := testNode("test:b", "b")
	graph := MergeFragments(root, []Fragment{{
		Provider: "test",
		Nodes:    []Node{a, b},
		Edges: []Edge{
			{From: a.ID, To: b.ID, Kind: EdgeKind("dependecy"), Evidence: Evidence{Manifest: "settings.gradle.kts"}},
			{From: b.ID, To: a.ID, Evidence: Evidence{Manifest: "settings.gradle.kts"}},
		},
		Members: map[ID][]string{
			a.ID: {"a/Main.kt"},
			b.ID: {"b/Main.kt"},
		},
		Coverage: Coverage{Status: CoverageComplete},
	}})

	if graph.Coverage.Status != CoveragePartial {
		t.Fatalf("coverage = %q, want partial", graph.Coverage.Status)
	}
	if len(graph.Dependencies) != 0 || len(graph.Dependents) != 0 {
		t.Fatalf("invalid edge kinds were retained: deps=%#v dependents=%#v", graph.Dependencies, graph.Dependents)
	}
	if !hasIssueCode(graph.Coverage.Issues, "invalid-edge-kind") {
		t.Fatalf("issues = %#v, want invalid-edge-kind", graph.Coverage.Issues)
	}
}

func TestDependencyEdgesAloneCreateHubs(t *testing.T) {
	root := t.TempDir()
	hub := testNode("test:settings.gradle.kts:hub", "hub")
	nodes := []Node{hub}
	members := map[ID][]string{hub.ID: {"modules/hub/src/Hub.kt"}}
	var edges []Edge
	for _, suffix := range []string{"a", "b", "c"} {
		node := testNode("test:settings.gradle.kts:"+suffix, suffix)
		nodes = append(nodes, node)
		members[node.ID] = []string{"modules/" + suffix + "/src/Main.kt"}
		edges = append(edges, Edge{
			From:     node.ID,
			To:       hub.ID,
			Kind:     EdgeDependency,
			Evidence: Evidence{Manifest: "settings.gradle.kts"},
		})
	}
	parent := testNode("test:pom.xml:parent", "parent")
	boundary := testNode("test:settings.gradle.kts:boundary", "boundary")
	nodes = append(nodes, parent, boundary)
	members[parent.ID] = []string{"parent/src/Parent.java"}
	members[boundary.ID] = []string{"boundary/src/Main.kt"}
	edges = append(edges,
		Edge{From: nodes[1].ID, To: parent.ID, Kind: EdgeInheritance, Evidence: Evidence{Manifest: "pom.xml"}},
		Edge{From: nodes[2].ID, To: boundary.ID, Kind: EdgeBuildBoundary, Evidence: Evidence{Manifest: "settings.gradle.kts"}},
	)

	graph := MergeFragments(root, []Fragment{{
		Provider: "test",
		Nodes:    nodes,
		Edges:    edges,
		Members:  members,
		Coverage: Coverage{Status: CoverageComplete},
	}})

	if !graph.IsHub(hub.ID) {
		t.Fatalf("%q should be a hub", hub.ID)
	}
	if graph.IsHub(parent.ID) || graph.IsHub(boundary.ID) {
		t.Fatalf("non-dependency edges created hubs: parent=%v boundary=%v", graph.IsHub(parent.ID), graph.IsHub(boundary.ID))
	}
	if got := graph.HubNodes(); !reflect.DeepEqual(got, []ID{hub.ID}) {
		t.Fatalf("hubs = %#v, want %#v", got, []ID{hub.ID})
	}
}

func TestSelectModuleFailsClosedForDuplicateDisplayNames(t *testing.T) {
	root := t.TempDir()
	left := testNode("test:left:common", "common")
	right := testNode("test:right:common", "common")
	graph := MergeFragments(root, []Fragment{{
		Provider: "test",
		Nodes:    []Node{right, left},
		Members: map[ID][]string{
			left.ID:  {"left/Main.kt"},
			right.ID: {"right/Main.kt"},
		},
		Coverage: Coverage{Status: CoverageComplete},
	}})

	if _, candidates, ok := graph.SelectModule("common"); ok || !reflect.DeepEqual(candidates, []ID{left.ID, right.ID}) {
		t.Fatalf("ambiguous selection = ok:%v candidates:%#v", ok, candidates)
	}
	if node, candidates, ok := graph.SelectModule(string(right.ID)); !ok || len(candidates) != 0 || node.ID != right.ID {
		t.Fatalf("exact selection = node:%#v candidates:%#v ok:%v", node, candidates, ok)
	}
}

func TestOwnersReturnEveryCandidateWithoutSelectingOne(t *testing.T) {
	root := t.TempDir()
	a := testNode("test:a", "a")
	b := testNode("test:b", "b")
	graph := MergeFragments(root, []Fragment{{
		Provider: "test",
		Nodes:    []Node{b, a},
		Members: map[ID][]string{
			a.ID: {"shared/Main.kt"},
			b.ID: {"shared/Main.kt"},
		},
		Coverage: Coverage{Status: CoverageComplete},
	}})

	if got := graph.OwnersForFile("shared/Main.kt"); !reflect.DeepEqual(got, []ID{a.ID, b.ID}) {
		t.Fatalf("owners = %#v, want both candidates", got)
	}
}

func TestExpandReferenceEmitsEveryResolvedTargetOnce(t *testing.T) {
	from := ID("swiftpm:Package.swift:Client")
	a := ID("swiftpm:Package.swift:A")
	b := ID("swiftpm:Package.swift:B")
	template := Edge{
		Kind:        EdgeDependency,
		Scope:       EdgeScope("production"),
		Evidence:    Evidence{Manifest: "Package.swift", Line: 12},
		Conditional: true,
	}

	edges, issue := ExpandReference(from, template, ReferenceResolution{
		Status:  ResolutionResolved,
		Targets: []ID{b, a, b},
	})

	if issue != nil {
		t.Fatalf("unexpected issue: %#v", issue)
	}
	want := []Edge{
		{From: from, To: a, Kind: EdgeDependency, Scope: EdgeScope("production"), Evidence: template.Evidence, Conditional: true},
		{From: from, To: b, Kind: EdgeDependency, Scope: EdgeScope("production"), Evidence: template.Evidence, Conditional: true},
	}
	if !reflect.DeepEqual(edges, want) {
		t.Fatalf("edges = %#v, want %#v", edges, want)
	}
}

func TestExpandReferenceRejectsAmbiguityWithoutEdges(t *testing.T) {
	a := ID("swiftpm:Package.swift:A")
	b := ID("swiftpm:Package.swift:B")
	edges, issue := ExpandReference(ID("swiftpm:Package.swift:Client"), Edge{
		Kind:     EdgeDependency,
		Evidence: Evidence{Manifest: "Package.swift", Line: 12},
	}, ReferenceResolution{
		Status:     ResolutionAmbiguous,
		Candidates: []ID{b, a, b},
		Note:       "product Core matches multiple local declarations",
	})

	if len(edges) != 0 {
		t.Fatalf("ambiguous resolution emitted edges: %#v", edges)
	}
	if issue == nil || issue.Code != "ambiguous-reference" {
		t.Fatalf("issue = %#v, want ambiguous-reference", issue)
	}
	if !reflect.DeepEqual(issue.Candidates, []ID{a, b}) {
		t.Fatalf("candidates = %#v, want sorted unique IDs", issue.Candidates)
	}
}

func TestMergeFragmentsIgnoresNonApplicableProviders(t *testing.T) {
	root := t.TempDir()
	answered := Fragment{
		Provider: "test",
		Nodes:    []Node{testNode("test:settings.gradle.kts:app", "app")},
		Coverage: Coverage{Status: CoverageComplete},
	}
	// Providers whose manifests are absent report Unavailable with nothing to say.
	notApplicable := []Fragment{
		{Provider: "jvm", Coverage: Coverage{Status: CoverageUnavailable}},
		{Provider: "swiftpm", Coverage: Coverage{Status: CoverageUnavailable}},
	}

	graph := MergeFragments(root, append([]Fragment{answered}, notApplicable...))
	if graph.Coverage.Status != CoverageComplete {
		t.Fatalf("coverage = %q, want %q when the only applicable provider answered fully",
			graph.Coverage.Status, CoverageComplete)
	}

	// Nothing applied anywhere: topology has nothing to report.
	if graph := MergeFragments(root, notApplicable); graph.Coverage.Status != CoverageUnavailable {
		t.Fatalf("coverage = %q, want %q when no provider produced a module",
			graph.Coverage.Status, CoverageUnavailable)
	}

	// A provider that genuinely failed carries an Issue and still counts.
	failed := Fragment{
		Provider: "jvm",
		Coverage: Coverage{
			Status: CoverageUnavailable,
			Issues: []Issue{{Provider: "jvm", Code: "provider-failed", Message: "boom"}},
		},
	}
	if graph := MergeFragments(root, []Fragment{answered, failed}); graph.Coverage.Status != CoveragePartial {
		t.Fatalf("coverage = %q, want %q when an applicable provider failed",
			graph.Coverage.Status, CoveragePartial)
	}
}
