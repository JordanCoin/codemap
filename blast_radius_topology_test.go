package main

import (
	"context"
	"strings"
	"testing"

	"codemap/scanner"
	"codemap/topology"
)

func TestBuildTopologyImpactsKeepsModulesSeparateFromFiles(t *testing.T) {
	core := topology.Node{
		ID:       topology.ID("jvm:settings.gradle.kts:core"),
		Name:     "core",
		Manifest: "settings.gradle.kts",
		Root:     "core",
		Provider: "jvm",
	}
	app := topology.Node{
		ID:       topology.ID("jvm:settings.gradle.kts:app"),
		Name:     "app",
		Manifest: "settings.gradle.kts",
		Root:     "app",
		Provider: "jvm",
	}
	graph := &topology.Graph{
		Nodes: map[topology.ID]topology.Node{core.ID: core, app.ID: app},
		Dependencies: map[topology.ID][]topology.Edge{
			app.ID: {{From: app.ID, To: core.ID, Kind: topology.EdgeDependency}},
		},
		Dependents: map[topology.ID][]topology.Edge{
			core.ID: {{From: app.ID, To: core.ID, Kind: topology.EdgeDependency}},
		},
		Members: map[topology.ID][]string{
			core.ID: {"core/src/Core.kt"},
			app.ID:  {"app/src/Main.kt", "app/src/Other.kt"},
		},
		Owners: map[string][]topology.ID{
			"core/src/Core.kt": {core.ID},
			"app/src/Main.kt":  {app.ID},
			"app/src/Other.kt": {app.ID},
		},
		Coverage: topology.Coverage{Status: topology.CoverageComplete},
	}
	changed := []scanner.FileInfo{{Path: "core/src/Core.kt"}}

	got := buildTopologyImpacts(graph, changed)
	if len(got) != 2 {
		t.Fatalf("module impacts = %#v, want owner and dependent", got)
	}
	if got[0].ID != core.ID || got[0].Relation != "owns-changed-file" {
		t.Fatalf("owner impact = %#v", got[0])
	}
	if got[1].ID != app.ID || got[1].Relation != "depends-on-changed-module" {
		t.Fatalf("dependent impact = %#v", got[1])
	}
	for _, impact := range got {
		if strings.Contains(impact.Name, ".kt") {
			t.Fatalf("module impact contains an expanded member file: %#v", impact)
		}
	}
}

func TestCollectTopologyImpactsSkipsEmptyDiff(t *testing.T) {
	original := buildBlastTopology
	called := false
	buildBlastTopology = func(context.Context, string) (*topology.Graph, topology.CacheIdentity, error) {
		called = true
		return nil, topology.CacheIdentity{}, nil
	}
	t.Cleanup(func() { buildBlastTopology = original })

	if got := collectTopologyImpacts(t.TempDir(), nil); got != nil {
		t.Fatalf("empty diff impacts = %#v, want nil", got)
	}
	if called {
		t.Fatal("empty diff built topology")
	}
}

func TestRenderBlastRadiusIncludesSeparateModuleSection(t *testing.T) {
	bundle := blastRadiusBundle{
		Root:   "/repo",
		Ref:    "main",
		Limits: defaultBlastRadiusLimits(),
		Summary: blastRadiusSummary{
			ChangedFiles:      1,
			ChangedFilesTotal: 1,
		},
		AffectedModules: []blastRadiusModuleImpact{{
			ID:       topology.ID("jvm:settings.gradle.kts:app"),
			Name:     "app",
			Relation: "depends-on-changed-module",
			Via:      topology.ID("jvm:settings.gradle.kts:core"),
		}},
	}

	markdown := renderBlastRadiusMarkdown(bundle)
	if !strings.Contains(markdown, "## Affected Modules") ||
		!strings.Contains(markdown, "`app`") ||
		strings.Contains(markdown, "app/src/") {
		t.Fatalf("unexpected markdown module section:\n%s", markdown)
	}
	text := renderBlastRadiusText(bundle)
	if !strings.Contains(text, "[affected_modules]") || !strings.Contains(text, "app") {
		t.Fatalf("unexpected text module section:\n%s", text)
	}
}
