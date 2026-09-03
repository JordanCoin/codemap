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
		files := routingFiles("./cmd/context.go", "cmd/context.go", "internal/context.go")
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
		files := routingFiles("Cmd/Foo.go", "cmd/foo.go")
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
	index.forPrefix("src/build", func(path string) bool {
		got = append(got, path)
		return false
	})
	if want := []string{"src/build/a.go", "src/build/z.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("prefix files = %#v, want %#v", got, want)
	}
}

func TestContextFileIndexPrefixesRespectBoundaries(t *testing.T) {
	index := newContextFileIndex(routingFiles("src/build.go", "src/build/a.go", "src/building/b.go"), false)
	var got []string
	index.forPrefix("src/build", func(path string) bool {
		got = append(got, path)
		return false
	})
	if want := []string{"src/build/a.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("boundary files = %#v, want %#v", got, want)
	}
}

func TestContextFileIndexPrefixesRespectCaseFolding(t *testing.T) {
	files := routingFiles("Src/Build/z.go", "src/build/a.go", "src/building/b.go")
	index := newContextFileIndex(files, true)
	if want := []string{"Src/Build/z.go", "src/build/a.go"}; !reflect.DeepEqual(index.prefixPaths["src/build"], want) {
		t.Fatalf("case-folded prefix index = %#v, want %#v", index.prefixPaths["src/build"], want)
	}
	var got []string
	index.forPrefix(index.key("src/build"), func(path string) bool {
		got = append(got, path)
		return false
	})
	if want := []string{"Src/Build/z.go", "src/build/a.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("case-folded files = %#v, want %#v", got, want)
	}
	cfg := config.ProjectConfig{Routing: config.RoutingConfig{
		Subsystems: []config.Subsystem{{ID: "build", Keywords: []string{"build"}, Paths: []string{"src/build"}}},
	}}
	if got := resolveContextFilesWithCase("build", files, cfg, 1, true); !reflect.DeepEqual(got, []string{"Src/Build/z.go"}) {
		t.Fatalf("case-folded top-k files = %#v, want first path", got)
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
