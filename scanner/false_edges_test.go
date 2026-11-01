package scanner

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func falseEdgeGraph(t *testing.T, files map[string]string) *FileGraph {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	graph, err := BuildFileGraph(context.Background(), dir, Filters{})
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}
	return graph
}

func assertImports(t *testing.T, graph *FileGraph, from string, want ...string) {
	t.Helper()
	got := slices.Clone(graph.Imports[from])
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("%s imports = %v, want %v", from, got, want)
	}
}

func TestCAngleIncludeOfStandardHeaderStaysExternal(t *testing.T) {
	graph := falseEdgeGraph(t, map[string]string{
		"src/fs.c":           "#include <errno.h>\n#include <uv/errno.h>\n#include <uv.h>\n",
		"include/uv.h":       "#include <errno.h>\n",
		"include/uv/errno.h": "int e;\n",
	})
	assertImports(t, graph, "src/fs.c", "include/uv/errno.h", "include/uv.h")
	assertImports(t, graph, "include/uv.h")
}

func TestCQuotedIncludeFindsSameNamedProjectHeader(t *testing.T) {
	graph := falseEdgeGraph(t, map[string]string{
		"src/compat.c": "#include \"errno.h\"\n",
		"src/errno.h":  "int e;\n",
		"src/both.c":   "#ifdef A\n#include <errno.h>\n#else\n#include \"errno.h\"\n#endif\n",
	})
	assertImports(t, graph, "src/compat.c", "src/errno.h")
	assertImports(t, graph, "src/both.c", "src/errno.h")
}

func TestCppExtensionlessIncludeNeverGainsAnExtension(t *testing.T) {
	graph := falseEdgeGraph(t, map[string]string{
		"absl/strings/str.h":     "#include <memory>\n#include <utility>\n#include <absl/memory/memory.h>\n#include \"absl/utility/utility.h\"\n",
		"absl/memory/memory.h":   "int m;\n",
		"absl/utility/utility.h": "int u;\n",
		"memory/other.cc":        "int o;\n",
	})
	assertImports(t, graph, "absl/strings/str.h", "absl/memory/memory.h", "absl/utility/utility.h")
}

func TestCQuotedIncludePrefersIncluderDirectoryThenClosest(t *testing.T) {
	graph := falseEdgeGraph(t, map[string]string{
		"internal.h":          "int r;\n",
		"src/unix/internal.h": "int u;\n",
		"src/win/internal.h":  "int w;\n",
		"src/unix/process.c":  "#include \"internal.h\"\n",
		"src/unix/sub/deep.c": "#include \"internal.h\"\n",
		"src/common.c":        "#include \"../src/win/internal.h\"\n",
		"lib/a/x.c":           "#include \"thing.h\"\n",
		"lib/a/b/thing.h":     "int near;\n",
		"other/thing.h":       "int far;\n",
		"lib/y.c":             "#include \"plat.h\"\n",
		"lib/win/plat.h":      "int w;\n",
		"lib/unix/plat.h":     "int u;\n",
		"tool/z.c":            "#include \"sdk/api.h\"\n",
		"sdk/api.h":           "int root;\n",
		"vendor/sdk/api.h":    "int vendored;\n",
	})
	assertImports(t, graph, "src/unix/process.c", "src/unix/internal.h")
	assertImports(t, graph, "src/unix/sub/deep.c", "src/unix/internal.h")
	assertImports(t, graph, "src/common.c", "src/win/internal.h")
	assertImports(t, graph, "lib/a/x.c", "lib/a/b/thing.h")
	// No way to pick without the -I order: an edge to each is the honest answer.
	assertImports(t, graph, "lib/y.c", "lib/win/plat.h", "lib/unix/plat.h")
	assertImports(t, graph, "tool/z.c", "sdk/api.h")
}

func TestCIncludeOfDifferentlyCasedNameIsNotTheIncluder(t *testing.T) {
	graph := falseEdgeGraph(t, map[string]string{
		"include/freertos.h": "#include \"FreeRTOS.h\"\n",
		"src/a.c":            "#include \"FreeRTOS.h\"\n",
	})
	assertImports(t, graph, "include/freertos.h")
	assertImports(t, graph, "src/a.c")
}

func TestFileNeverImportsItself(t *testing.T) {
	graph := falseEdgeGraph(t, map[string]string{
		"pkg/__init__.py": "from pkg import helper\nimport pkg\n",
		"pkg/helper.py":   "x = 1\n",
		"a.h":             "#include \"a.h\"\n",
		"lua/mod.lua":     "local m = require(\"mod\")\n",
	})
	assertImports(t, graph, "a.h")
	assertImports(t, graph, "lua/mod.lua")
	if slices.Contains(graph.Imports["pkg/__init__.py"], "pkg/__init__.py") {
		t.Fatalf("self edge: %v", graph.Imports["pkg/__init__.py"])
	}
}

func TestLuaModuleResolvesThroughPackagePathBeforeSuffix(t *testing.T) {
	graph := falseEdgeGraph(t, map[string]string{
		"plugin/telescope.lua":            "local t = require(\"telescope\")\n",
		"lua/telescope/init.lua":          "local u = require(\"telescope.utils\")\nlocal g = require(\"telescope.actions\")\n",
		"lua/telescope/utils.lua":         "return {}\n",
		"lua/telescope/actions/init.lua":  "return {}\n",
		"lua/telescope/actions/other.lua": "return {}\n",
		"app/main.lua":                    "local s = require(\"src.nested.mod\")\nlocal r = require(\"nested.mod\")\n",
		"src/nested/mod.lua":              "return {}\n",
	})
	assertImports(t, graph, "plugin/telescope.lua", "lua/telescope/init.lua")
	assertImports(t, graph, "lua/telescope/init.lua", "lua/telescope/utils.lua", "lua/telescope/actions/init.lua")
	assertImports(t, graph, "app/main.lua", "src/nested/mod.lua")
}
