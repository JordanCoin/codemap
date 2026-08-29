package topology

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"codemap/scanner"
)

func TestMavenBuildsReactorParentsDependenciesAndMembership(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "pom.xml", `
<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>root</artifactId>
  <version>1.0.0</version>
  <packaging>pom</packaging>
  <modules><module>core</module><module>app</module></modules>
</project>`)
	writeTopologyFixture(t, root, "core/pom.xml", `
<project>
  <parent>
    <groupId>com.example</groupId><artifactId>root</artifactId><version>1.0.0</version>
    <relativePath>../pom.xml</relativePath>
  </parent>
  <artifactId>core</artifactId>
</project>`)
	writeTopologyFixture(t, root, "app/pom.xml", `
<project>
  <parent>
    <groupId>com.example</groupId><artifactId>root</artifactId><version>1.0.0</version>
    <relativePath>../pom.xml</relativePath>
  </parent>
  <artifactId>app</artifactId>
  <dependencies>
    <dependency>
      <groupId>com.example</groupId><artifactId>core</artifactId><version>1.0.0</version>
      <scope>runtime</scope>
    </dependency>
  </dependencies>
</project>`)
	files := []scanner.FileInfo{
		{Path: filepath.FromSlash("core/src/main/java/Core.java"), Ext: ".java"},
		{Path: filepath.FromSlash("core/src/test/kotlin/CoreTest.kt"), Ext: ".kt"},
		{Path: filepath.FromSlash("app/src/main/scala/App.scala"), Ext: ".scala"},
	}
	for _, file := range files {
		writeTopologyFixture(t, root, filepath.ToSlash(file.Path), "// source\n")
	}

	fragment, err := (jvmProvider{}).Build(context.Background(), Inventory{
		Root:  root,
		Files: files,
		Manifests: []string{
			"pom.xml",
			filepath.FromSlash("core/pom.xml"),
			filepath.FromSlash("app/pom.xml"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	graph := MergeFragments(root, []Fragment{fragment})
	if graph.Coverage.Status != CoverageComplete {
		t.Fatalf("coverage = %q: %#v", graph.Coverage.Status, graph.Coverage.Issues)
	}

	rootID := mavenID("pom.xml", "com.example", "root")
	coreID := mavenID(filepath.FromSlash("core/pom.xml"), "com.example", "core")
	appID := mavenID(filepath.FromSlash("app/pom.xml"), "com.example", "app")
	for _, id := range []ID{rootID, coreID, appID} {
		if _, ok := graph.Nodes[id]; !ok {
			t.Fatalf("missing Maven node %q", id)
		}
	}
	assertTopologyEdge(t, graph.Dependencies[coreID], rootID, EdgeInheritance, EdgeScope(""))
	assertTopologyEdge(t, graph.Dependencies[appID], rootID, EdgeInheritance, EdgeScope(""))
	assertTopologyEdge(t, graph.Dependencies[appID], coreID, EdgeDependency, EdgeScope("runtime"))
	if len(graph.Members[coreID]) != 2 || len(graph.Members[appID]) != 1 {
		t.Fatalf("members: core=%#v app=%#v", graph.Members[coreID], graph.Members[appID])
	}
}

func TestMaven4BuildsSubprojectsAndSources(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "pom.xml", `
<project xmlns="http://maven.apache.org/POM/4.1.0">
  <modelVersion>4.1.0</modelVersion>
  <groupId>com.example</groupId><artifactId>root</artifactId><version>1.0.0</version>
  <packaging>pom</packaging>
  <subprojects><subproject>app</subproject></subprojects>
</project>`)
	writeTopologyFixture(t, root, "app/pom.xml", `
<project xmlns="http://maven.apache.org/POM/4.1.0">
  <modelVersion>4.1.0</modelVersion>
  <parent><groupId>com.example</groupId><artifactId>root</artifactId></parent>
  <artifactId>app</artifactId>
  <build><sources>
    <source><scope>main</scope><directory>src/main/generated</directory></source>
    <source><scope>test</scope><directory>src/integration/kotlin</directory></source>
  </sources></build>
</project>`)
	files := []scanner.FileInfo{
		{Path: filepath.FromSlash("app/src/main/generated/Generated.java"), Ext: ".java"},
		{Path: filepath.FromSlash("app/src/integration/kotlin/AppTest.kt"), Ext: ".kt"},
	}
	fragment, err := (jvmProvider{}).Build(context.Background(), Inventory{
		Root: root, Files: files, Manifests: []string{"pom.xml", "app/pom.xml"},
	})
	if err != nil {
		t.Fatal(err)
	}
	graph := MergeFragments(root, []Fragment{fragment})
	if graph.Coverage.Status != CoverageComplete {
		t.Fatalf("coverage = %q: %#v", graph.Coverage.Status, graph.Coverage.Issues)
	}
	rootID := mavenID("pom.xml", "com.example", "root")
	appID := mavenID(filepath.FromSlash("app/pom.xml"), "com.example", "app")
	assertTopologyEdge(t, graph.Dependencies[rootID], appID, EdgeBuildBoundary, EdgeScope(""))
	if !slices.Contains(graph.Nodes[appID].SourceRoots, filepath.FromSlash("app/src/main/generated")) ||
		!slices.Contains(graph.Nodes[appID].TestSourceRoots, filepath.FromSlash("app/src/integration/kotlin")) {
		t.Fatalf("app roots = %#v", graph.Nodes[appID])
	}
	if !slices.Contains(graph.Members[appID], filepath.FromSlash("app/src/main/generated/Generated.java")) ||
		!slices.Contains(graph.Members[appID], filepath.FromSlash("app/src/integration/kotlin/AppTest.kt")) {
		t.Fatalf("app members = %#v", graph.Members[appID])
	}
}

func TestMaven4AutoDiscoversDirectSubprojects(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "pom.xml", `<project>
  <modelVersion>4.1.0</modelVersion><groupId>com.example</groupId>
  <artifactId>root</artifactId><packaging>pom</packaging>
</project>`)
	writeTopologyFixture(t, root, "app/pom.xml", `<project>
  <modelVersion>4.1.0</modelVersion>
  <parent><groupId>com.example</groupId><artifactId>root</artifactId></parent>
  <artifactId>app</artifactId>
</project>`)
	fragment, err := (jvmProvider{}).Build(context.Background(), Inventory{
		Root: root, Manifests: []string{"pom.xml", "app/pom.xml"},
	})
	if err != nil {
		t.Fatal(err)
	}
	graph := MergeFragments(root, []Fragment{fragment})
	assertTopologyEdge(t, graph.Dependencies[mavenID("pom.xml", "com.example", "root")],
		mavenID(filepath.FromSlash("app/pom.xml"), "com.example", "app"), EdgeBuildBoundary, EdgeScope(""))
}

func TestMaven3DoesNotAutoDiscoverDirectSubprojects(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "pom.xml", `<project>
  <modelVersion>4.0.0</modelVersion><groupId>com.example</groupId>
  <artifactId>root</artifactId><packaging>pom</packaging>
</project>`)
	writeTopologyFixture(t, root, "app/pom.xml", `<project>
  <modelVersion>4.0.0</modelVersion>
  <parent><groupId>com.example</groupId><artifactId>root</artifactId></parent>
  <artifactId>app</artifactId>
</project>`)
	fragment, err := (jvmProvider{}).Build(context.Background(), Inventory{
		Root: root, Manifests: []string{"pom.xml", "app/pom.xml"},
	})
	if err != nil {
		t.Fatal(err)
	}
	graph := MergeFragments(root, []Fragment{fragment})
	for _, edge := range graph.Dependencies[mavenID("pom.xml", "com.example", "root")] {
		if edge.Kind == EdgeBuildBoundary {
			t.Fatalf("Maven 3 root unexpectedly auto-discovered child: %#v", edge)
		}
	}
}

func TestMavenFailsClosedForDuplicateCoordinatesAndTopologyProfiles(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "one/pom.xml", `
<project><groupId>com.example</groupId><artifactId>shared</artifactId><version>1</version></project>`)
	writeTopologyFixture(t, root, "two/pom.xml", `
<project><groupId>com.example</groupId><artifactId>shared</artifactId><version>2</version></project>`)
	writeTopologyFixture(t, root, "app/pom.xml", `
<project>
  <groupId>com.example</groupId><artifactId>app</artifactId>
  <dependencies><dependency><groupId>com.example</groupId><artifactId>shared</artifactId></dependency></dependencies>
  <profiles><profile><id>extra</id><modules><module>dynamic</module></modules></profile></profiles>
</project>`)

	fragment, err := (jvmProvider{}).Build(context.Background(), Inventory{
		Root: root,
		Manifests: []string{
			filepath.FromSlash("one/pom.xml"),
			filepath.FromSlash("two/pom.xml"),
			filepath.FromSlash("app/pom.xml"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	graph := MergeFragments(root, []Fragment{fragment})
	if graph.Coverage.Status != CoveragePartial {
		t.Fatalf("coverage = %q, want partial", graph.Coverage.Status)
	}
	for _, code := range []string{"ambiguous-maven-coordinate", "maven-profile-topology"} {
		if !hasIssueCode(graph.Coverage.Issues, code) {
			t.Fatalf("issues = %#v, want %s", graph.Coverage.Issues, code)
		}
	}
	appID := mavenID(filepath.FromSlash("app/pom.xml"), "com.example", "app")
	for _, edge := range graph.Dependencies[appID] {
		if edge.Kind == EdgeDependency {
			t.Fatalf("ambiguous Maven coordinate emitted edge: %#v", edge)
		}
	}
}

func TestMavenReportsUnresolvedPropertyCoordinates(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "pom.xml", `
<project>
  <groupId>com.example</groupId><artifactId>app</artifactId>
  <dependencies>
    <dependency><groupId>${local.group}</groupId><artifactId>core</artifactId></dependency>
  </dependencies>
</project>`)
	fragment, err := (jvmProvider{}).Build(context.Background(), Inventory{
		Root:      root,
		Manifests: []string{"pom.xml"},
	})
	if err != nil {
		t.Fatal(err)
	}
	graph := MergeFragments(root, []Fragment{fragment})
	if !hasIssueCode(graph.Coverage.Issues, "unresolved-maven-property") {
		t.Fatalf("issues = %#v", graph.Coverage.Issues)
	}
}

func TestMavenResolvesPropertiesScopesAndSourceRoots(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "pom.xml", `
<project>
  <groupId>com.example</groupId><artifactId>app</artifactId>
  <properties><local.group>com.example</local.group><generated>src/generated/java</generated></properties>
  <build>
    <sourceDirectory>${project.basedir}/${generated}</sourceDirectory>
    <testSourceDirectory>src/integration/kotlin</testSourceDirectory>
  </build>
  <dependencies>
    <dependency><groupId>${local.group}</groupId><artifactId>compile-lib</artifactId></dependency>
    <dependency><groupId>com.example</groupId><artifactId>provided-lib</artifactId><scope>provided</scope></dependency>
    <dependency><groupId>com.example</groupId><artifactId>test-lib</artifactId><scope>test</scope></dependency>
    <dependency><groupId>com.example</groupId><artifactId>optional-lib</artifactId><optional>true</optional></dependency>
  </dependencies>
</project>`)
	for _, artifact := range []string{"compile-lib", "provided-lib", "test-lib", "optional-lib"} {
		writeTopologyFixture(t, root, artifact+"/pom.xml",
			`<project><groupId>com.example</groupId><artifactId>`+artifact+`</artifactId></project>`)
	}
	manifests := []string{"pom.xml"}
	for _, artifact := range []string{"compile-lib", "provided-lib", "test-lib", "optional-lib"} {
		manifests = append(manifests, filepath.FromSlash(artifact+"/pom.xml"))
	}

	fragment, err := (jvmProvider{}).Build(context.Background(), Inventory{Root: root, Manifests: manifests})
	if err != nil {
		t.Fatal(err)
	}
	graph := MergeFragments(root, []Fragment{fragment})
	if graph.Coverage.Status != CoverageComplete {
		t.Fatalf("coverage = %q: %#v", graph.Coverage.Status, graph.Coverage.Issues)
	}
	appID := mavenID("pom.xml", "com.example", "app")
	for _, expectation := range []struct {
		artifact string
		scope    EdgeScope
	}{
		{"compile-lib", "compile"},
		{"provided-lib", "provided"},
		{"test-lib", "test"},
		{"optional-lib", "optional"},
	} {
		assertTopologyEdge(t, graph.Dependencies[appID],
			mavenID(filepath.FromSlash(expectation.artifact+"/pom.xml"), "com.example", expectation.artifact),
			EdgeDependency, expectation.scope)
	}
	node := graph.Nodes[appID]
	if !slices.Contains(node.SourceRoots, filepath.FromSlash("src/generated/java")) {
		t.Fatalf("source roots = %#v", node.SourceRoots)
	}
	if !slices.Contains(node.TestSourceRoots, filepath.FromSlash("src/integration/kotlin")) {
		t.Fatalf("test roots = %#v", node.TestSourceRoots)
	}
}

func TestMavenResolvesChainedPropertiesDeterministically(t *testing.T) {
	properties := map[string]string{
		"first":  "${second}",
		"second": "${third}",
		"third":  "com.example",
	}
	if got, ok := resolveMavenValue(properties["first"], properties); !ok || got != "com.example" {
		t.Fatalf("single property resolution = %q, %v", got, ok)
	}

	root := t.TempDir()
	writeTopologyFixture(t, root, "pom.xml", `<project>
  <groupId>com.example</groupId><artifactId>app</artifactId>
  <properties><first>${second}</first><second>${third}</second><third>com.example</third></properties>
  <dependencies><dependency><groupId>${first}</groupId><artifactId>lib</artifactId></dependency></dependencies>
</project>`)
	writeTopologyFixture(t, root, "lib/pom.xml", `<project><groupId>com.example</groupId><artifactId>lib</artifactId></project>`)
	fragment, err := (jvmProvider{}).Build(context.Background(), Inventory{Root: root, Manifests: []string{"pom.xml", "lib/pom.xml"}})
	if err != nil {
		t.Fatal(err)
	}
	graph := MergeFragments(root, []Fragment{fragment})
	if hasIssueCode(graph.Coverage.Issues, "unresolved-maven-property") {
		t.Fatalf("chained property remained unresolved: %#v", graph.Coverage.Issues)
	}
	appID := mavenID("pom.xml", "com.example", "app")
	libID := mavenID("lib/pom.xml", "com.example", "lib")
	assertTopologyEdge(t, graph.Dependencies[appID], libID, EdgeDependency, EdgeScope("compile"))
}
