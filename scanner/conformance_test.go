package scanner

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"

	"codemap/analysis"
)

// conformanceCase is the smallest repository in one language where a→b is
// the idiomatic intra-project reference. file-level languages must produce
// the edge and stay complete; symbol-level languages must be declared, not
// silently labelled complete with an empty graph.
type conformanceCase struct {
	files    map[string]string
	from, to string
	edge     bool                    // the a→b edge must exist
	status   analysis.CoverageStatus // expected EffectiveStatus
}

var conformanceCases = map[string]conformanceCase{
	"go":         {map[string]string{"go.mod": "module mod\n", "main.go": "package main\nimport \"mod/b\"\nfunc main(){ b.B() }\n", "b/b.go": "package b\nfunc B(){}\n"}, "main.go", "b/b.go", true, analysis.CoverageComplete},
	"python":     {map[string]string{"a.py": "from b import x\n", "b.py": "x = 1\n"}, "a.py", "b.py", true, analysis.CoverageComplete},
	"javascript": {map[string]string{"a.js": "import { x } from './b';\n", "b.js": "export const x = 1;\n"}, "a.js", "b.js", true, analysis.CoverageComplete},
	"typescript": {map[string]string{"a.ts": "import { x } from './b';\n", "b.ts": "export const x = 1;\n"}, "a.ts", "b.ts", true, analysis.CoverageComplete},
	"rust":       {map[string]string{"src/main.rs": "mod b;\nfn main(){ b::f() }\n", "src/b.rs": "pub fn f(){}\n"}, "src/main.rs", "src/b.rs", true, analysis.CoveragePartial},
	"ruby":       {map[string]string{"a.rb": "require_relative 'b'\n", "b.rb": "X = 1\n"}, "a.rb", "b.rb", true, analysis.CoverageComplete},
	"c":          {map[string]string{"a.c": "#include \"b.h\"\nint main(){return f();}\n", "b.h": "int f(void);\n"}, "a.c", "b.h", true, analysis.CoverageComplete},
	"cpp":        {map[string]string{"a.cpp": "#include \"b.hpp\"\nint main(){return f();}\n", "b.hpp": "int f();\n"}, "a.cpp", "b.hpp", true, analysis.CoverageComplete},
	"java":       {map[string]string{"app/A.java": "package app;\npublic class Alpha { Widget b; }\n", "app/B.java": "package app;\npublic class Widget {}\n"}, "app/A.java", "app/B.java", false, analysis.CoveragePartial},
	"swift":      {map[string]string{"A.swift": "struct Alpha { let b: Widget }\n", "B.swift": "struct Widget {}\n"}, "A.swift", "B.swift", true, analysis.CoveragePartial},
	"dart":       {map[string]string{"lib/a.dart": "import 'b.dart';\n", "lib/b.dart": "int x = 1;\n"}, "lib/a.dart", "lib/b.dart", true, analysis.CoverageComplete},
	"bash":       {map[string]string{"a.sh": "#!/bin/bash\nsource ./b.sh\n", "b.sh": "x=1\n"}, "a.sh", "b.sh", true, analysis.CoverageComplete},
	"kotlin":     {map[string]string{"app/A.kt": "package app\nclass Alpha(val b: Widget)\n", "app/B.kt": "package app\nclass Widget\n"}, "app/A.kt", "app/B.kt", false, analysis.CoveragePartial},
	"csharp":     {map[string]string{"A.cs": "namespace App { class Alpha { Widget b; } }\n", "B.cs": "namespace App { class Widget {} }\n"}, "A.cs", "B.cs", false, analysis.CoveragePartial},
	"php":        {map[string]string{"a.php": "<?php\nrequire_once 'b.php';\n", "b.php": "<?php\n$x = 1;\n"}, "a.php", "b.php", true, analysis.CoverageComplete},
	"lua":        {map[string]string{"a.lua": "local b = require('b')\n", "b.lua": "return {}\n"}, "a.lua", "b.lua", true, analysis.CoverageComplete},
	"scala":      {map[string]string{"app/A.scala": "package app\nclass Alpha(b: Widget)\n", "app/B.scala": "package app\nclass Widget\n"}, "app/A.scala", "app/B.scala", false, analysis.CoveragePartial},
	"elixir":     {map[string]string{"lib/a.ex": "defmodule App.A do\n  alias App.B\n  def f, do: B.g()\nend\n", "lib/b.ex": "defmodule App.B do\n  def g, do: 1\nend\n"}, "lib/a.ex", "lib/b.ex", false, analysis.CoveragePartial},
	"solidity":   {map[string]string{"A.sol": "pragma solidity ^0.8.0;\nimport \"./B.sol\";\ncontract A is B {}\n", "B.sol": "pragma solidity ^0.8.0;\ncontract B {}\n"}, "A.sol", "B.sol", true, analysis.CoverageComplete},
	"cue":        {map[string]string{"cue.mod/module.cue": "module: \"mod\"\n", "a.cue": "package a\nimport \"mod/b\"\nx: b.y\n", "b/b.cue": "package b\ny: 1\n"}, "a.cue", "b/b.cue", true, analysis.CoverageComplete},
}

// Every language the scanner claims to detect gets one conformance case, so
// adding an extension without a case fails here instead of in a user's issue.
func TestConformanceCoversEveryDetectedLanguage(t *testing.T) {
	var missing []string
	for _, lang := range extToLang {
		if _, ok := conformanceCases[lang]; !ok && !slices.Contains(missing, lang) {
			missing = append(missing, lang)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("languages in extToLang with no conformance case: %v", missing)
	}
}

func TestConformanceResolvesEachLanguage(t *testing.T) {
	for lang, c := range conformanceCases {
		t.Run(lang, func(t *testing.T) {
			dir := t.TempDir()
			for name, body := range c.files {
				p := filepath.Join(dir, name)
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
			if got := slices.Contains(g.Imports[c.from], c.to); got != c.edge {
				t.Fatalf("edge %s -> %s = %v, want %v (imports = %v)", c.from, c.to, got, c.edge, g.Imports[c.from])
			}
			if got := g.Coverage.EffectiveStatus(); got != c.status {
				t.Fatalf("coverage = %v, want %v (notes = %v)", got, c.status, g.Coverage.Notes)
			}
		})
	}
}
