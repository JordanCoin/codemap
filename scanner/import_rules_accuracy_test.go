package scanner

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func buildFixtureGraph(t *testing.T, files map[string]string) *FileGraph {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	g, err := BuildFileGraph(context.Background(), dir, Filters{})
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}
	return g
}

func assertEdges(t *testing.T, g *FileGraph, from string, want []string) {
	t.Helper()
	got := slices.Clone(g.Imports[from])
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("%s imports = %v, want %v", from, got, want)
	}
}

func TestLuaRequireForms(t *testing.T) {
	g := buildFixtureGraph(t, map[string]string{
		"lua/app/init.lua": "local a = require \"app.dq\"\nlocal b = require 'app.sq'\nlocal function f()\n  return require(\"app.pkg\").go()\nend\ndofile \"lua/app/run.lua\"\nlocal t = require { \"app.table\" }\n",
		"lua/app/dq.lua":   "return {}\n",
		"lua/app/sq.lua":   "local app = require \"app\"\nreturn {}\n",
		"lua/app/run.lua":  "return {}\n",
		// Nested so the table-call case above would have a target if it were captured.
		"lua/app/table.lua":    "return {}\n",
		"lua/app/pkg/init.lua": "return {}\n",
		"plugin/app.lua":       "return {}\n",
		"lua/app/ext.lua":      "local j = require \"cjson\"\nlocal r = require \"resty.http\"\n",
		"lua/app/x/cjson.lua":  "return {}\n",
		"lua/app/http/a.lua":   "return {}\n",
		"lua/app/http/b.lua":   "return {}\n",
	})
	assertEdges(t, g, "lua/app/init.lua", []string{"lua/app/dq.lua", "lua/app/sq.lua", "lua/app/pkg/init.lua", "lua/app/run.lua"})
	// lua/ wins over plugin/app.lua, which a suffix match would have picked.
	assertEdges(t, g, "lua/app/sq.lua", []string{"lua/app/init.lua"})
	// External libraries must not land on a same-named file or directory.
	assertEdges(t, g, "lua/app/ext.lua", nil)
}

func TestRubyAutoloadForms(t *testing.T) {
	g := buildFixtureGraph(t, map[string]string{
		"lib/app.rb":       "module App\n  autoload :Cache, \"app/cache\"\n  autoload(:Store, 'app/store')\n  autoload :Bare\nend\n",
		"lib/app/cache.rb": "module App; class Cache; end; end\n",
		"lib/app/store.rb": "module App; class Store; end; end\n",
		"lib/app/bare.rb":  "module App; class Bare; end; end\n",
	})
	assertEdges(t, g, "lib/app.rb", []string{"lib/app/cache.rb", "lib/app/store.rb"})
}

func TestBashLoadResolvesRelativeToFile(t *testing.T) {
	g := buildFixtureGraph(t, map[string]string{
		"test/a.bats":         "load 'helper'\nload sub/util\nload \"$BATS_TEST_DIRNAME/dyn\"\nload onlysh\n",
		"test/helper.bash":    "x=1\n",
		"test/helper.sh":      "x=1\n",
		"helper.bash":         "x=1\n",
		"test/sub/util.bash":  "x=1\n",
		"test/dyn.bash":       "x=1\n",
		"test/onlysh.sh":      "x=1\n",
		"scripts/run.sh":      "load ../test/helper.bash\n",
		"scripts/unrelate.sh": "load missing\n",
	})
	assertEdges(t, g, "test/a.bats", []string{"test/helper.bash", "test/sub/util.bash", "test/dyn.bash"})
	assertEdges(t, g, "scripts/run.sh", nil)
	assertEdges(t, g, "scripts/unrelate.sh", nil)
}

func TestBashDynamicPrefixNeedsUniqueMatch(t *testing.T) {
	g := buildFixtureGraph(t, map[string]string{
		"bin/run.sh": "DIR=.; ROOT=.; LIBDIR=lib\n" +
			"source \"${DIR}/lib/d.sh\"\n" +
			"source \"$(dirname \"${BASH_SOURCE[0]}\")/../lib/e.sh\"\n" +
			". \"$ROOT/$LIBDIR/core/f.bash\"\n" +
			"source \"$(cd \"$(dirname \"$0\")\" && pwd)/g.sh\"\n" +
			"source `dirname $0`/h.sh\n" +
			"source \"${DIR}/lib/twice.sh\"\n" +
			"source \"$HOME/lib/home.sh\"\n" +
			"source \"$DIR/lib/$NAME.sh\"\n" +
			"source \"$FILE\"\n",
		"lib/d.sh":        "x=1\n",
		"lib/e.sh":        "x=1\n",
		"lib/core/f.bash": "x=1\n",
		"bin/g.sh":        "x=1\n",
		"bin/h.sh":        "x=1\n",
		"a/lib/twice.sh":  "x=1\n",
		"b/lib/twice.sh":  "x=1\n",
		"lib/home.sh":     "x=1\n",
	})
	assertEdges(t, g, "bin/run.sh", []string{"lib/d.sh", "lib/e.sh", "lib/core/f.bash", "bin/g.sh", "bin/h.sh"})
}

func TestBashWordSegmentsKeepSubstitutionsWhole(t *testing.T) {
	want := []string{"${BASH_SOURCE[0]%/*}", "$(cd /a/b)", "lib", "x y.sh"}
	if got := bashWordSegments(` "${BASH_SOURCE[0]%/*}/$(cd /a/b)/lib/x y.sh" extra`); !slices.Equal(got, want) {
		t.Fatalf("segments = %q, want %q", got, want)
	}
}

func TestCUEHeaderSkipsLeadingAttributes(t *testing.T) {
	pkg, imports := cueHeader([]byte("// c\n@experiment(aliasv2)\n@if(foo(bar))\npackage ci\n\n@extern(x)\nimport (\n\t\"m/a\"\n)\nimport \"m/b\"\nx: 1\n"))
	if pkg != "ci" || !slices.Equal(imports, []string{"m/a", "m/b"}) {
		t.Fatalf("pkg = %q imports = %v", pkg, imports)
	}
}

func TestSwiftTypeNameAcceptsPackageModifier(t *testing.T) {
	for text, want := range map[string]string{
		"package final class Lock {}":      "Lock",
		"package struct Box {}":            "Box",
		"nonisolated struct Plain {}":      "Plain",
		"@usableFromInline package enum E": "E",
		"public class func make() {}":      "",
	} {
		if got := extractTypeName(text); got != want {
			t.Errorf("extractTypeName(%q) = %q, want %q", text, got, want)
		}
	}
}
