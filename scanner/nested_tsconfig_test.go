package scanner

import "testing"

// #271: trpc's examples carry their own tsconfig with "~/*" aliases the root
// tsconfig does not declare, and files under them resolve against the nearest one.
func TestNestedTSConfigPathsApplyToFilesBelowThem(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "tsconfig.json", `{"compilerOptions":{"baseUrl":".","paths":{"~/*":["lib/*"]}}}`)
	writeWorkspaceFile(t, root, "examples/app/tsconfig.json", `{
  // comments are legal in tsconfig
  "compilerOptions": {"paths": {"~/*": ["./src/*"]}},
}`)
	writeWorkspaceFile(t, root, "examples/base/tsconfig.base.json", `{"compilerOptions":{"baseUrl":"..","paths":{"@shared/*":["shared/*"]}}}`)
	writeWorkspaceFile(t, root, "examples/web/tsconfig.json", `{"extends":["./missing","../base/tsconfig.base"]}`)
	writeWorkspaceFiles(t, root,
		"lib/util.ts",
		"src/main.ts",
		"examples/app/src/util.ts",
		"examples/app/src/pages/index.ts",
		"examples/shared/db.ts",
		"examples/web/page.ts",
	)

	graph := buildWorkspaceGraph(t, root,
		workspaceAnalysis("src/main.ts", "~/util"),
		workspaceAnalysis("examples/app/src/pages/index.ts", "~/util"),
		workspaceAnalysis("examples/web/page.ts", "@shared/db", "~/util"),
	)
	assertImports(t, graph, "src/main.ts", "lib/util.ts")
	assertImports(t, graph, "examples/app/src/pages/index.ts", "examples/app/src/util.ts")
	// extends chain: baseUrl ".." is relative to the base config, not the child.
	assertImports(t, graph, "examples/web/page.ts", "examples/shared/db.ts")
}

func TestDirectoryImportResolvesThroughPackageJSONMain(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "packages/js/package.json", `{"main":"lib/index.js"}`)
	writeWorkspaceFile(t, root, "packages/noext/package.json", `{"main":"./lib/api"}`)
	writeWorkspaceFile(t, root, "packages/exp/package.json", `{"exports":"./out/entry.js"}`)
	writeWorkspaceFile(t, root, "packages/broken/package.json", `{"main":"gone.js"}`)
	writeWorkspaceFiles(t, root,
		"packages/js/lib/index.js",
		"packages/noext/lib/api.js",
		"packages/noext/index.js",
		"packages/exp/out/entry.js",
		"packages/broken/index.js",
		"tools/rule.js",
	)

	graph := buildWorkspaceGraph(t, root, FileAnalysis{
		Path: "tools/rule.js", Language: "javascript",
		Imports: []string{"../packages/js", "../packages/noext", "../packages/exp", "../packages/broken"},
	})
	assertImports(t, graph, "tools/rule.js",
		"packages/js/lib/index.js",
		"packages/noext/lib/api.js",
		"packages/exp/out/entry.js",
		"packages/broken/index.js",
	)
}
