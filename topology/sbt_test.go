package topology

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"codemap/scanner"
)

func TestSBTBuildsModernAndLegacyProjects(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "build.sbt", `
ThisBuild / name := "Demo"
scalaVersion := "3.8.4"
lazy val root = project.in(file("."))
  .aggregate(core)
lazy val core = project.in(file("core"))
lazy val app = (project in file("app"))
  .dependsOn(core % Test)
/*
  .aggregate(hidden)
*/
Compile / unmanagedSourceDirectories ++= Seq(baseDirectory.value / "src/main/generated", baseDirectory.value / "src/main/extra")
Test / unmanagedSourceDirectories += baseDirectory.value / "src/integration/scala"
`)
	files := []scanner.FileInfo{
		{Path: filepath.FromSlash("core/src/main/java/Core.java"), Ext: ".java"},
		{Path: filepath.FromSlash("core/src/main/kotlin/Core.kt"), Ext: ".kt"},
		{Path: filepath.FromSlash("app/src/main/extra/Extra.scala"), Ext: ".scala"},
		{Path: filepath.FromSlash("app/src/main/generated/Generated.scala"), Ext: ".scala"},
		{Path: filepath.FromSlash("app/src/integration/scala/AppTest.scala"), Ext: ".scala"},
	}
	fragment, err := (jvmProvider{}).Build(context.Background(), Inventory{
		Root:      root,
		Files:     files,
		Manifests: []string{"build.sbt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	graph := MergeFragments(root, []Fragment{fragment})
	rootID := sbtID("build.sbt", "root")
	coreID := sbtID("build.sbt", "core")
	appID := sbtID("build.sbt", "app")
	if graph.Coverage.Status != CoverageComplete {
		t.Fatalf("coverage = %q: %#v", graph.Coverage.Status, graph.Coverage.Issues)
	}
	assertTopologyEdge(t, graph.Dependencies[rootID], coreID, EdgeBuildBoundary, EdgeScope(""))
	assertTopologyEdge(t, graph.Dependencies[appID], coreID, EdgeDependency, EdgeScope("test"))
	if got := graph.Members[coreID]; !reflect.DeepEqual(got, []string{
		filepath.FromSlash("core/src/main/java/Core.java"),
		filepath.FromSlash("core/src/main/kotlin/Core.kt"),
	}) {
		t.Fatalf("core members = %#v", got)
	}
	if got := graph.Members[appID]; !reflect.DeepEqual(got, []string{
		filepath.FromSlash("app/src/integration/scala/AppTest.scala"),
		filepath.FromSlash("app/src/main/extra/Extra.scala"),
		filepath.FromSlash("app/src/main/generated/Generated.scala"),
	}) {
		t.Fatalf("app members = %#v", got)
	}
}

func TestSBTSupportsScala2RootProjectSyntax(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "build.sbt", `
scalaVersion := "2.13.16"
val root = rootProject
  .aggregate(core)
val core = project
`)
	fragment, err := (jvmProvider{}).Build(context.Background(), Inventory{
		Root: root, Manifests: []string{"build.sbt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	graph := MergeFragments(root, []Fragment{fragment})
	if graph.Coverage.Status != CoverageComplete {
		t.Fatalf("coverage = %q: %#v", graph.Coverage.Status, graph.Coverage.Issues)
	}
	assertTopologyEdge(t, graph.Dependencies[sbtID("build.sbt", "root")], sbtID("build.sbt", "core"), EdgeBuildBoundary, EdgeScope(""))
}

func TestSBTReportsDynamicAndUnknownReferences(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "build.sbt", `
lazy val root = project
  .dependsOn(projects.map(_.project))
  .aggregate(missing)
`)
	fragment, err := (jvmProvider{}).Build(context.Background(), Inventory{
		Root: root, Manifests: []string{"build.sbt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	graph := MergeFragments(root, []Fragment{fragment})
	if graph.Coverage.Status != CoveragePartial {
		t.Fatalf("coverage = %q, want partial", graph.Coverage.Status)
	}
	for _, code := range []string{"dynamic-sbt-reference", "unknown-sbt-project"} {
		if !hasIssueCode(graph.Coverage.Issues, code) {
			t.Fatalf("issues = %#v, want %s", graph.Coverage.Issues, code)
		}
	}
}

func TestSBTReportsGeneratedProjects(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "build.sbt", `
val generated = Seq("app").map(name => Project(name))
`)
	fragment, err := (jvmProvider{}).Build(context.Background(), Inventory{
		Root: root, Manifests: []string{"build.sbt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	graph := MergeFragments(root, []Fragment{fragment})
	if graph.Coverage.Status != CoveragePartial || !hasIssueCode(graph.Coverage.Issues, "dynamic-sbt-project") {
		t.Fatalf("coverage = %q, issues = %#v", graph.Coverage.Status, graph.Coverage.Issues)
	}
}
