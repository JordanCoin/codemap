package topology

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPythonProviderBuildsModernWorkspaceTopology(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "pyproject.toml", `
[project]
name = "app"
dependencies = ["lib>=1", "shared", "marker-lib; sys_platform == 'darwin'"]

[project.optional-dependencies]
test = ["extra-lib"]

[tool.uv.workspace]
members = ["packages/*", "libs/*"]

[tool.uv.sources]
lib = { workspace = true }
marker-lib = { workspace = true }
extra-lib = { workspace = true }
shared = [
  { path = "libs/shared", marker = "sys_platform == 'darwin'" }
]
`)
	writeTopologyFixture(t, root, "packages/lib/pyproject.toml", `
[project]
name = "lib"
`)
	writeTopologyFixture(t, root, "packages/marker/pyproject.toml", `
[project]
name = "marker-lib"
`)
	writeTopologyFixture(t, root, "packages/extra/pyproject.toml", `
[project]
name = "extra-lib"
`)
	writeTopologyFixture(t, root, "libs/shared/pyproject.toml", `
[tool.poetry]
name = "shared"
`)
	writeTopologyFixture(t, root, "src/app/__init__.py", "")
	writeTopologyFixture(t, root, "packages/lib/src/lib/__init__.py", "")
	writeTopologyFixture(t, root, "packages/lib/tests/test_lib.py", "")
	writeTopologyFixture(t, root, "libs/shared/shared/__init__.py", "")

	graph, _, err := BuildGraphWithProviders(context.Background(), root, []Provider{pythonProvider{}})
	if err != nil {
		t.Fatal(err)
	}

	app := ID("python:pyproject.toml:app")
	lib := ID("python:packages/lib/pyproject.toml:lib")
	marker := ID("python:packages/marker/pyproject.toml:marker-lib")
	extra := ID("python:packages/extra/pyproject.toml:extra-lib")
	shared := ID("python:libs/shared/pyproject.toml:shared")
	if got := sortedNodeIDs(graph.Nodes); !reflect.DeepEqual(got, []ID{shared, extra, lib, marker, app}) {
		t.Fatalf("nodes = %#v", got)
	}
	if got := graph.Dependencies[app]; len(got) != 4 ||
		got[0].To != shared || !got[0].Conditional ||
		got[1].To != extra || !got[1].Conditional ||
		got[2].To != lib || got[2].Conditional ||
		got[3].To != marker || !got[3].Conditional {
		t.Fatalf("app dependencies = %#v", got)
	}
	assertPythonOwner(t, graph, "src/app/__init__.py", app)
	assertPythonOwner(t, graph, "packages/lib/src/lib/__init__.py", lib)
	assertPythonOwner(t, graph, "packages/lib/tests/test_lib.py", lib)
	assertPythonOwner(t, graph, "libs/shared/shared/__init__.py", shared)
	if graph.Coverage.Status != CoverageComplete {
		t.Fatalf("coverage = %#v", graph.Coverage)
	}
}

func TestPythonProviderDoesNotGuessDeclaredPackageLayouts(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "pyproject.toml", `
[project]
name = "myproj"

[tool.setuptools]
py-modules = ["standalone_module"]
`)
	writeTopologyFixture(t, root, "standalone_module.py", "")
	writeTopologyFixture(t, root, "src/unrelated_scratch/scratch.py", "")

	graph, _, err := BuildGraphWithProviders(context.Background(), root, []Provider{pythonProvider{}})
	if err != nil {
		t.Fatal(err)
	}
	node := graph.Nodes[ID("python:pyproject.toml:myproj")]
	if len(node.SourceRoots) != 0 || len(graph.Members[node.ID]) != 0 {
		t.Fatalf("declared flat layout was guessed: node = %#v, members = %#v", node, graph.Members[node.ID])
	}
	if graph.Coverage.Status != CoveragePartial || !hasIssueCode(graph.Coverage.Issues, "unsupported-package-layout") {
		t.Fatalf("coverage = %#v, want partial unsupported-package-layout", graph.Coverage)
	}
}

func TestPythonProviderUsesDeclaredHatchPackageRoot(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "packages/service/pyproject.toml", `
[project]
name = "myproj"

[tool.hatch.build.targets.wheel]
packages = ["lib/actualpkg"]
`)
	writeTopologyFixture(t, root, "packages/service/lib/actualpkg/__init__.py", "")
	writeTopologyFixture(t, root, "packages/service/src/unrelated_scratch/scratch.py", "")

	graph, _, err := BuildGraphWithProviders(context.Background(), root, []Provider{pythonProvider{}})
	if err != nil {
		t.Fatal(err)
	}
	node := graph.Nodes[ID("python:packages/service/pyproject.toml:myproj")]
	if len(node.SourceRoots) != 1 || node.SourceRoots[0] != filepath.FromSlash("packages/service/lib/actualpkg") {
		t.Fatalf("source roots = %#v", node.SourceRoots)
	}
	assertPythonOwner(t, graph, "packages/service/lib/actualpkg/__init__.py", node.ID)
	if graph.Owners[filepath.FromSlash("packages/service/src/unrelated_scratch/scratch.py")] != nil {
		t.Fatalf("unrelated source was assigned: %#v", graph.Owners)
	}
	if graph.Coverage.Status != CoveragePartial || !hasIssueCode(graph.Coverage.Issues, "unsupported-package-layout") {
		t.Fatalf("coverage = %#v, want partial unsupported-package-layout", graph.Coverage)
	}
}

func TestPythonProviderUsesDeclaredSetuptoolsPackageRoot(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "pyproject.toml", `
[project]
name = "myproj"

[tool.setuptools]
package-dir = {"" = "src"}

[tool.setuptools.packages.find]
where = ["src"]
`)
	writeTopologyFixture(t, root, "src/myproj/__init__.py", "")
	writeTopologyFixture(t, root, "myproj/unrelated.py", "")

	graph, _, err := BuildGraphWithProviders(context.Background(), root, []Provider{pythonProvider{}})
	if err != nil {
		t.Fatal(err)
	}
	node := graph.Nodes[ID("python:pyproject.toml:myproj")]
	if !reflect.DeepEqual(node.SourceRoots, []string{filepath.FromSlash("src")}) {
		t.Fatalf("source roots = %#v", node.SourceRoots)
	}
	assertPythonOwner(t, graph, "src/myproj/__init__.py", node.ID)
	if graph.Owners[filepath.FromSlash("myproj/unrelated.py")] != nil {
		t.Fatalf("unrelated source was assigned: %#v", graph.Owners)
	}
}

func TestPythonProviderRejectsManifestSymlinkOutsideRoot(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	writeTopologyFixture(t, outside, "pyproject.toml", "[project]\nname = \"external\"\n")
	if err := os.Symlink(filepath.Join(outside, "pyproject.toml"), filepath.Join(root, "pyproject.toml")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	graph, _, err := BuildGraphWithProviders(context.Background(), root, []Provider{pythonProvider{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 0 || graph.Coverage.Status == CoverageComplete || !hasIssueCode(graph.Coverage.Issues, "invalid-manifest-path") {
		t.Fatalf("external manifest was trusted: %#v", graph)
	}
}

func TestPythonProviderResolvesPoetryPathsAndRejectsUnsafeOrAmbiguousSources(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, ".gitignore", "ignored/\n")
	writeTopologyFixture(t, root, "pyproject.toml", `
[project]
name = "app"
dependencies = ["dupe-name", "escape", "missing", "absolute", "ignored"]

[tool.poetry]
name = "different-app"

[tool.uv.workspace]
members = ["packages/*"]

[tool.uv.sources]
"dupe.name" = { workspace = true }
escape = { path = "../outside" }
missing = { path = "missing" }
absolute = { path = "/tmp/absolute" }
ignored = { path = "ignored" }
`)
	writeTopologyFixture(t, root, "packages/a/pyproject.toml", "[project]\nname = \"dupe_name\"\n")
	writeTopologyFixture(t, root, "packages/b/pyproject.toml", "[project]\nname = \"dupe-name\"\n")
	writeTopologyFixture(t, root, "packages/poetry/pyproject.toml", `
[tool.poetry]
name = "poetry-lib"

[tool.poetry.dependencies]
python = "^3.12"
app = { path = "../..", markers = "python_version >= '3.12'" }
`)
	writeTopologyFixture(t, root, "ignored/pyproject.toml", "[project]\nname = \"ignored\"\n")
	writeTopologyFixture(t, root, "packages/poetry/poetry_lib/__init__.py", "")

	graph, _, err := BuildGraphWithProviders(context.Background(), root, []Provider{pythonProvider{}})
	if err != nil {
		t.Fatal(err)
	}

	app := ID("python:pyproject.toml:app")
	poetry := ID("python:packages/poetry/pyproject.toml:poetry-lib")
	if got := graph.Dependencies[app]; len(got) != 0 {
		t.Fatalf("unsafe or ambiguous dependencies resolved: %#v", got)
	}
	if got := graph.Dependencies[poetry]; len(got) != 1 || got[0].To != app || !got[0].Conditional {
		t.Fatalf("Poetry dependencies = %#v", got)
	}
	assertPythonOwner(t, graph, "packages/poetry/poetry_lib/__init__.py", poetry)
	for _, code := range []string{
		"ambiguous-local-dependency",
		"conflicting-project-name",
		"invalid-local-source",
		"missing-local-source",
	} {
		if !hasIssueCode(graph.Coverage.Issues, code) {
			t.Fatalf("issues = %#v, want %q", graph.Coverage.Issues, code)
		}
	}
	if graph.Coverage.Status != CoveragePartial {
		t.Fatalf("coverage = %#v", graph.Coverage)
	}
}

func TestPythonProviderKeepsMalformedAndDynamicProjectsPartial(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, "pyproject.toml", `
[project]
name = "root-project"
dynamic = ["dependencies"]
`)
	writeTopologyFixture(t, root, "broken/pyproject.toml", "[project\nname = ")
	writeTopologyFixture(t, root, "src/root_project/__init__.py", "")

	graph, _, err := BuildGraphWithProviders(context.Background(), root, []Provider{pythonProvider{}})
	if err != nil {
		t.Fatal(err)
	}
	rootID := ID("python:pyproject.toml:root-project")
	if _, ok := graph.Nodes[rootID]; !ok {
		t.Fatalf("valid project missing: %#v", graph.Nodes)
	}
	assertPythonOwner(t, graph, "src/root_project/__init__.py", rootID)
	if !hasIssueCode(graph.Coverage.Issues, "dynamic-dependencies") ||
		!hasIssueCode(graph.Coverage.Issues, "malformed-manifest") {
		t.Fatalf("coverage issues = %#v", graph.Coverage.Issues)
	}
}

func TestPythonProviderOnlyLanguageIncludesPythonFiles(t *testing.T) {
	root := t.TempDir()
	writeTopologyFixture(t, root, ".codemap/config.json", `{"only":["py"]}`)
	writeTopologyFixture(t, root, "pyproject.toml", "[project]\nname = \"app\"\n")
	writeTopologyFixture(t, root, "src/app/__init__.py", "")

	graph, _, err := BuildGraphWithProviders(context.Background(), root, []Provider{pythonProvider{}})
	if err != nil {
		t.Fatal(err)
	}
	assertPythonOwner(t, graph, "src/app/__init__.py", ID("python:pyproject.toml:app"))
}

func TestNormalizePythonProjectName(t *testing.T) {
	for input, want := range map[string]string{
		"Foo.Bar_baz": "foo-bar-baz",
		"plain":       "plain",
		"  Mixed--_":  "mixed",
	} {
		if got := normalizePythonProjectName(input); got != want {
			t.Fatalf("normalizePythonProjectName(%q) = %q, want %q", input, got, want)
		}
	}
}

func assertPythonOwner(t *testing.T, graph *Graph, path string, want ID) {
	t.Helper()
	path = filepath.Clean(path)
	if got := graph.Owners[path]; !reflect.DeepEqual(got, []ID{want}) {
		t.Fatalf("owners[%q] = %#v, want %#v", path, got, []ID{want})
	}
}
