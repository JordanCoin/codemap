package cmd

import (
	"context"
	"reflect"
	"testing"

	"codemap/config"
	"codemap/scanner"
)

func TestContextLexicalRouting(t *testing.T) {
	t.Run("exact path wins before stem", func(t *testing.T) {
		files := routingFiles("cmd/context.go", "internal/helper.go")
		got := resolveContextFilesWithCase("inspect cmd/context.go and context", files, config.ProjectConfig{}, 3, false)
		if want := []string{"cmd/context.go"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("files = %#v, want %#v", got, want)
		}
	})

	t.Run("unique extension basename and Rust stem", func(t *testing.T) {
		files := routingFiles("mcp/server.go", "src/final_build.rs")
		got := resolveContextFilesWithCase("connect server.go to final_build", files, config.ProjectConfig{}, 3, false)
		if want := []string{"mcp/server.go", "src/final_build.rs"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("files = %#v, want %#v", got, want)
		}
	})

	t.Run("ambiguous basename and stem stay unresolved", func(t *testing.T) {
		files := routingFiles("cmd/context.go", "internal/context.go", "src/graph.rs", "mcp/graph.go")
		got := resolveContextFilesWithCase("inspect context.go and graph", files, config.ProjectConfig{}, 4, false)
		if len(got) != 0 {
			t.Fatalf("ambiguous files = %#v, want none", got)
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

	t.Run("absolute and volume paths stay unresolved", func(t *testing.T) {
		files := routingFiles("/tmp/target.go", "C:/tmp/target.go", "//server/share.go")
		got := resolveContextFilesWithCase("inspect /tmp/target.go C:\\tmp\\target.go //server/share.go", files, config.ProjectConfig{}, 3, true)
		if len(got) != 0 {
			t.Fatalf("absolute files = %#v, want none", got)
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
		got := resolveContextFilesWithCase("alpha beta gamma", files, config.ProjectConfig{}, 2, false)
		if want := []string{"src/alpha.go", "src/beta.go"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("files = %#v, want %#v", got, want)
		}
	})

	t.Run("subsystem paths are deterministic and bounded", func(t *testing.T) {
		files := routingFiles("src/build/c.go", "src/build/a.go", "src/build/b.go", "src/other.go")
		cfg := config.ProjectConfig{Routing: config.RoutingConfig{
			Subsystems: []config.Subsystem{{ID: "build", Keywords: []string{"overdrive"}, Paths: []string{"src/build"}}},
		}}
		got := resolveContextFilesWithCase("speed up overdrive", files, cfg, 2, false)
		if want := []string{"src/build/a.go", "src/build/b.go"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("files = %#v, want %#v", got, want)
		}
	})

	t.Run("duplicate subsystem ids retain each path", func(t *testing.T) {
		files := routingFiles("alpha/a.go", "beta/b.go")
		cfg := config.ProjectConfig{Routing: config.RoutingConfig{Subsystems: []config.Subsystem{
			{ID: "shared", Keywords: []string{"overdrive"}, Paths: []string{"alpha"}},
			{ID: "shared", Keywords: []string{"overdrive"}, Paths: []string{"beta"}},
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
		got := resolveContextFilesWithCase("trace mcp -> final_build -> graph", files, cfg, 3, false)
		want := []string{"src/final_build.rs", "src/graph.rs", "mcp/server.go"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("files = %#v, want %#v", got, want)
		}
	})

	t.Run("resolved stem drives intent risk", func(t *testing.T) {
		files := routingFiles("src/final_build.rs", "a.rs", "b.rs", "c.rs")
		graph := &scanner.FileGraph{
			Imports: map[string][]string{
				"a.rs": {"src/final_build.rs"}, "b.rs": {"src/final_build.rs"}, "c.rs": {"src/final_build.rs"},
			},
			Importers: map[string][]string{"src/final_build.rs": {"a.rs", "b.rs", "c.rs"}},
		}
		envelope := buildContextEnvelopeWithDeps(context.Background(), t.TempDir(), "refactor final_build", true, testContextEnvelopeDeps(files, graph))
		if envelope.Intent == nil || !reflect.DeepEqual(envelope.Intent.Files, []string{"src/final_build.rs"}) || envelope.Intent.RiskLevel != "medium" {
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
