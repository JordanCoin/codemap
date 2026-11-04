package scanner

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

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

func readTSConfigCounting(t *testing.T, root, config string) (map[string][]string, int) {
	t.Helper()
	reader := newTSConfigReader(root)
	paths, _, _ := reader.read(filepath.Join(root, config), 0)
	return paths, reader.reads
}

func TestTSConfigExtendsCycleAndDiamondReadEachConfigOnce(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "a.json", `{"extends":"./b","compilerOptions":{"paths":{"@a/*":["a/*"]}}}`)
	writeWorkspaceFile(t, root, "b.json", `{"extends":"./a","compilerOptions":{"paths":{"@b/*":["b/*"]}}}`)
	if paths, reads := readTSConfigCounting(t, root, "a.json"); len(paths) != 2 || reads != 2 {
		t.Fatalf("cycle: paths=%v reads=%d, want 2 keys in 2 reads", paths, reads)
	}

	writeWorkspaceFile(t, root, "top.json", `{"extends":["./left","./right"]}`)
	writeWorkspaceFile(t, root, "left.json", `{"extends":"./shared"}`)
	writeWorkspaceFile(t, root, "right.json", `{"extends":"./shared"}`)
	writeWorkspaceFile(t, root, "shared.json", `{"compilerOptions":{"paths":{"@s/*":["s/*"]}}}`)
	if paths, reads := readTSConfigCounting(t, root, "top.json"); len(paths) != 1 || reads != 4 {
		t.Fatalf("diamond: paths=%v reads=%d, want 1 key in 4 reads", paths, reads)
	}

	// 20 levels of extends [x, x]: 2^20 reads without the visited set.
	for depth := 0; depth < 20; depth++ {
		writeWorkspaceFile(t, root, fmt.Sprintf("d%d.json", depth), fmt.Sprintf(`{"extends":["./d%d","./d%d"]}`, depth+1, depth+1))
	}
	writeWorkspaceFile(t, root, "d20.json", `{"compilerOptions":{"paths":{"@d/*":["d/*"]}}}`)
	if paths, reads := readTSConfigCounting(t, root, "d0.json"); len(paths) != 1 || reads != 21 {
		t.Fatalf("repeated parents: paths=%v reads=%d, want 1 key in 21 reads", paths, reads)
	}
}

func TestTSConfigExtendsFanOutIsBounded(t *testing.T) {
	root := t.TempDir()
	// Each level lists two distinct parents, so only the read cap bounds it.
	for depth := 0; depth < 20; depth++ {
		for i := 0; i < 1<<depth && i < 128; i++ {
			body := fmt.Sprintf(`{"extends":["./c%d_%d","./c%d_%d"]}`, depth+1, 2*i, depth+1, 2*i+1)
			writeWorkspaceFile(t, root, fmt.Sprintf("c%d_%d.json", depth, i), body)
		}
	}
	if _, reads := readTSConfigCounting(t, root, "c0_0.json"); reads != maxTSConfigReads {
		t.Fatalf("reads = %d, want the cap %d", reads, maxTSConfigReads)
	}
}

func TestTSConfigExtendsStaysInsideRoot(t *testing.T) {
	outside := t.TempDir()
	writeWorkspaceFile(t, outside, "secret.json", `{"compilerOptions":{"paths":{"@x/*":["x/*"]}}}`)
	root := t.TempDir()
	rel, err := filepath.Rel(root, filepath.Join(outside, "secret.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeWorkspaceFile(t, root, "up.json", fmt.Sprintf(`{"extends":%q}`, filepath.ToSlash(rel)))
	if err := os.Symlink(filepath.Join(outside, "secret.json"), filepath.Join(root, "link.json")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	writeWorkspaceFile(t, root, "via-link.json", `{"extends":"./link.json"}`)
	writeWorkspaceFile(t, root, "node_modules/@tsconfig/base/tsconfig.json", `{"compilerOptions":{"paths":{"@n/*":["n/*"]}}}`)
	writeWorkspaceFile(t, root, "pkg.json", `{"extends":"./node_modules/@tsconfig/base/tsconfig.json"}`)
	for _, config := range []string{"up.json", "via-link.json"} {
		if paths, _ := readTSConfigCounting(t, root, config); len(paths) != 0 {
			t.Fatalf("%s read a config outside the root: %v", config, paths)
		}
	}
	if paths, _ := readTSConfigCounting(t, root, "pkg.json"); len(paths) != 1 {
		t.Fatalf("pkg.json paths = %v, want the node_modules base inside the root", paths)
	}
}
