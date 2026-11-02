package scanner

import (
	"context"
	"os"
	"path/filepath"
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
		"unterminated heredoc": {"a.sh", "cat <<EOF\nsource $x\n", analysis.CoverageComplete},
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
