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
