package cmd

import (
	"context"
	"reflect"
	"testing"

	"codemap/config"
	"codemap/scanner"
)

func TestContextLexicalRouting(t *testing.T) {
	t.Run("exact path wins over basename regardless of token order", func(t *testing.T) {
		files := routingFiles("cmd/context.go", "pkg/server.go")
		got := resolveContextFilesWithCase("inspect server.go and cmd/context.go", files, config.ProjectConfig{}, 2, false)
		if want := []string{"cmd/context.go", "pkg/server.go"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("files = %#v, want %#v", got, want)
		}
	})

	t.Run("unique extension basename resolves", func(t *testing.T) {
		files := routingFiles("mcp/server.go", "src/final_build.rs")
		got := resolveContextFilesWithCase("connect server.go", files, config.ProjectConfig{}, 3, false)
		if want := []string{"mcp/server.go"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("files = %#v, want %#v", got, want)
		}
	})

	t.Run("ambiguous basename stays unresolved", func(t *testing.T) {
		files := routingFiles("cmd/context.go", "internal/context.go", "src/graph.rs", "mcp/graph.go")
		got := resolveContextFilesWithCase("inspect context.go and graph", files, config.ProjectConfig{}, 4, false)
		if len(got) != 0 {
			t.Fatalf("ambiguous files = %#v, want none", got)
		}
	})

	t.Run("ordinary words do not resolve extensionless file stems", func(t *testing.T) {
		files := routingFiles("cmd/doctor.go", "plugins/install.go", "cmd/root.go", "handoff/build.go")
		got := resolveContextFilesWithCase("please install a new machine and root out the build problem", files, config.ProjectConfig{}, 4, false)
		if len(got) != 0 {
			t.Fatalf("inferred files = %#v, want none", got)
		}
	})

	t.Run("Windows separators and case are normalized", func(t *testing.T) {
		files := routingFiles("Cmd/Context.go")
		got := resolveContextFilesWithCase(`inspect cmd\context.go`, files, config.ProjectConfig{}, 1, true)
		if want := []string{"Cmd/Context.go"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("files = %#v, want %#v", got, want)
		}
		if got := resolveContextFilesWithCase(`inspect cmd\context.go`, files, config.ProjectConfig{}, 1, false); len(got) != 0 {
			t.Fatalf("case-sensitive routing = %#v, want none", got)
		}
	})

	t.Run("traversal tokens stay unresolved", func(t *testing.T) {
		files := routingFiles("foo.go")
		got := resolveContextFilesWithCase("inspect ../foo.go and ../../foo.go", files, config.ProjectConfig{}, 2, false)
		if len(got) != 0 {
			t.Fatalf("traversal files = %#v, want none", got)
		}
	})

	t.Run("normalized duplicates resolve once", func(t *testing.T) {
		files := routingFiles("./cmd/context.go", "cmd//context.go", "cmd/context.go/", "cmd/context.go", "internal/context.go")
		got := resolveContextFilesWithCase("inspect cmd/./context.go and context", files, config.ProjectConfig{}, 2, false)
		if want := []string{"cmd/context.go"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("normalized files = %#v, want %#v", got, want)
		}
	})

	t.Run("inventory keeps significant whitespace", func(t *testing.T) {
		files := routingFiles(" foo.go", "bar.go ")
		got := resolveContextFilesWithCase("inspect foo.go and bar.go", files, config.ProjectConfig{}, 2, false)
		if len(got) != 0 {
			t.Fatalf("whitespace files = %#v, want none", got)
		}
	})

	t.Run("absolute and volume paths stay unresolved", func(t *testing.T) {
		files := routingFiles("/tmp/target.go", "C:/tmp/target.go", "//server/share.go")
		got := resolveContextFilesWithCase("inspect /tmp/target.go C:\\tmp\\target.go //server/share.go", files, config.ProjectConfig{}, 3, true)
		if len(got) != 0 {
			t.Fatalf("absolute files = %#v, want none", got)
		}
		if got := normalizeContextPath(`C:\\tmp\\target.go`); got != "" {
			t.Fatalf("drive path = %q, want empty", got)
		}
		if got := normalizeContextPath("//server/share.go"); got != "" {
			t.Fatalf("UNC path = %q, want empty", got)
		}
		if got := normalizeContextInventoryPath("x:helper.go"); got != "x:helper.go" {
			t.Fatalf("inventory path = %q, want x:helper.go", got)
		}
	})

	t.Run("case-folded exact collisions stay unresolved", func(t *testing.T) {
		files := routingFiles("Cmd/Foo.go", "Root/a.go", "alpha/b.go", "cmd/foo.go")
		got := resolveContextFilesWithCase(`inspect CMD\FOO.GO`, files, config.ProjectConfig{}, 2, true)
		if len(got) != 0 {
			t.Fatalf("case-collision files = %#v, want none", got)
		}
	})

	t.Run("top k bounds lexical matches", func(t *testing.T) {
		files := routingFiles("src/alpha.go", "src/beta.go", "src/gamma.go")
		got := resolveContextFilesWithCase("alpha.go beta.go gamma.go", files, config.ProjectConfig{}, 2, false)
		if want := []string{"src/alpha.go", "src/beta.go"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("files = %#v, want %#v", got, want)
		}
	})

	t.Run("subsystem paths are deterministic and bounded", func(t *testing.T) {
		files := routingFiles("src/build/c.go", "src/build/a.go", "src/build/b.go", "src/building/no.go", "src/other.go")
		cfg := config.ProjectConfig{Routing: config.RoutingConfig{
			Subsystems: []config.Subsystem{{ID: "build", Keywords: []string{"overdrive"}, Paths: []string{"src/build"}}},
		}}
		got := resolveContextFilesWithCase("speed up overdrive", files, cfg, 10, false)
		if want := []string{"src/build/a.go", "src/build/b.go", "src/build/c.go"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("files = %#v, want %#v", got, want)
		}
	})

	t.Run("duplicate subsystem ids retain each path", func(t *testing.T) {
		files := routingFiles("alpha/a.go", "beta/b.go")
		cfg := config.ProjectConfig{Routing: config.RoutingConfig{Subsystems: []config.Subsystem{
			{ID: "shared", Keywords: []string{"overdrive"}, Paths: []string{"alpha"}},
			{ID: "shared", Keywords: []string{"overdrive"}, Paths: []string{"beta"}},
			{ID: "shared", Keywords: []string{"overdrive"}, Paths: []string{"alpha"}},
		}}}
		got := resolveContextFilesWithCase("inspect overdrive", files, cfg, 2, false)
		if want := []string{"alpha/a.go", "beta/b.go"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("files = %#v, want %#v", got, want)
		}
	})

	t.Run("mixed lexical and subsystem routes", func(t *testing.T) {
		files := routingFiles("mcp/server.go", "src/final_build.rs", "src/graph.rs", "docs/design.md")
		cfg := config.ProjectConfig{Routing: config.RoutingConfig{
			Subsystems: []config.Subsystem{{ID: "mcp", Keywords: []string{"mcp"}, Paths: []string{"mcp"}}},
		}}
		got := resolveContextFilesWithCase("trace mcp -> final_build.rs -> graph.rs", files, cfg, 3, false)
		want := []string{"src/final_build.rs", "src/graph.rs", "mcp/server.go"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("files = %#v, want %#v", got, want)
		}
	})

	t.Run("case-folded subsystem route fills after an explicit match", func(t *testing.T) {
		files := routingFiles("src/build/c.go", "SRC/build/a.go", "Src/Build/b.go")
		cfg := config.ProjectConfig{Routing: config.RoutingConfig{
			Subsystems: []config.Subsystem{{ID: "build", Keywords: []string{"overdrive"}, Paths: []string{"src/build"}}},
		}}
		got := resolveContextFilesWithCase("inspect SRC/build/a.go during overdrive", files, cfg, 2, true)
		if want := []string{"SRC/build/a.go", "Src/Build/b.go"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("files = %#v, want %#v", got, want)
		}
	})

	t.Run("explicit basename drives intent risk", func(t *testing.T) {
		files := routingFiles("src/final_build.rs", "a.rs", "b.rs", "c.rs")
		graph := &scanner.FileGraph{
			Imports: map[string][]string{
				"a.rs": {"src/final_build.rs"}, "b.rs": {"src/final_build.rs"}, "c.rs": {"src/final_build.rs"},
			},
			Importers: map[string][]string{"src/final_build.rs": {"a.rs", "b.rs", "c.rs"}},
		}
		envelope := buildContextEnvelopeWithDeps(context.Background(), t.TempDir(), "refactor final_build.rs", true, testContextEnvelopeDeps(files, graph))
		if envelope.Intent == nil || !reflect.DeepEqual(envelope.Intent.Files, []string{"src/final_build.rs"}) || envelope.Intent.RiskLevel != "medium" {
			t.Fatalf("intent = %#v", envelope.Intent)
		}
		if envelope.Intent.FileConfidence != "explicit" {
			t.Fatalf("file confidence = %q, want explicit", envelope.Intent.FileConfidence)
		}
	})

	t.Run("inferred candidates do not affect explicit scope or risk", func(t *testing.T) {
		files := routingFiles("README.md", "src/build/a.go", "src/build/b.go")
		cfg := config.ProjectConfig{Routing: config.RoutingConfig{
			Subsystems: []config.Subsystem{{ID: "build", Keywords: []string{"overdrive"}, Paths: []string{"src/build"}}},
		}}
		graph := &scanner.FileGraph{Importers: map[string][]string{
			"README.md":      {},
			"src/build/a.go": {"one.go", "two.go", "three.go"},
		}}
		root := t.TempDir()
		writeProjectConfig(t, root, cfg)
		envelope := buildContextEnvelopeWithDeps(context.Background(), root, "inspect README.md during overdrive", true, testContextEnvelopeDeps(files, graph))
		if envelope.Intent == nil || len(envelope.Intent.Files) != 3 || envelope.Intent.FileConfidence != "mixed" || envelope.Intent.Scope != "single-file" || envelope.Intent.RiskLevel != "low" {
			t.Fatalf("intent = %#v", envelope.Intent)
		}
	})

	t.Run("inferred subsystem candidates do not set scope or risk", func(t *testing.T) {
		files := routingFiles("src/build/a.go", "src/build/b.go", "src/other.go")
		cfg := config.ProjectConfig{Routing: config.RoutingConfig{
			Subsystems: []config.Subsystem{{ID: "build", Keywords: []string{"overdrive"}, Paths: []string{"src/build"}}},
		}}
		graph := &scanner.FileGraph{Importers: map[string][]string{"src/build/a.go": {"src/other.go", "src/other.go", "src/other.go"}}}
		root := t.TempDir()
		writeProjectConfig(t, root, cfg)
		envelope := buildContextEnvelopeWithDeps(context.Background(), root, "speed up overdrive", true, testContextEnvelopeDeps(files, graph))
		if envelope.Intent == nil || len(envelope.Intent.Files) != 2 || envelope.Intent.FileConfidence != "inferred" || envelope.Intent.Scope != "unknown" || envelope.Intent.RiskLevel != "unknown" {
			t.Fatalf("intent = %#v", envelope.Intent)
		}
	})
}

func routingFiles(paths ...string) []scanner.FileInfo {
	files := make([]scanner.FileInfo, len(paths))
	for i, path := range paths {
		files[i] = scanner.FileInfo{Path: path}
	}
	return files
}

func TestContextFileIndexPrefixes(t *testing.T) {
	index := newContextFileIndex(routingFiles("src/build/z.go", "src/build/a.go", "src/other.go"), false)
	var got []string
	index.forPrefix("src/build", 1, func(path string) bool {
		got = append(got, path)
		return false
	})
	if want := []string{"src/build/a.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("prefix files = %#v, want %#v", got, want)
	}
}

func TestContextFileIndexReusesOrderedInventory(t *testing.T) {
	index := newContextFileIndex(routingFiles("Root/a.go", "Root/b.go"), true)
	index.preparePaths()
	if !index.useInventory {
		t.Fatal("ordered inventory used fallback index")
	}

	index = newContextFileIndex(routingFiles("Root/a.go", "alpha/b.go"), true)
	index.preparePaths()
	if index.useInventory {
		t.Fatal("unordered case-folded inventory used binary search")
	}
	if len(index.sortedPaths) != 0 {
		t.Fatal("unordered case-folded inventory built a fallback index")
	}
	match, unique := index.uniqueExact("alpha/b.go")
	if !unique || match != "alpha/b.go" {
		t.Fatalf("direct-scan exact match = %q, %v", match, unique)
	}

	index = newContextFileIndex(routingFiles(`Root\a.go`, `Root\b.go`), true)
	index.preparePaths()
	if !index.useInventory {
		t.Fatal("ordered backslash-separated inventory used fallback index")
	}
	match, unique = index.uniqueExact("root/a.go")
	if !unique || match != "Root/a.go" {
		t.Fatalf("backslash-separated exact match = %q, %v", match, unique)
	}
	var prefixed []string
	index.forPrefix("root", 2, func(path string) bool {
		prefixed = append(prefixed, path)
		return false
	})
	if want := []string{"Root/a.go", "Root/b.go"}; !reflect.DeepEqual(prefixed, want) {
		t.Fatalf("backslash-separated prefix matches = %#v, want %#v", prefixed, want)
	}
	basenames := index.uniqueBasenames([]string{"b.go"})
	if match, unique = basenames.unique("b.go"); !unique || match != "Root/b.go" {
		t.Fatalf("backslash-separated basename match = %q, %v", match, unique)
	}

	index = newContextFileIndex(routingFiles("Root/a.go", `Root\a.go`), true)
	index.preparePaths()
	if index.useInventory {
		t.Fatal("separator-normalized duplicate reused inventory")
	}
}

func TestContextFileIndexPrefixesRespectBoundaries(t *testing.T) {
	index := newContextFileIndex(routingFiles("src/build.go", "src/build", "src/build/a.go", "src/building/b.go"), false)
	var got []string
	index.forPrefix("src/build", 10, func(path string) bool {
		got = append(got, path)
		return false
	})
	if want := []string{"src/build", "src/build/a.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("boundary files = %#v, want %#v", got, want)
	}
}

func TestContextFileIndexPrefixLimitCountsUniquePaths(t *testing.T) {
	index := newContextFileIndex(routingFiles("src/a.go", `src\a.go`, "src/b.go"), false)
	var got []string
	index.forPrefix("src", 2, func(path string) bool {
		got = append(got, path)
		return false
	})
	if want := []string{"src/a.go", "src/b.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("prefix files = %#v, want %#v", got, want)
	}
}

func TestContextFileIndexBasenamesUseLargeKeySet(t *testing.T) {
	for _, test := range []struct {
		name            string
		files           []scanner.FileInfo
		caseInsensitive bool
		want            string
	}{
		{name: "ordered", files: routingFiles("other/a.go", "pkg/a.go", "pkg/b.go", "pkg/c.go", "pkg/d.go", "pkg/e.go"), want: "pkg/e.go"},
		{name: "unordered", files: routingFiles("pkg/e.go", "pkg/d.go", "pkg/c.go", "pkg/b.go", "pkg/a.go", "other/a.go"), want: "pkg/e.go"},
		{name: "normalized fallback", files: routingFiles("./pkg/e.go", "pkg/d.go", "pkg/c.go", "pkg/b.go", "pkg/a.go", "other/a.go"), want: "pkg/e.go"},
		{name: "case-folded separators", files: routingFiles(`Other\a.go`, `Pkg\a.go`, `Pkg\b.go`, `Pkg\c.go`, `Pkg\d.go`, `Pkg\e.go`), caseInsensitive: true, want: "Pkg/e.go"},
	} {
		t.Run(test.name, func(t *testing.T) {
			index := newContextFileIndex(test.files, test.caseInsensitive)
			keys := []string{"a.go", "b.go", "c.go", "d.go", "e.go"}
			for keyIndex := range keys {
				keys[keyIndex] = index.key(keys[keyIndex])
			}
			matches := index.uniqueBasenames(keys)
			if _, unique := matches.unique(index.key("a.go")); unique {
				t.Fatal("ambiguous basename resolved")
			}
			if got, unique := matches.unique(index.key("e.go")); !unique || got != test.want {
				t.Fatalf("unique basename = %q, %v, want %s, true", got, unique, test.want)
			}
		})
	}

	index := newContextFileIndex(routingFiles("pkg/Ä.go", "pkg/b.go", "pkg/c.go", "pkg/d.go", "pkg/e.go"), true)
	keys := []string{index.key("ä.go"), "b.go", "c.go", "d.go", "e.go"}
	matches := index.uniqueBasenames(keys)
	if got, unique := matches.unique(index.key("ä.go")); !unique || got != "pkg/Ä.go" {
		t.Fatalf("Unicode basename = %q, %v, want pkg/Ä.go, true", got, unique)
	}
}

func TestContextFileIndexPrefixesRespectCaseFolding(t *testing.T) {
	files := routingFiles("src/Build/d.go", "Src/build/a.go", "SRC/BUILD/c.go", "src/build/b.go", "src/building/e.go")
	index := newContextFileIndex(files, true)
	if index.pathsReady {
		t.Fatal("case-folded inventory prepared eagerly")
	}
	var got []string
	index.forPrefix(index.key("src/build"), 2, func(path string) bool {
		got = append(got, path)
		return false
	})
	if want := []string{"SRC/BUILD/c.go", "Src/build/a.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("case-folded files = %#v, want %#v", got, want)
	}
	if !index.pathsReady {
		t.Fatal("case-folded inventory was not prepared on demand")
	}
	cfg := config.ProjectConfig{Routing: config.RoutingConfig{
		Subsystems: []config.Subsystem{{ID: "build", Keywords: []string{"build"}, Paths: []string{"src/build"}}},
	}}
	if got := resolveContextFilesWithCase("build", files, cfg, 1, true); !reflect.DeepEqual(got, []string{"SRC/BUILD/c.go"}) {
		t.Fatalf("case-folded top-k files = %#v, want first path", got)
	}

	index = newContextFileIndex(routingFiles("Ärea/a.go", "ärea/b.go"), true)
	got = nil
	index.forPrefix(index.key("ÄREA"), 2, func(path string) bool { got = append(got, path); return false })
	if want := []string{"Ärea/a.go", "ärea/b.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Unicode case-folded files = %#v, want %#v", got, want)
	}
}

func TestContextSubsystemMatchesUsesSharedRouteScoring(t *testing.T) {
	cfg := config.ProjectConfig{Routing: config.RoutingConfig{
		Retrieval: config.RetrievalConfig{Strategy: "keyword", TopK: 2},
		Subsystems: []config.Subsystem{
			{ID: "low", Keywords: []string{"build"}},
			{ID: "high", Keywords: []string{"build", "latency"}, Paths: []string{"cmd"}},
			{ID: "tie", Keywords: []string{"build"}},
		},
	}}
	prompt := "build latency in cmd"
	shared := matchSubsystemRoutes(prompt, cfg, cfg.RoutingTopKOrDefault())
	got := contextSubsystemMatches(prompt, cfg, cfg.RoutingTopKOrDefault())
	want := make([]int, len(shared))
	for i, match := range shared {
		want[i] = match.Index
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("route indices = %#v, want %#v", got, want)
	}
}
