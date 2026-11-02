package scanner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codemap/analysis"
)

func TestLuaDotModuleNamesNoFile(t *testing.T) {
	graph := falseEdgeGraph(t, map[string]string{
		"init.lua": "x = 1\n",
		"s/a.lua":  "require(\"..\")\n",
		"b.lua":    "require(\".\")\n",
	})
	assertImports(t, graph, "s/a.lua")
	assertImports(t, graph, "b.lua")
}

func TestImportsStayInsideScanRoot(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"outside.h":         "int x;\n",
		"outside.lua":       "x = 1\n",
		"outside.ts":        "export const x = 1\n",
		"outside.dart":      "int x = 1;\n",
		"src/a.c":           "#include \"../outside.h\"\n",
		"src/b.lua":         "require(\"../outside\")\n",
		"src/c.ts":          "import x from \"@o/outside\"\n",
		"src/d.dart":        "import '../outside.dart';\n",
		"src/tsconfig.json": `{"compilerOptions":{"baseUrl":".","paths":{"@o/*":["../*"]}}}`,
	} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	graph, err := BuildFileGraph(context.Background(), filepath.Join(dir, "src"), Filters{})
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"a.c", "b.lua", "c.ts", "d.dart"} {
		assertImports(t, graph, file)
	}
}

func TestRootDirectoryImportFollowsPackageMain(t *testing.T) {
	files := map[string]string{
		"index.js": "x = 1\n",
		"lib/m.js": "x = 1\n",
		"s/t.js":   "import x from '../'\n",
		"s/u.js":   "require('..')\n",
		"v.js":     "require('.')\n",
	}
	withMain := map[string]string{"package.json": `{"name":"p","main":"lib/m.js"}`}
	for name, body := range files {
		withMain[name] = body
	}
	graph := falseEdgeGraph(t, withMain)
	for _, file := range []string{"s/t.js", "s/u.js", "v.js"} {
		assertImports(t, graph, file, "lib/m.js")
	}
	graph = falseEdgeGraph(t, files)
	assertImports(t, graph, "s/t.js", "index.js")
}

func TestBashLoadBelongsToBats(t *testing.T) {
	graph := falseEdgeGraph(t, map[string]string{
		"a.sh":               "load() { echo; }\nload config\nload data.sh\n",
		"b.bash":             "load() { echo; }\nload config\n",
		"config.bash":        "x=1\n",
		"data.sh":            "x=1\n",
		"t/a.bats":           "load helper\n",
		"t/helper.bash":      "load more\n",
		"t/more.bash":        "x=1\n",
		"t/setup_suite.bash": "load suite\n",
		"t/suite.bash":       "x=1\n",
	})
	assertImports(t, graph, "a.sh")
	assertImports(t, graph, "b.bash")
	assertImports(t, graph, "t/a.bats", "t/helper.bash")
	assertImports(t, graph, "t/helper.bash", "t/more.bash")
	assertImports(t, graph, "t/setup_suite.bash", "t/suite.bash")
}

func TestBashDynamicPrefixMustPointIntoRepo(t *testing.T) {
	graph := falseEdgeGraph(t, map[string]string{
		"a.sh": ". \"$NVM_DIR/nvm.sh\"\nsource \"$(brew --prefix)/brew.sh\"\n" +
			"source \"$ASSIGNED/lib/x.sh\"\nsource \"${BASH_SOURCE%/*}/y.sh\"\nsource \"$(dirname \"$0\")/../z.sh\"\n" +
			"source \"$FROM_LAUNCHER/w.sh\"\n",
		"b.sh":           "export ASSIGNED=\"$PWD\"\n",
		"bin/launch":     "#!/usr/bin/env bash\nFROM_LAUNCHER=.\n",
		"vendor2/nvm.sh": "x=1\n",
		"sub/brew.sh":    "x=1\n",
		"lib/x.sh":       "x=1\n",
		"y.sh":           "x=1\n",
		"z.sh":           "x=1\n",
		"w.sh":           "x=1\n",
	})
	assertImports(t, graph, "a.sh", "lib/x.sh", "y.sh", "z.sh", "w.sh")
}

func TestBashSingleQuotedDollarIsLiteral(t *testing.T) {
	graph := falseEdgeGraph(t, map[string]string{
		"a.sh": "X=1\nsource '$X'/b.sh\nsource \\$X/b.sh\n",
		"b.sh": "x=1\n",
	})
	assertImports(t, graph, "a.sh")
}

func TestDynamicShellWordsStayOutOfVisibleImports(t *testing.T) {
	dir := t.TempDir()
	body := "DIR=.\nsource \"$(pwd)/nope.sh\"\n. \"$X/bad.bash\"\nsource \"${DIR}/lib/b.sh\"\nsource ./lib/b.sh\n"
	if err := os.WriteFile(filepath.Join(dir, "a.sh"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	outcome, err := scanForDepsPrimaryOutcome(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.Analyses) != 1 || len(outcome.Analyses[0].Imports) != 1 || outcome.Analyses[0].Imports[0] != "./lib/b.sh" {
		t.Fatalf("analyses = %+v, want only the literal import", outcome.Analyses)
	}
	graph := falseEdgeGraph(t, map[string]string{"a.sh": "DIR=.\nsource \"${DIR}/lib/b.sh\"\n", "lib/b.sh": "x=1\n"})
	assertImports(t, graph, "a.sh", "lib/b.sh")
}

func TestWeakEvidenceDoesNotLowerCoverage(t *testing.T) {
	for name, tc := range map[string]struct {
		file, body string
		want       analysis.CoverageStatus
	}{
		"string":               {"a.sh", "echo \"go; source $x\"\n", analysis.CoverageComplete},
		"comment":              {"a.sh", "true # ; source $x\n", analysis.CoverageComplete},
		"heredoc":              {"a.sh", "cat <<'EOF'\nsource $x\nEOF\n", analysis.CoverageComplete},
		"unterminated heredoc": {"a.sh", "cat <<EOF\nsource \"$x\"\n", analysis.CoveragePartial},
		"escaped":              {"a.sh", ". \\$x\n", analysis.CoverageComplete},
		"load in sh":           {"a.sh", "load \"$x\"\n", analysis.CoverageComplete},
		"global use":           {"a.php", "<?php\nuse DateTime;\n", analysis.CoverageComplete},
		"docblock":             {"a.php", "<?php\n/**\n namespace Foo;\n*/\n", analysis.CoverageComplete},
		"ruby string":          {"a.rb", "puts \"Zeitwerk::Loader\"\n", analysis.CoverageComplete},
		"real if":              {"a.sh", "if x; then source \"$y\"; fi\n", analysis.CoveragePartial},
		"real and":             {"a.sh", "[ -f z ] && . \"$w\"\n", analysis.CoveragePartial},
		"real case":            {"a.sh", "case $a in\n  b) source \"$y\" ;;\nesac\n", analysis.CoveragePartial},
		"after heredoc":        {"a.sh", "cat <<-EOF\n\tx\n\tEOF\nsource \"$y\"\n", analysis.CoveragePartial},
		"real ns":              {"a.php", "<?php\nnamespace Foo\\Bar;\n", analysis.CoveragePartial},
		"real use":             {"a.php", "<?php\nuse Foo\\Bar;\n", analysis.CoveragePartial},
		"real loader":          {"a.rb", "l = Zeitwerk::Loader.new\n", analysis.CoveragePartial},
	} {
		t.Run(name, func(t *testing.T) {
			assertFileCoverage(t, evidenceGraph(t, map[string]string{tc.file: tc.body}), tc.file, tc.want, "")
		})
	}
}

func TestResolvedDynamicSourceIsNoEvidence(t *testing.T) {
	files := map[string]string{"a.sh": "DIR=.\nsource \"${DIR}/lib/b.sh\"\n", "lib/b.sh": "x=1\n"}
	assertFileCoverage(t, evidenceGraph(t, files), "a.sh", analysis.CoverageComplete, "")
	files["c.sh"] = "source \"${DIR}/lib/gone.sh\"\n"
	assertFileCoverage(t, evidenceGraph(t, files), "c.sh", analysis.CoveragePartial, "c.sh:1, 1 in total")
}

func TestCIncludeClimbingPastRootLandsInRepo(t *testing.T) {
	graph := falseEdgeGraph(t, map[string]string{
		"src/a.c":     "#include \"../../include/x.h\"\n",
		"src/b.c":     "#include \"../../x.h\"\n",
		"include/x.h": "int x;\n",
		"x.h":         "int x;\n",
	})
	assertImports(t, graph, "src/a.c", "include/x.h")
	assertImports(t, graph, "src/b.c")
}

func TestImportAboveRootLowersCoverage(t *testing.T) {
	for file, body := range map[string]string{
		"src/a.c":   "#include \"../../nope.h\"\n",
		"src/a.php": "<?php\nrequire '../../nope.php';\n",
		"src/a.rb":  "require_relative '../../nope'\n",
		"src/a.sh":  "source ../../nope.sh\n",
	} {
		t.Run(file, func(t *testing.T) {
			assertFileCoverage(t, evidenceGraph(t, map[string]string{file: body}), file, analysis.CoveragePartial, "climbing above the scan root")
		})
	}
}

func TestShiftOperatorIsNoHeredoc(t *testing.T) {
	for _, first := range []string{
		"(( a << b ))", "a=$((x<<y))", "echo $(( 1 << n ))", "[[ a<<b ]]",
		"cat <<END-OF", "cat <<a.b", "x=\"\ncat <<EOF\n\"",
	} {
		t.Run(first, func(t *testing.T) {
			graph := evidenceGraph(t, map[string]string{"a.sh": first + "\nsource \"$y\"\n"})
			assertFileCoverage(t, graph, "a.sh", analysis.CoveragePartial, "")
		})
	}
	for name, body := range map[string]string{
		"dashed":     "cat <<END-OF\nsource \"$y\"\nEND-OF\n",
		"dotted":     "cat <<a.b\nsource \"$y\"\na.b\n",
		"quoted":     "cat <<'END OF'\nsource \"$y\"\nEND OF\n",
		"heredoc in": "(( a << b ))\ncat <<b\nsource \"$y\"\nb\n",
	} {
		t.Run(name, func(t *testing.T) {
			assertFileCoverage(t, evidenceGraph(t, map[string]string{"a.sh": body}), "a.sh", analysis.CoverageComplete, "")
		})
	}
}

func TestRootDirectoryImportFallsBackWhenMainNamesNothing(t *testing.T) {
	for name, tc := range map[string]struct {
		pkg, index, from, body string
	}{
		"missing file": {`{"main":"nope.js"}`, "index.js", "s/t.js", "require('..')\n"},
		"missing dir":  {`{"main":"lib"}`, "index.js", "s/t.js", "require('..')\n"},
		"unbuilt ts":   {`{"main":"dist/index.js"}`, "index.ts", "s/t.ts", "import '../'\n"},
	} {
		t.Run(name, func(t *testing.T) {
			graph := falseEdgeGraph(t, map[string]string{"package.json": tc.pkg, tc.index: "export {}\n", tc.from: tc.body})
			assertImports(t, graph, tc.from, tc.index)
		})
	}
}

func TestEvidencePastSizeCapReadsPartial(t *testing.T) {
	pad := strings.Repeat("# "+strings.Repeat("x", 78)+"\n", maxEvidenceFileBytes/80+1)
	for name, tc := range map[string]struct {
		files map[string]string
		file  string
		named string
	}{
		"composer": {map[string]string{"composer.json": `{"pad":"` + strings.Repeat("x", maxEvidenceFileBytes) + `","autoload":{"psr-4":{"A\\":"s"}}}`, "a.php": "<?php\n"}, "a.php", "composer.json"},
		"gemfile":  {map[string]string{"Gemfile": pad + "gem 'rails'\n", "a.rb": "x = 1\n"}, "a.rb", "Gemfile"},
		"php":      {map[string]string{"a.php": "<?php\n" + pad + "namespace Foo;\n"}, "a.php", "a.php"},
		"ruby":     {map[string]string{"a.rb": pad + "Zeitwerk::Loader.new\n"}, "a.rb", "a.rb"},
		"shell":    {map[string]string{"a.sh": pad + "source \"$y\"\n"}, "a.sh", "a.sh"},
	} {
		t.Run(name, func(t *testing.T) {
			assertFileCoverage(t, evidenceGraph(t, tc.files), tc.file, analysis.CoveragePartial, tc.named+" is too large")
		})
	}
}

func TestDiffImpactSeesDynamicShellImports(t *testing.T) {
	impact := AnalyzeImpactFromAnalyses(
		[]FileInfo{{Path: "lib/b.sh"}},
		[]FileAnalysis{{Path: "a.sh", Language: "bash", References: []ImportReference{{Path: "${DIR}/lib/b.sh", Kind: bashDynamicImport}}}},
	)
	if len(impact) != 1 || impact[0].UsedBy != 1 {
		t.Fatalf("impact = %+v, want one use of b.sh", impact)
	}
}

func TestBashAssignmentAndCommandMustBeReal(t *testing.T) {
	for _, source := range []string{
		"# X=.\nsource \"$X/lib/b.sh\"\n",
		"true # X=.\nsource \"$X/lib/b.sh\"\n",
		"echo 'a X=.'\nsource \"$X/lib/b.sh\"\n",
		"echo \"a X=.\"\nsource \"$X/lib/b.sh\"\n",
		"cat <<E\nX=.\nE\nsource \"$X/lib/b.sh\"\n",
		"source \"$(curl https://x/pwd)/lib/b.sh\"\n",
		"source \"$(brew --prefix dirname)/lib/b.sh\"\n",
		"source \"$(echo /opt/pwd)/lib/b.sh\"\n",
		"source \"$(dirname \"$HOME\")/lib/b.sh\"\n",
		"source \"$(cd /opt && pwd)/lib/b.sh\"\n",
	} {
		t.Run(source, func(t *testing.T) {
			assertImports(t, falseEdgeGraph(t, map[string]string{"a.sh": source, "lib/b.sh": "x=1\n"}), "a.sh")
		})
	}
	for _, source := range []string{
		"X=.\nsource \"$X/lib/b.sh\"\n",
		"export X=. Y=.\nsource \"$Y/lib/b.sh\"\n",
		": \"${X:=.}\"\nsource \"$X/lib/b.sh\"\n",
		"f() { X=.; }\nsource \"$X/lib/b.sh\"\n",
		"source \"$(pwd -P)/lib/b.sh\"\n",
		"source \"$(git rev-parse --show-toplevel)/lib/b.sh\"\n",
		"source \"$(dirname \"${BASH_SOURCE[0]}\")/lib/b.sh\"\n",
		"source \"$(CDPATH= cd -- \"$(dirname -- \"$0\")\" && pwd)/lib/b.sh\"\n",
	} {
		t.Run(source, func(t *testing.T) {
			assertImports(t, falseEdgeGraph(t, map[string]string{"a.sh": source, "lib/b.sh": "x=1\n"}), "a.sh", "lib/b.sh")
		})
	}
}

func TestStrongEvidenceStillLowersCoverage(t *testing.T) {
	for name, tc := range map[string]struct {
		files map[string]string
		file  string
	}{
		"bats reached load": {map[string]string{"t.bats": "load h\n", "h.bash": "load \"$y\"\n"}, "h.bash"},
		"ruby after string": {map[string]string{"a.rb": "puts \"x\"; l = Zeitwerk::Loader.new\n"}, "a.rb"},
		"ruby string arg":   {map[string]string{"a.rb": "Foo.new(\"a\") || Zeitwerk::Loader.for_gem\n"}, "a.rb"},
		"unicode namespace": {map[string]string{"a.php": "<?php\nnamespace Föö;\n"}, "a.php"},
	} {
		t.Run(name, func(t *testing.T) {
			assertFileCoverage(t, evidenceGraph(t, tc.files), tc.file, analysis.CoveragePartial, "")
		})
	}
}

func TestMoreLanguagesReportImportsAboveRoot(t *testing.T) {
	for file, tc := range map[string]struct{ body, fragment string }{
		"a.dart": {"import '../outside.dart';\n", "climbing above the scan root"},
		"a.sol":  {"import \"../outside.sol\";\n", "climbing above the scan root"},
		"a.js":   {"require('..')\n", "did not resolve"},
		"b/a.js": {"require('../../..')\n", "did not resolve"},
	} {
		t.Run(file, func(t *testing.T) {
			assertFileCoverage(t, evidenceGraph(t, map[string]string{file: tc.body}), file, analysis.CoveragePartial, tc.fragment)
		})
	}
}
