package scanner

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestBuildFileGraphResolvesPackageWorkspaces(t *testing.T) {
	tests := []struct{ name, workspaces, memberDir string }{
		{"node nested workspace glob", `["packages/**"]`, "packages/components/ui"},
		{"bun workspace object", `{"packages":["modules/*"]}`, "modules/ui"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeWorkspaceFile(t, root, "package.json", `{"name":"workspace-root","workspaces":`+tt.workspaces+`}`)
			writeWorkspaceFile(t, root, ".codemap/config.json", `{"only":["ts"]}`)
			writeWorkspaceFile(t, root, tt.memberDir+"/package.json",
				`{"name":"@acme/ui","exports":{".":"./src/index.ts","./button":"./src/button.ts","./features/*":"./src/features/*.ts"},"imports":{"#internal/*":"./src/internal/*.ts"}}`)
			writeWorkspaceFiles(t, root,
				"app/main.ts", "app/local.ts", tt.memberDir+"/src/index.ts", tt.memberDir+"/src/button.ts",
				tt.memberDir+"/src/features/card.ts",
				tt.memberDir+"/src/internal/util.ts", tt.memberDir+"/src/consumer.ts",
			)
			graph := buildWorkspaceGraph(t, root,
				workspaceAnalysis("app/main.ts",
					"@acme/ui", "@acme/ui/button", "@acme/ui/features/card", "#internal/util",
					"node:path", "external-package", "./local",
				),
				workspaceAnalysis(tt.memberDir+"/src/consumer.ts", "#internal/util"),
			)
			assertWorkspaceImports(t, graph, "app/main.ts", []string{
				filepath.FromSlash("app/local.ts"),
				filepath.FromSlash(tt.memberDir + "/src/button.ts"),
				filepath.FromSlash(tt.memberDir + "/src/features/card.ts"),
				filepath.FromSlash(tt.memberDir + "/src/index.ts"),
			})
			assertWorkspaceImports(t, graph, tt.memberDir+"/src/consumer.ts", []string{
				filepath.FromSlash(tt.memberDir + "/src/internal/util.ts"),
			})
		})
	}
}

func TestBuildFileGraphResolvesDenoAndHybridWorkspaces(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "deno.jsonc", `{
		// URL comments and trailing commas exercise JSONC normalization.
		"imports":{"@exact":"./lib/exact.ts","@core/":"./lib/core/","url-literal":"https://example.com/a//b"},
		"workspace":["./members/tool","./members/hybrid"],
	}`)
	writeWorkspaceFile(t, root, "members/tool/deno.json",
		`{"name":"@deno/tool","exports":"./mod.ts","imports":{"@exact":"./local.ts"}}`)
	writeWorkspaceFile(t, root, "members/hybrid/package.json",
		`{"name":"@hybrid/pkg","exports":{".":"./src/index.ts"}}`)

	writeWorkspaceFiles(t, root,
		"app/main.ts", "lib/exact.ts", "lib/core/util.ts",
		"members/tool/mod.ts", "members/tool/local.ts", "members/tool/consumer.ts",
		"members/hybrid/src/index.ts",
	)

	graph := buildWorkspaceGraph(t, root,
		workspaceAnalysis("app/main.ts",
			"@exact", "@core/util.ts", "@deno/tool", "@hybrid/pkg", "url-literal",
			"npm:chalk", "jsr:@std/path", "bun:test", "node:path", "https://example.com/mod.ts",
			"data:text/javascript,export default 1",
		),
		workspaceAnalysis("members/tool/consumer.ts", "@exact"),
	)

	assertWorkspaceImports(t, graph, "app/main.ts", []string{
		filepath.FromSlash("lib/core/util.ts"),
		filepath.FromSlash("lib/exact.ts"),
		filepath.FromSlash("members/hybrid/src/index.ts"),
		filepath.FromSlash("members/tool/mod.ts"),
	})
	assertWorkspaceImports(t, graph, "members/tool/consumer.ts", []string{
		filepath.FromSlash("members/tool/local.ts"),
	})
}

func TestBuildFileGraphResolvesDenoObjectWorkspaceMembers(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "deno.json", `{"workspace":{"members":["members/tool"]}}`)
	writeWorkspaceFile(t, root, "members/tool/deno.json", `{"name":"@deno/tool","exports":"./mod.ts"}`)
	writeWorkspaceFiles(t, root, "app.ts", "members/tool/mod.ts")
	graph := buildWorkspaceGraph(t, root, workspaceAnalysis("app.ts", "@deno/tool"))
	assertWorkspaceImports(t, graph, "app.ts", []string{filepath.FromSlash("members/tool/mod.ts")})
}

func TestBuildFileGraphCombinesWorkspaceDeclarationsAtSameRoot(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "package.json", `{"workspaces":["packages/*"]}`)
	writeWorkspaceFile(t, root, "pnpm-workspace.yaml", "packages:\n  - \"tools/*\"\n")
	writeWorkspaceFile(t, root, "packages/web/package.json", `{"name":"web","exports":"./index.ts"}`)
	writeWorkspaceFile(t, root, "tools/cli/package.json", `{"name":"tooling","exports":"./index.ts"}`)
	writeWorkspaceFiles(t, root, "app.ts", "packages/web/index.ts", "tools/cli/index.ts")
	graph := buildWorkspaceGraph(t, root, workspaceAnalysis("app.ts", "web", "tooling"))
	assertWorkspaceImports(t, graph, "app.ts", []string{
		filepath.FromSlash("packages/web/index.ts"),
		filepath.FromSlash("tools/cli/index.ts"),
	})
}

func TestJSWorkspaceResolverUsesExplicitFilters(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, ".codemap/config.json", `{"exclude":["packages/web"]}`)
	writeWorkspaceFile(t, root, "package.json", `{"workspaces":["packages/*"]}`)
	writeWorkspaceFile(t, root, "packages/web/package.json", `{"name":"web","exports":"./index.ts"}`)
	writeWorkspaceFile(t, root, "app.ts", "")
	writeWorkspaceFile(t, root, "packages/web/index.ts", "")

	graph, err := BuildFileGraphFromAnalyses(context.Background(), root, []FileAnalysis{{
		Path: "app.ts", Language: "typescript", Imports: []string{"web"},
	}}, Filters{})
	if err != nil {
		t.Fatal(err)
	}
	assertWorkspaceImports(t, graph, "app.ts", []string{filepath.FromSlash("packages/web/index.ts")})
}

func TestJSWorkspaceResolverOnlyWidensJavaScriptScans(t *testing.T) {
	if needsJSWorkspaceResolver([]FileAnalysis{{Path: "main.go", Language: "go"}}) {
		t.Fatal("Go-only analysis should not widen the filtered file scan")
	}
	if !needsJSWorkspaceResolver([]FileAnalysis{{Path: "app.ts", Language: "typescript"}}) {
		t.Fatal("TypeScript analysis should enable workspace manifest scanning")
	}
}

func TestBuildJSWorkspaceResolverHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := buildJSWorkspaceResolver(ctx, t.TempDir(), nil); err != context.Canceled {
		t.Fatalf("buildJSWorkspaceResolver() error = %v, want context.Canceled", err)
	}
}

func TestAddJSWorkspaceMembersHonorsMidBuildCancellation(t *testing.T) {
	ctx := &cancelAfterContext{Context: context.Background(), remaining: 1}
	workspace := newJSPackageWorkspace("")
	members := map[string]*jsWorkspaceManifest{
		"a": {root: "packages/a", pkg: &jsWorkspacePackage{root: "packages/a", name: "a"}},
		"b": {root: "packages/b", pkg: &jsWorkspacePackage{root: "packages/b", name: "b"}},
		"c": {root: "packages/c", pkg: &jsWorkspacePackage{root: "packages/c", name: "c"}},
	}
	if err := addJSWorkspaceMembers(ctx, workspace, "", []string{"packages/*"}, members); err != context.Canceled {
		t.Fatalf("addJSWorkspaceMembers() error = %v, want context.Canceled", err)
	}
	if len(workspace.packages) > 1 {
		t.Fatalf("added %d packages after cancellation boundary, want at most 1", len(workspace.packages))
	}
}

type cancelAfterContext struct {
	context.Context
	remaining int
}

func (c *cancelAfterContext) Err() error {
	if c.remaining == 0 {
		return context.Canceled
	}
	c.remaining--
	return nil
}

func TestBuildFileGraphResolvesWorkspaceEntriesWithoutExports(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "package.json", `{"workspaces":["packages/*"]}`)
	writeWorkspaceFile(t, root, "packages/plain/package.json", `{"name":"@acme/plain","main":"src/index.ts"}`)
	writeWorkspaceFile(t, root, "packages/conflict/package.json",
		`{"name":"@acme/conflict","module":"src/module.ts","main":"src/main.ts"}`)
	writeWorkspaceFiles(t, root,
		"app/main.ts",
		"packages/plain/src/index.ts",
		"packages/plain/feature.ts",
		"packages/conflict/src/module.ts",
		"packages/conflict/src/main.ts",
	)

	graph := buildWorkspaceGraph(t, root,
		workspaceAnalysis("app/main.ts", "@acme/plain", "@acme/plain/feature", "@acme/conflict"),
	)
	assertWorkspaceImports(t, graph, "app/main.ts", []string{
		filepath.FromSlash("packages/plain/feature.ts"),
		filepath.FromSlash("packages/plain/src/index.ts"),
	})
}

func TestBuildFileGraphResolvesPnpmConditionalExportsToSources(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "pnpm-workspace.yaml", "packages:\n  - packages/**\n  - '!packages/excluded'\n")
	writeWorkspaceFile(t, root, "packages/foundation/core/package.json",
		`{"name":"@acme/core","exports":{"./*":{"import":"./dist/*.js","types":"./dist/*.d.ts","types@>=5.0":"./dist/*.d.ts"}}}`)
	writeWorkspaceFile(t, root, "packages/foundation/core/tsconfig.json",
		`{"compilerOptions":{"rootDir":"src","outDir":"dist"}}`)
	writeWorkspaceFile(t, root, "packages/ambiguous/package.json",
		`{"name":"@acme/ambiguous","exports":{".":{"import":"./dist/import.js","require":"./dist/require.cjs"},"./array":["./dist/one.js","./dist/two.js"],"./empty":null}}`)
	writeWorkspaceFile(t, root, "packages/ambiguous/tsconfig.json",
		`{"compilerOptions":{"rootDir":"src","outDir":"dist"}}`)
	writeWorkspaceFile(t, root, "packages/no-map/package.json",
		`{"name":"@acme/no-map","exports":{"./*":{"import":"./dist/*.js"}}}`)
	writeWorkspaceFile(t, root, "packages/excluded/package.json",
		`{"name":"@acme/excluded","exports":"./src/index.ts"}`)

	writeWorkspaceFiles(t, root,
		"app/main.ts",
		"packages/foundation/core/src/context.ts",
		"packages/ambiguous/src/import.ts",
		"packages/ambiguous/src/require.ts",
		"packages/ambiguous/src/one.ts",
		"packages/ambiguous/src/two.ts",
		"packages/no-map/src/context.ts",
		"packages/excluded/src/index.ts",
	)

	graph := buildWorkspaceGraph(t, root,
		workspaceAnalysis("app/main.ts",
			"@acme/core/context",
			"@acme/ambiguous",
			"@acme/ambiguous/array",
			"@acme/ambiguous/empty",
			"@acme/no-map/context",
			"@acme/excluded",
		),
	)
	assertWorkspaceImports(t, graph, "app/main.ts", []string{
		filepath.FromSlash("packages/foundation/core/src/context.ts"),
	})
}

func TestBuildFileGraphDoesNotLeakPackagesAcrossWorkspaceOwners(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "one/package.json", `{"name":"one-root","workspaces":["packages/*"]}`)
	writeWorkspaceFile(t, root, "one/packages/local/package.json", `{"name":"@acme/local","exports":"./src/index.ts"}`)
	writeWorkspaceFile(t, root, "two/deno.json", `{"workspace":["./packages/external"]}`)
	writeWorkspaceFile(t, root, "two/packages/external/package.json", `{"name":"@acme/external","exports":"./src/index.ts"}`)
	writeWorkspaceFiles(t, root,
		"one/app/main.ts",
		"one/packages/local/src/index.ts",
		"two/packages/external/src/index.ts",
	)

	graph := buildWorkspaceGraph(t, root,
		workspaceAnalysis("one/app/main.ts", "@acme/local", "@acme/external"),
	)
	assertWorkspaceImports(t, graph, "one/app/main.ts", []string{
		filepath.FromSlash("one/packages/local/src/index.ts"),
	})
}

func TestBuildFileGraphAppliesOrderedWorkspacePatterns(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "package.json", `{"workspaces":["packages/*"," !packages/private ","!packages/restored","packages/restored"]}`)
	writeWorkspaceFile(t, root, "packages/ui/package.json", `{"name":"@acme/ui","exports":"./src/index.ts"}`)
	writeWorkspaceFile(t, root, "packages/private/package.json", `{"name":"@acme/private","exports":"./src/index.ts"}`)
	writeWorkspaceFile(t, root, "packages/restored/package.json", `{"name":"@acme/restored","exports":"./src/index.ts"}`)
	writeWorkspaceFiles(t, root, "packages/ui/src/index.ts", "packages/private/src/index.ts", "packages/restored/src/index.ts")
	writeWorkspaceFile(t, root, "app/main.ts", "")

	graph := buildWorkspaceGraph(t, root, workspaceAnalysis("app/main.ts", "@acme/ui", "@acme/private", "@acme/restored"))
	assertWorkspaceImports(t, graph, "app/main.ts", []string{
		filepath.FromSlash("packages/restored/src/index.ts"),
		filepath.FromSlash("packages/ui/src/index.ts"),
	})
}

func TestValidWorkspacePatterns(t *testing.T) {
	for pattern, want := range map[string]bool{
		"!packages/private": true,
		"!../outside":       false,
		"!!packages/*":      false,
		"!":                 false,
		"C:/outside":        false,
		"!C:/outside":       false,
	} {
		if got := validWorkspacePattern(pattern); got != want {
			t.Errorf("validWorkspacePattern(%q) = %v, want %v", pattern, got, want)
		}
	}
}

func TestBuildFileGraphRejectsInvalidWorkspacePatterns(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "package.json", `{"workspaces":["packages/*","../outside/*"]}`)
	writeWorkspaceFile(t, root, "packages/ui/package.json", `{"name":"@acme/ui","exports":"./src/index.ts"}`)
	writeWorkspaceFiles(t, root, "packages/ui/src/index.ts", "app/main.ts")
	graph := buildWorkspaceGraph(t, root, workspaceAnalysis("app/main.ts", "@acme/ui"))
	assertWorkspaceImports(t, graph, "app/main.ts", nil)
}

func TestBuildFileGraphRejectsAmbiguousOrEscapingWorkspaceTargets(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "package.json", `{"workspaces":["packages/*"]}`)

	manifests := map[string]string{
		"packages/dup-a/package.json":        `{"name":"@acme/duplicate","exports":"./src/index.ts"}`,
		"packages/dup-b/package.json":        `{"name":"@acme/duplicate","exports":"./src/index.ts"}`,
		"packages/conditional/package.json":  `{"name":"@acme/conditional","exports":{".":{"import":"./src/import.ts","require":"./src/require.ts"}}}`,
		"packages/escape/package.json":       `{"name":"@acme/escape","exports":"../outside.ts"}`,
		"packages/unknown/package.json":      `{"name":"@acme/unknown","exports":{".":{"custom":"./src/index.ts"}}}`,
		"packages/typesfoo/package.json":     `{"name":"@acme/typesfoo","exports":{".":{"typesfoo":"./src/index.ts","import":"./src/index.ts"}}}`,
		"packages/declaration/package.json":  `{"name":"@acme/declaration","exports":"./src/index.d.ts"}`,
		"packages/incompatible/package.json": `{"name":"@acme/incompatible","exports":"./src/index.go"}`,
		"packages/implicit/package.json":     `{"name":"@acme/implicit","exports":"./src/index"}`,
	}
	for path, body := range manifests {
		writeWorkspaceFile(t, root, path, body)
	}
	writeWorkspaceFiles(t, root,
		"app/main.ts",
		"outside.ts",
		"packages/dup-a/src/index.ts",
		"packages/dup-b/src/index.ts",
		"packages/conditional/src/import.ts",
		"packages/conditional/src/require.ts",
		"packages/unknown/src/index.ts",
		"packages/typesfoo/src/index.ts",
		"packages/declaration/src/index.d.ts",
		"packages/incompatible/src/index.go",
		"packages/implicit/src/index.ts",
	)

	graph := buildWorkspaceGraph(t, root,
		workspaceAnalysis("app/main.ts", "@acme/duplicate", "@acme/conditional", "@acme/escape", "@acme/unknown", "@acme/typesfoo", "@acme/declaration", "@acme/incompatible", "@acme/implicit"),
	)
	assertWorkspaceImports(t, graph, "app/main.ts", nil)
}

func TestStripJSONC(t *testing.T) {
	input := []byte(`{
		"url": "https://example.com/a//b",
		"quoted": "/* not a comment */",
		// line comment
		"array": [1, 2,],
		/* block
		   comment */
		"object": {"ok": true,},
	}`)

	got, err := stripJSONC(input)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"url":    "https://example.com/a//b",
		"quoted": "/* not a comment */",
		"array":  []any{float64(1), float64(2)},
		"object": map[string]any{"ok": true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stripJSONC() = %#v, want %#v", got, want)
	}

	if _, err := stripJSONC([]byte(`{"value": /* unterminated`)); err == nil {
		t.Fatal("expected unterminated block comment to fail")
	}
	if _, err := stripJSONC([]byte(`{"value": invalid}`)); err == nil {
		t.Fatal("expected invalid JSON to fail")
	}
}

func writeWorkspaceFile(t *testing.T, root, path, body string) {
	t.Helper()
	fullPath := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeWorkspaceFiles(t *testing.T, root string, paths ...string) {
	t.Helper()
	for _, path := range paths {
		writeWorkspaceFile(t, root, path, "")
	}
}

func workspaceAnalysis(path string, imports ...string) FileAnalysis {
	return FileAnalysis{Path: filepath.FromSlash(path), Language: "typescript", Imports: imports}
}

func buildWorkspaceGraph(t *testing.T, root string, analyses ...FileAnalysis) *FileGraph {
	t.Helper()
	graph, err := BuildFileGraphFromAnalyses(context.Background(), root, analyses, Filters{})
	if err != nil {
		t.Fatal(err)
	}
	return graph
}

func assertWorkspaceImports(t *testing.T, graph *FileGraph, path string, want []string) {
	t.Helper()
	got := append([]string(nil), graph.Imports[filepath.FromSlash(path)]...)
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("imports[%q] = %v, want %v", path, got, want)
	}
}

// Regression for PR #105: package-internal "#imports" with conditional targets
// ({"default": ..., "types": ...}) must resolve like conditional exports
// instead of being dropped as invalid.
func TestBuildFileGraphResolvesConditionalPackageImports(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "package.json",
		`{"name":"@acme/cond","exports":"./src/index.ts","imports":{"#env":{"default":"./src/env.ts","types":"./src/env.d.ts"},"#noop":"nope"}}`)
	writeWorkspaceFiles(t, root, "src/index.ts", "src/env.ts", "src/env.d.ts", "src/consumer.ts")

	graph := buildWorkspaceGraph(t, root,
		workspaceAnalysis("src/consumer.ts", "#env", "#noop"),
	)
	assertWorkspaceImports(t, graph, "src/consumer.ts", []string{filepath.FromSlash("src/env.ts")})
}

// Regression for PR #105: a package whose tsconfig.json inherits rootDir/outDir
// via extends must still remap ./dist/* exports back to src/* sources.
func TestBuildFileGraphResolvesExtendedTsconfigOutputDirs(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "tsconfig.base.json", `{"compilerOptions":{"rootDir":"src","outDir":"dist"}}`)
	writeWorkspaceFile(t, root, "package.json", `{"workspaces":["packages/*"]}`)
	writeWorkspaceFile(t, root, "packages/core/package.json", `{"name":"@acme/ext","exports":"./dist/index.js"}`)
	writeWorkspaceFile(t, root, "packages/core/tsconfig.json", `{"extends":"../../tsconfig.base.json"}`)
	writeWorkspaceFiles(t, root, "app/main.ts", "packages/core/src/index.ts")

	graph := buildWorkspaceGraph(t, root, workspaceAnalysis("app/main.ts", "@acme/ext"))
	assertWorkspaceImports(t, graph, "app/main.ts", []string{filepath.FromSlash("packages/core/src/index.ts")})
}

func TestParsePackageImportsConditionalTargets(t *testing.T) {
	m := parsePackageImports(map[string]any{
		"#env":    map[string]any{"default": "./src/env.ts", "types": "./src/env.d.ts"},
		"#direct": "./src/direct.ts",
		"#bad":    []any{"./a.ts", "./b.ts"},
		"#types":  map[string]any{"types": "./src/env.d.ts"},
	})
	if got := m.exact["#env"]; !got.valid || got.target != "./src/env.ts" {
		t.Fatalf("#env = %+v, want valid ./src/env.ts", got)
	}
	if got := m.exact["#direct"]; !got.valid || got.target != "./src/direct.ts" {
		t.Fatalf("#direct = %+v, want valid ./src/direct.ts", got)
	}
	if got := m.exact["#bad"]; got.valid {
		t.Fatalf("#bad array must stay invalid, got %+v", got)
	}
	if got := m.exact["#types"]; got.valid {
		t.Fatalf("#types-only target must stay invalid, got %+v", got)
	}
}

func TestMergedTSOutputDirsChildOverridesParent(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "tsconfig.base.json", `{"compilerOptions":{"rootDir":"lib","outDir":"build"}}`)
	writeWorkspaceFile(t, root, "tsconfig.json", `{"extends":"./tsconfig.base.json","compilerOptions":{"outDir":"dist"}}`)
	rootDir, outDir := mergedTSOutputDirs(filepath.Join(root, "tsconfig.json"))
	if rootDir != "lib" || outDir != "dist" {
		t.Fatalf("merged dirs = (%q, %q), want (lib, dist)", rootDir, outDir)
	}
}

func TestMergedTSOutputDirsExtendsWithoutExtension(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "tsconfig.base.json", `{"compilerOptions":{"rootDir":"src","outDir":"dist"}}`)
	writeWorkspaceFile(t, root, "tsconfig.json", `{"extends":"./tsconfig.base"}`)
	rootDir, outDir := mergedTSOutputDirs(filepath.Join(root, "tsconfig.json"))
	if rootDir != "src" || outDir != "dist" {
		t.Fatalf("merged dirs = (%q, %q), want (src, dist)", rootDir, outDir)
	}
}

func TestMergedTSOutputDirsCycleGuardTerminates(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "a.json", `{"extends":"./b.json"}`)
	writeWorkspaceFile(t, root, "b.json", `{"extends":"./a.json"}`)
	rootDir, outDir := mergedTSOutputDirs(filepath.Join(root, "a.json"))
	if rootDir != "" || outDir != "" {
		t.Fatalf("cyclic extends must yield empty dirs, got (%q, %q)", rootDir, outDir)
	}
}

func TestParsePackageImportsSkipsNonPackageKeys(t *testing.T) {
	m := parsePackageImports(map[string]any{
		"#ok":   "./src/ok.ts",
		"env":   "./src/env.ts",
		"#":     "./src/hash.ts",
		"#/":    "./src/slash.ts",
		"#sub/": "./src/sub.ts",
	})
	if got := m.exact["#ok"]; !got.valid {
		t.Fatalf("#ok must stay valid, got %+v", got)
	}
	// Non-# keys, bare "#", "#/" and trailing-#/ keys are not package imports.
	for _, key := range []string{"env", "#", "#/"} {
		if _, ok := m.exact[key]; ok {
			t.Fatalf("key %q must be skipped, got %+v", key, m.exact[key])
		}
	}
}
