package topology

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"codemap/config"
	"codemap/scanner"
)

func TestGradleBuildsProjectsDependenciesAndMembership(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "settings.gradle.kts", `
rootProject.name = "Demo"
include(":app", ":business:app_lock", ":core")
project(":core").projectDir = file("modules/core")
includeBuild("build-logic")
`)
	writeTopologyFixture(t, root, "app/build.gradle.kts", `
dependencies {
    implementation(project(":core"))
    testImplementation(projects.business.appLock)
}

`)
	writeTopologyFixture(t, root, "business/app_lock/build.gradle.kts", "")
	writeTopologyFixture(t, root, "modules/core/build.gradle.kts", `
sourceSets {
    main {
        java.srcDir("src/generated/java")
    }
}
`)
	writeTopologyFixture(t, root, "build-logic/settings.gradle.kts", `rootProject.name = "build-logic"`)
	files := []scanner.FileInfo{
		{Path: filepath.FromSlash("app/src/main/kotlin/App.kt"), Ext: ".kt"},
		{Path: filepath.FromSlash("app/src/test/kotlin/AppTest.kt"), Ext: ".kt"},
		{Path: filepath.FromSlash("business/app_lock/src/main/kotlin/Lock.kt"), Ext: ".kt"},
		{Path: filepath.FromSlash("modules/core/src/main/java/Core.java"), Ext: ".java"},
		{Path: filepath.FromSlash("modules/core/src/generated/java/Generated.java"), Ext: ".java"},
		{Path: filepath.FromSlash("build-logic/src/main/kotlin/Plugin.kt"), Ext: ".kt"},
	}
	for _, file := range files {
		writeTopologyFixture(t, root, filepath.ToSlash(file.Path), "// source\n")
	}

	fragment, err := (jvmProvider{}).Build(context.Background(), Inventory{
		Root:  root,
		Files: files,
		Manifests: []string{
			"settings.gradle.kts",
			filepath.FromSlash("app/build.gradle.kts"),
			filepath.FromSlash("business/app_lock/build.gradle.kts"),
			filepath.FromSlash("modules/core/build.gradle.kts"),
			filepath.FromSlash("build-logic/settings.gradle.kts"),
		},
		Config: config.ProjectConfig{},
	})
	if err != nil {
		t.Fatal(err)
	}
	graph := MergeFragments(root, []Fragment{fragment})
	if graph.Coverage.Status != CoverageComplete {
		t.Fatalf("coverage = %q: %#v", graph.Coverage.Status, graph.Coverage.Issues)
	}

	rootID := gradleID("settings.gradle.kts", ":")
	appID := gradleID("settings.gradle.kts", ":app")
	lockID := gradleID("settings.gradle.kts", ":business:app_lock")
	coreID := gradleID("settings.gradle.kts", ":core")
	buildLogicID := gradleID(filepath.FromSlash("build-logic/settings.gradle.kts"), ":")
	for _, id := range []ID{rootID, appID, lockID, coreID, buildLogicID} {
		if _, ok := graph.Nodes[id]; !ok {
			t.Fatalf("missing Gradle node %q: %#v", id, graph.Nodes)
		}
	}
	if got := graph.Nodes[coreID].Root; got != filepath.FromSlash("modules/core") {
		t.Fatalf("core root = %q", got)
	}
	if got := graph.Members[coreID]; !reflect.DeepEqual(got, []string{
		filepath.FromSlash("modules/core/src/generated/java/Generated.java"),
		filepath.FromSlash("modules/core/src/main/java/Core.java"),
	}) {
		t.Fatalf("core members = %#v", got)
	}
	assertTopologyEdge(t, graph.Dependencies[appID], coreID, EdgeDependency, EdgeScope("implementation"))
	assertTopologyEdge(t, graph.Dependencies[appID], lockID, EdgeDependency, EdgeScope("testImplementation"))
	assertTopologyEdge(t, graph.Dependencies[rootID], buildLogicID, EdgeBuildBoundary, EdgeScope(""))
}

func TestGradleReportsDynamicAndEscapingDeclarations(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "settings.gradle.kts", `
val projectName = ":dynamic"
include(projectName)
include(":safe")
project(":safe").projectDir = file("../outside")
includeBuild(compositePath)
`)
	fragment, err := (jvmProvider{}).Build(context.Background(), Inventory{
		Root:      root,
		Manifests: []string{"settings.gradle.kts"},
	})
	if err != nil {
		t.Fatal(err)
	}
	graph := MergeFragments(root, []Fragment{fragment})
	if graph.Coverage.Status != CoveragePartial {
		t.Fatalf("coverage = %q, want partial", graph.Coverage.Status)
	}
	for _, code := range []string{"dynamic-gradle-include", "dynamic-gradle-include-build", "invalid-node-path"} {
		if !hasIssueCode(graph.Coverage.Issues, code) {
			t.Fatalf("issues = %#v, want %s", graph.Coverage.Issues, code)
		}
	}
	if _, ok := graph.Nodes[gradleID("settings.gradle.kts", ":safe")]; ok {
		t.Fatal("escaping projectDir node was retained")
	}
}

func TestGradleBuildsLegacyGroovyDeclarations(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "settings.gradle", `
rootProject.name = 'Demo'
include ':app', ':core'
includeBuild 'build-logic'
`)
	writeTopologyFixture(t, root, "app/build.gradle", `
dependencies {
    implementation project(':core')
}
sourceSets.main.java.srcDirs = ['src/generated/java']
sourceSets.test.java.srcDir 'src/integration/java'
`)
	writeTopologyFixture(t, root, "core/build.gradle", "")
	writeTopologyFixture(t, root, "build-logic/settings.gradle", "rootProject.name = 'build-logic'")
	files := []scanner.FileInfo{
		{Path: filepath.FromSlash("app/src/generated/java/Generated.java"), Ext: ".java"},
		{Path: filepath.FromSlash("app/src/integration/java/AppTest.java"), Ext: ".java"},
		{Path: filepath.FromSlash("core/src/main/kotlin/Core.kt"), Ext: ".kt"},
	}
	fragment, err := (jvmProvider{}).Build(context.Background(), Inventory{
		Root:  root,
		Files: files,
		Manifests: []string{
			"settings.gradle",
			filepath.FromSlash("app/build.gradle"),
			filepath.FromSlash("core/build.gradle"),
			filepath.FromSlash("build-logic/settings.gradle"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	graph := MergeFragments(root, []Fragment{fragment})
	appID := gradleID("settings.gradle", ":app")
	coreID := gradleID("settings.gradle", ":core")
	assertTopologyEdge(t, graph.Dependencies[appID], coreID, EdgeDependency, EdgeScope("implementation"))
	assertTopologyEdge(t, graph.Dependencies[gradleID("settings.gradle", ":")], gradleID(filepath.FromSlash("build-logic/settings.gradle"), ":"), EdgeBuildBoundary, EdgeScope(""))
	if got := graph.Members[appID]; !reflect.DeepEqual(got, []string{
		filepath.FromSlash("app/src/generated/java/Generated.java"),
		filepath.FromSlash("app/src/integration/java/AppTest.java"),
	}) {
		t.Fatalf("app members = %#v", got)
	}
}

func TestGradleAccessorMappingUsesIncludedProjectNames(t *testing.T) {
	got := gradleAccessorForProject(":business:app_lock")
	if got != "projects.business.appLock" {
		t.Fatalf("accessor = %q", got)
	}
}

func TestGradleCommentStripperPreservesURLLikeStringLiterals(t *testing.T) {
	line := `rootProject.name = "demo//test" // trailing comment`
	if got := stripGradleLineComment(line); got != `rootProject.name = "demo//test" ` {
		t.Fatalf("stripped line = %q", got)
	}
}

func TestGradleIgnoresBlockCommentsAndInterpolatedIncludes(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "settings.gradle.kts", `
include(":app", ":core")
/*
include(":commented")
*/
include(":${projectName}")
`)
	writeTopologyFixture(t, root, "app/build.gradle.kts", `
/*
implementation(project(":commented"))
*/
implementation(project(":core"))
`)
	fragment, err := (jvmProvider{}).Build(context.Background(), Inventory{
		Root: root, Manifests: []string{"settings.gradle.kts", "app/build.gradle.kts"},
	})
	if err != nil {
		t.Fatal(err)
	}
	graph := MergeFragments(root, []Fragment{fragment})
	if !hasIssueCode(graph.Coverage.Issues, "dynamic-gradle-include") {
		t.Fatalf("issues = %#v", graph.Coverage.Issues)
	}
	if _, ok := graph.Nodes[gradleID("settings.gradle.kts", ":commented")]; ok {
		t.Fatal("block-commented Gradle project was retained")
	}
	if _, ok := graph.Nodes[gradleID("settings.gradle.kts", ":${projectName}")]; ok {
		t.Fatal("interpolated Gradle project was retained")
	}
	assertTopologyEdge(t, graph.Dependencies[gradleID("settings.gradle.kts", ":app")], gradleID("settings.gradle.kts", ":core"), EdgeDependency, EdgeScope("implementation"))
}

func TestGradleReportsSharedAndMultilineDeclarations(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "settings.gradle.kts", `
include(":app", ":common")
include(
    ":multiline"
)
`)
	writeTopologyFixture(t, root, "build.gradle", `
subprojects {
    dependencies {
        implementation project(":common")
    }
}
`)
	fragment, err := (jvmProvider{}).Build(context.Background(), Inventory{
		Root: root, Manifests: []string{"settings.gradle.kts", "build.gradle"},
	})
	if err != nil {
		t.Fatal(err)
	}
	graph := MergeFragments(root, []Fragment{fragment})
	if graph.Coverage.Status != CoveragePartial {
		t.Fatalf("coverage = %q: %#v", graph.Coverage.Status, graph.Coverage.Issues)
	}
	for _, code := range []string{"dynamic-gradle-include", "dynamic-gradle-scope"} {
		if !hasIssueCode(graph.Coverage.Issues, code) {
			t.Fatalf("issues = %#v, want %s", graph.Coverage.Issues, code)
		}
	}
	if _, ok := graph.Nodes[gradleID("settings.gradle.kts", ":multiline")]; ok {
		t.Fatal("multiline Gradle project was retained")
	}
	for _, edge := range graph.Dependencies[gradleID("settings.gradle.kts", ":")] {
		if edge.Kind == EdgeDependency {
			t.Fatalf("shared Gradle block emitted a root dependency: %#v", edge)
		}
	}
}

func TestGradleFailsClosedForAmbiguousAccessors(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "settings.gradle.kts", `include(":app", ":foo_bar", ":foo-bar")`)
	writeTopologyFixture(t, root, "app/build.gradle.kts", `implementation(projects.fooBar)`)

	fragment, err := (jvmProvider{}).Build(context.Background(), Inventory{
		Root: root,
		Manifests: []string{
			"settings.gradle.kts",
			filepath.FromSlash("app/build.gradle.kts"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	graph := MergeFragments(root, []Fragment{fragment})
	if !hasIssueCode(graph.Coverage.Issues, "ambiguous-gradle-accessor") {
		t.Fatalf("issues = %#v", graph.Coverage.Issues)
	}
	appID := gradleID("settings.gradle.kts", ":app")
	for _, edge := range graph.Dependencies[appID] {
		if edge.Kind == EdgeDependency {
			t.Fatalf("ambiguous Gradle accessor emitted edge: %#v", edge)
		}
	}
}

func assertTopologyEdge(t *testing.T, edges []Edge, target ID, kind EdgeKind, scope EdgeScope) {
	t.Helper()
	for _, edge := range edges {
		if edge.To == target && edge.Kind == kind && edge.Scope == scope {
			return
		}
	}
	t.Fatalf("edge to %q kind=%q scope=%q missing from %#v", target, kind, scope, edges)
}

func TestJVMProviderMetadata(t *testing.T) {
	provider := jvmProvider{}
	if provider.Name() != "jvm" || provider.Version() == "" {
		t.Fatalf("provider metadata = %q %q", provider.Name(), provider.Version())
	}
	if got := provider.Manifests().Names; !reflect.DeepEqual(got, []string{
		"build.gradle",
		"build.gradle.kts",
		"build.sbt",
		"pom.xml",
		"settings.gradle",
		"settings.gradle.kts",
	}) {
		t.Fatalf("manifest names = %#v", got)
	}
}
