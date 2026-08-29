package topology

import (
	"strings"
	"testing"
)

func TestFormatTopologyIsDeterministicAndFilterable(t *testing.T) {
	root := t.TempDir()
	app := testNode("jvm:settings.gradle.kts:app", "app")
	app.Provider = "jvm"
	core := testNode("jvm:settings.gradle.kts:core", "core")
	core.Provider = "jvm"
	swift := testNode("swiftpm:Package.swift:Core", "Core")
	swift.Provider = "swiftpm"
	swift.Manifest = "Package.swift"
	swift.Root = "Sources/Core"
	graph := MergeFragments(root, []Fragment{{
		Provider: "mixed",
		Nodes:    []Node{swift, core, app},
		Edges: []Edge{{
			From:     app.ID,
			To:       core.ID,
			Kind:     EdgeDependency,
			Scope:    EdgeScope("production"),
			Evidence: Evidence{Manifest: "settings.gradle.kts", Line: 4},
		}},
		Members: map[ID][]string{
			app.ID:   {"modules/app/src/Main.kt"},
			core.ID:  {"modules/core/src/Core.kt"},
			swift.ID: {"Sources/Core/Core.swift"},
		},
		Coverage: Coverage{Status: CoverageComplete},
	}})

	first := FormatGraph(graph, "")
	second := FormatGraph(graph, "")
	if first != second {
		t.Fatalf("format changed between calls:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	for _, want := range []string{
		"PROJECT TOPOLOGY",
		"Coverage: complete",
		"jvm:settings.gradle.kts:app",
		"jvm:settings.gradle.kts:core",
		"swiftpm:Package.swift:Core",
		"app -> core [dependency, production]",
	} {
		if !strings.Contains(first, want) {
			t.Fatalf("formatted graph missing %q:\n%s", want, first)
		}
	}

	jvm := FormatGraph(graph, "jvm")
	if strings.Contains(jvm, string(swift.ID)) || !strings.Contains(jvm, string(app.ID)) {
		t.Fatalf("ecosystem filter produced:\n%s", jvm)
	}

	jsonA, err := FormatGraphJSON(graph, "")
	if err != nil {
		t.Fatal(err)
	}
	jsonB, err := FormatGraphJSON(graph, "")
	if err != nil {
		t.Fatal(err)
	}
	if string(jsonA) != string(jsonB) {
		t.Fatalf("JSON is nondeterministic:\n%s\n%s", jsonA, jsonB)
	}
}

func TestFormatModuleContextFailsClosedForAmbiguousSelectors(t *testing.T) {
	root := t.TempDir()
	left := testNode("jvm:left:common", "common")
	right := testNode("jvm:right:common", "common")
	graph := MergeFragments(root, []Fragment{{
		Provider: "jvm",
		Nodes:    []Node{right, left},
		Members: map[ID][]string{
			left.ID:  {"shared/Main.kt"},
			right.ID: {"shared/Main.kt"},
		},
		Coverage: Coverage{Status: CoverageComplete},
	}})

	moduleText, err := FormatModuleContext(graph, "common", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(moduleText, "Ambiguous module") ||
		!strings.Contains(moduleText, string(left.ID)) ||
		!strings.Contains(moduleText, string(right.ID)) ||
		strings.Contains(moduleText, "Hub:") {
		t.Fatalf("ambiguous module output asserted a selection:\n%s", moduleText)
	}

	fileText, err := FormatModuleContext(graph, "", "shared/Main.kt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fileText, "Ambiguous ownership") ||
		!strings.Contains(fileText, string(left.ID)) ||
		!strings.Contains(fileText, string(right.ID)) ||
		strings.Contains(fileText, "Member of hub module") {
		t.Fatalf("ambiguous file output asserted hub membership:\n%s", fileText)
	}
}

func TestFormatModuleContextReportsUniqueHubMembership(t *testing.T) {
	root := t.TempDir()
	hub := testNode("jvm:settings.gradle.kts:core", "core")
	nodes := []Node{hub}
	members := map[ID][]string{hub.ID: {"core/src/Core.kt"}}
	var edges []Edge
	for _, name := range []string{"a", "b", "c"} {
		node := testNode("jvm:settings.gradle.kts:"+name, name)
		nodes = append(nodes, node)
		members[node.ID] = []string{name + "/src/Main.kt"}
		edges = append(edges, Edge{
			From:     node.ID,
			To:       hub.ID,
			Kind:     EdgeDependency,
			Evidence: Evidence{Manifest: "settings.gradle.kts"},
		})
	}
	graph := MergeFragments(root, []Fragment{{
		Provider: "jvm",
		Nodes:    nodes,
		Edges:    edges,
		Members:  members,
		Coverage: Coverage{Status: CoverageComplete},
	}})

	text, err := FormatModuleContext(graph, "", "core/src/Core.kt")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Module: core",
		"Member of hub module: yes",
		"Dependents: 3",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("module context missing %q:\n%s", want, text)
		}
	}
}
