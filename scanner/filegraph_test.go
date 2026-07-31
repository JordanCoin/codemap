package scanner

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestDetectLanguage(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{name: "go extension", path: "main.go", expected: "go"},
		{name: "uppercase extension", path: "handler.PY", expected: "python"},
		{name: "unknown extension", path: "README.md", expected: ""},
		{name: "no extension", path: "Makefile", expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectLanguage(tt.path)
			if got != tt.expected {
				t.Errorf("DetectLanguage(%q) = %q, want %q", tt.path, got, tt.expected)
			}
		})
	}
}

func TestNormalizeImport(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "removes quotes", input: "\"pkg/util\"", expected: "pkg/util"},
		{name: "python dots to slashes", input: "app.core.config", expected: filepath.Join("app", "core", "config")},
		{name: "relative dotted import unchanged", input: "../app.core", expected: "../app.core"},
		{name: "crate import converted", input: "crate::feature::parser", expected: filepath.Join("feature", "parser")},
		{name: "super import converted", input: "super::module::item", expected: filepath.Join("super", "module", "item")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeImport(tt.input)
			if got != tt.expected {
				t.Errorf("normalizeImport(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestBuildFileIndex(t *testing.T) {
	files := []FileInfo{
		{Path: "main.go"},
		{Path: filepath.Join("pkg", "util", "helpers.go")},
		{Path: filepath.Join("src", "app", "core", "config.py")},
	}

	idx := buildFileIndex(files, "example.com/project")

	tests := []struct {
		name string
		got  []string
		want []string
	}{
		{
			name: "exact lookup without extension",
			got:  idx.byExact[filepath.Join("pkg", "util", "helpers")],
			want: []string{filepath.Join("pkg", "util", "helpers.go")},
		},
		{
			name: "suffix lookup for nested path",
			got:  idx.bySuffix[filepath.Join("app", "core", "config.py")],
			want: []string{filepath.Join("src", "app", "core", "config.py")},
		},
		{
			name: "directory lookup",
			got:  idx.byDir[filepath.Join("pkg", "util")],
			want: []string{filepath.Join("pkg", "util", "helpers.go")},
		},
		{
			name: "go package lookup",
			got:  idx.goPkgs["example.com/project/pkg/util"],
			want: []string{filepath.Join("pkg", "util", "helpers.go")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !reflect.DeepEqual(tt.got, tt.want) {
				t.Errorf("%s: got %v, want %v", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestResolveRelative(t *testing.T) {
	files := []FileInfo{
		{Path: filepath.Join("pkg", "util", "helpers.go")},
		{Path: filepath.Join("pkg", "models", "user.go")},
	}
	idx := buildFileIndex(files, "")

	tests := []struct {
		name     string
		imp      string
		fromDir  string
		expected []string
	}{
		{
			name:     "same directory",
			imp:      "./helpers",
			fromDir:  filepath.Join("pkg", "util"),
			expected: []string{filepath.Join("pkg", "util", "helpers.go")},
		},
		{
			name:     "parent directory",
			imp:      "../models/user",
			fromDir:  filepath.Join("pkg", "util"),
			expected: []string{filepath.Join("pkg", "models", "user.go")},
		},
		{
			name:     "missing file",
			imp:      "./missing",
			fromDir:  filepath.Join("pkg", "util"),
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveRelative(tt.imp, tt.fromDir, idx, "go")
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("resolveRelative(%q, %q) = %v, want %v", tt.imp, tt.fromDir, got, tt.expected)
			}
		})
	}
}

func TestTrySuffixMatch(t *testing.T) {
	files := []FileInfo{
		{Path: filepath.Join("src", "pkg", "__init__.py")},
		{Path: filepath.Join("src", "auth", "service.ts")},
	}
	idx := buildFileIndex(files, "")

	tests := []struct {
		name       string
		normalized string
		language   string
		expected   []string
	}{
		{
			name:       "python package init fallback",
			normalized: filepath.Join("pkg"),
			language:   "python",
			expected:   []string{filepath.Join("src", "pkg", "__init__.py")},
		},
		{
			name:       "direct suffix with extension",
			normalized: filepath.Join("auth", "service"),
			language:   "typescript",
			expected:   []string{filepath.Join("src", "auth", "service.ts")},
		},
		{
			name:       "no match",
			normalized: "missing/path",
			language:   "typescript",
			expected:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := trySuffixMatch(tt.normalized, idx, tt.language)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("trySuffixMatch(%q) = %v, want %v", tt.normalized, got, tt.expected)
			}
		})
	}
}

func TestFuzzyResolve(t *testing.T) {
	files := []FileInfo{
		{Path: filepath.Join("pkg", "util", "helpers.go")},
		{Path: filepath.Join("pkg", "models", "user.go")},
		{Path: filepath.Join("src", "modules", "auth", "login.ts")},
		{Path: filepath.Join("src", "shared", "config.py")},
		{Path: filepath.Join("src", "services", "__init__.py")},
	}
	idx := buildFileIndex(files, "example.com/project")

	pathAliases := map[string][]string{
		"@modules/*": {"src/modules/*"},
	}

	tests := []struct {
		name     string
		imp      string
		fromFile string
		aliases  map[string][]string
		baseURL  string
		expected []string
	}{
		{
			name:     "go package strategy",
			imp:      "example.com/project/pkg/util",
			fromFile: filepath.Join("pkg", "models", "user.go"),
			aliases:  nil,
			baseURL:  "",
			expected: []string{filepath.Join("pkg", "util", "helpers.go")},
		},
		{
			name:     "relative strategy",
			imp:      "../util/helpers",
			fromFile: filepath.Join("pkg", "models", "user.go"),
			aliases:  nil,
			baseURL:  "",
			expected: []string{filepath.Join("pkg", "util", "helpers.go")},
		},
		{
			name:     "path alias strategy",
			imp:      "@modules/auth/login",
			fromFile: filepath.Join("src", "app.ts"),
			aliases:  pathAliases,
			baseURL:  ".",
			expected: []string{filepath.Join("src", "modules", "auth", "login.ts")},
		},
		{
			name:     "normalized dotted exact strategy",
			imp:      "src.shared.config",
			fromFile: filepath.Join("src", "app.py"),
			aliases:  nil,
			baseURL:  "",
			expected: []string{filepath.Join("src", "shared", "config.py")},
		},
		{
			name:     "suffix strategy with python init",
			imp:      "services",
			fromFile: filepath.Join("src", "shared", "config.py"),
			aliases:  nil,
			baseURL:  "",
			expected: []string{filepath.Join("src", "services", "__init__.py")},
		},
		{
			name:     "no match",
			imp:      "missing.pkg.path",
			fromFile: filepath.Join("src", "shared", "config.py"),
			aliases:  nil,
			baseURL:  "",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fuzzyResolve(tt.imp, tt.fromFile, idx, "example.com/project", tt.aliases, tt.baseURL)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("fuzzyResolve(%q) = %v, want %v", tt.imp, got, tt.expected)
			}
		})
	}
}

func TestFuzzyResolveGoImportsFailClosed(t *testing.T) {
	module := "example.com/project"
	onlyGo := filepath.Join("pkg", "only", "only.go")
	files := []FileInfo{
		{Path: "main.go"},
		{Path: filepath.Join("cmd", "context.go")},
		{Path: filepath.Join("mcp", "main.go")},
		{Path: filepath.Join("foo", "main.go")},
		{Path: onlyGo},
		{Path: filepath.Join("pkg", "only", "only_test.go")},
		{Path: filepath.Join("fixtures", "example.com", "project", "missing.go")},
		{Path: filepath.Join("src", "shared", "config.py")},
	}
	idx := buildFileIndex(files, module)

	tests := []struct {
		name   string
		imp    string
		module string
		want   []string
	}{
		{
			name:   "exact module root resolves",
			imp:    module,
			module: module,
			want:   []string{"main.go"},
		},
		{
			name:   "local package ignores test files",
			imp:    module + "/pkg/only",
			module: module,
			want:   []string{onlyGo},
		},
		{
			name:   "standard library import cannot match a local filename",
			imp:    "context",
			module: module,
		},
		{
			name:   "external package cannot match a local directory",
			imp:    "github.com/modelcontextprotocol/go-sdk/mcp",
			module: module,
		},
		{
			name:   "module prefix sibling is not local",
			imp:    "example.com/project-tools/foo",
			module: module,
		},
		{
			name:   "unknown local package cannot fall through to fuzzy matching",
			imp:    module + "/missing",
			module: module,
		},
		{
			name: "missing module fails closed",
			imp:  "context",
		},
		{
			name: "relative import keeps existing resolution",
			imp:  "../foo/main",
			want: []string{filepath.Join("foo", "main.go")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fuzzyResolve(tt.imp, filepath.Join("cmd", "caller.go"), idx, tt.module, nil, "")
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("fuzzyResolve(%q) = %v, want %v", tt.imp, got, tt.want)
			}
		})
	}
}

func TestBuildFileGraphGoPackageTargets(t *testing.T) {
	root := t.TempDir()
	module := "example.com/project"
	paths := []string{
		"go.mod",
		"cmd/caller.go",
		"cmd/caller_test.go",
		"cmd/context.go",
		"mcp/main.go",
		"fixtures/example.com/project/missing.go",
		"pkg/only/only.go",
		"pkg/only/only_test.go",
		"pkg/testonly/only_test.go",
		"pkg/multi/a.go",
		"pkg/multi/b.go",
	}
	for _, path := range paths {
		fullPath := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		content := []byte(nil)
		if path == "go.mod" {
			content = []byte("module " + module + "\n\ngo 1.24.0\n")
		}
		if err := os.WriteFile(fullPath, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	imports := []string{
		"context",
		"github.com/modelcontextprotocol/go-sdk/mcp",
		"example.com/project-tools/pkg/only",
		module + "/missing",
		module + "/pkg/only",
		module + "/pkg/testonly",
		module + "/pkg/multi",
	}
	analyses := []FileAnalysis{
		{Path: filepath.FromSlash("cmd/caller.go"), Language: "go", Imports: imports},
		{Path: filepath.FromSlash("cmd/caller_test.go"), Language: "go", Imports: imports},
	}

	graph, err := BuildFileGraphFromAnalyses(root, analyses)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{filepath.FromSlash("pkg/only/only.go")}
	for _, caller := range []string{"cmd/caller.go", "cmd/caller_test.go"} {
		caller = filepath.FromSlash(caller)
		if got := graph.Imports[caller]; !reflect.DeepEqual(got, want) {
			t.Errorf("imports[%q] = %v, want %v", caller, got, want)
		}
	}
	if got := graph.Importers[want[0]]; !reflect.DeepEqual(got, []string{
		filepath.FromSlash("cmd/caller.go"),
		filepath.FromSlash("cmd/caller_test.go"),
	}) {
		t.Errorf("importers[%q] = %v", want[0], got)
	}
}

func TestLanguageCompatibility(t *testing.T) {
	tests := []struct {
		name      string
		importer  string
		candidate string
		want      bool
	}{
		{name: "javascript to typescript", importer: "javascript", candidate: "typescript", want: true},
		{name: "typescript to javascript", importer: "typescript", candidate: "javascript", want: true},
		{name: "c to cpp", importer: "c", candidate: "cpp", want: true},
		{name: "cpp to c", importer: "cpp", candidate: "c", want: true},
		{name: "java to kotlin", importer: "java", candidate: "kotlin", want: true},
		{name: "kotlin to scala", importer: "kotlin", candidate: "scala", want: true},
		{name: "scala to java", importer: "scala", candidate: "java", want: true},
		{name: "python singleton", importer: "python", candidate: "python", want: true},
		{name: "csharp singleton", importer: "csharp", candidate: "csharp", want: true},
		{name: "go singleton", importer: "go", candidate: "go", want: true},
		{name: "rust singleton", importer: "rust", candidate: "rust", want: true},
		{name: "swift singleton", importer: "swift", candidate: "swift", want: true},
		{name: "ruby singleton", importer: "ruby", candidate: "ruby", want: true},
		{name: "php singleton", importer: "php", candidate: "php", want: true},
		{name: "lua singleton", importer: "lua", candidate: "lua", want: true},
		{name: "elixir singleton", importer: "elixir", candidate: "elixir", want: true},
		{name: "solidity singleton", importer: "solidity", candidate: "solidity", want: true},
		{name: "bash singleton", importer: "bash", candidate: "bash", want: true},
		{name: "cross family rejected", importer: "typescript", candidate: "rust", want: false},
		{name: "unknown importer rejected", importer: "", candidate: "typescript", want: false},
		{name: "unknown candidate rejected", importer: "typescript", candidate: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := languagesCompatible(tt.importer, tt.candidate); got != tt.want {
				t.Fatalf("languagesCompatible(%q, %q) = %v, want %v", tt.importer, tt.candidate, got, tt.want)
			}
		})
	}
}

func TestBuildFileGraphImportResolutionBoundaries(t *testing.T) {
	root := t.TempDir()
	paths := []string{
		"crates/path.rs",
		"crates/mod.rs",
		"web/path.ts",
		"web/url.ts",
		"web/target.scala",
		"web/target.ts",
		"web/icon.js",
		"web/app.ts",
		"web/relative.ts",
		"native/header.hpp",
		"native/main.c",
		"jvm/shared/Model.kt",
		"jvm/App.java",
		"python/services/__init__.py",
		"python/services/index.ts",
		"python/main.py",
		"MyApp/Models/User.cs",
		"MyApp/Models/helper.py",
		"MyApp/Program.cs",
		"pkg/single/util.go",
		"pkg/multi/a.go",
		"pkg/multi/b.go",
		"cmd/main.go",
		"cmd/multi.go",
	}
	for _, path := range paths {
		fullPath := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/project\n\ngo 1.24.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte(`{
  "compilerOptions": {
    "baseUrl": ".",
    "paths": {"@local/*": ["web/*"]}
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	analyses := []FileAnalysis{
		{Path: "crates/mod.rs", Language: "rust", Imports: []string{"crate::path"}},
		{Path: "web/icon.js", Language: "javascript", Imports: []string{"path", "url", "node:path"}},
		{Path: "web/app.ts", Language: "typescript", Imports: []string{"path", "url", "node:path"}},
		{Path: "web/relative.ts", Language: "typescript", Imports: []string{"./path", "./target", "@local/path"}},
		{Path: "native/main.c", Language: "c", Imports: []string{"./header"}},
		{Path: "jvm/App.java", Language: "java", Imports: []string{"jvm.shared.Model"}},
		{Path: "python/main.py", Language: "python", Imports: []string{"python.services"}},
		{Path: "MyApp/Program.cs", Language: "csharp", Imports: []string{"MyApp.Models"}},
		{Path: "cmd/main.go", Language: "go", Imports: []string{"example.com/project/pkg/single"}},
		{Path: "cmd/multi.go", Language: "go", Imports: []string{"example.com/project/pkg/multi"}},
		{Path: "README.md", Language: "", Imports: []string{"path"}},
	}

	fg, err := BuildFileGraphFromAnalyses(root, analyses)
	if err != nil {
		t.Fatal(err)
	}

	assertImporters := func(path string, want []string) {
		t.Helper()
		got := append([]string(nil), fg.Importers[filepath.FromSlash(path)]...)
		sort.Strings(got)
		sort.Strings(want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("importers[%q] = %v, want %v", path, got, want)
		}
	}
	assertImports := func(path string, want []string) {
		t.Helper()
		got := append([]string(nil), fg.Imports[filepath.FromSlash(path)]...)
		sort.Strings(got)
		sort.Strings(want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("imports[%q] = %v, want %v", path, got, want)
		}
	}

	assertImporters("crates/path.rs", []string{"crates/mod.rs"})
	assertImporters("web/url.ts", nil)
	assertImports("web/relative.ts", []string{"web/path.ts", "web/target.ts"})
	assertImports("native/main.c", []string{"native/header.hpp"})
	assertImports("jvm/App.java", []string{"jvm/shared/Model.kt"})
	assertImports("python/main.py", []string{"python/services/__init__.py"})
	assertImports("MyApp/Program.cs", []string{"MyApp/Models/User.cs"})
	assertImports("cmd/main.go", []string{"pkg/single/util.go"})
	assertImports("cmd/multi.go", nil)
	assertImports("README.md", nil)
}

func TestFilteredImportResolutionTargets(t *testing.T) {
	root := t.TempDir()
	module := "example.com/filtered"
	files := map[string]string{
		"go.mod":                         "module " + module + "\n\ngo 1.26\n",
		"tsconfig.json":                  `{"compilerOptions":{"baseUrl":".","paths":{"@blocked/*":["blocked/*"]}}}`,
		"src/app.ts":                     `import { blocked } from "@blocked/target"` + "\n",
		"blocked/target.ts":              "export const blocked = true\n",
		"cmd/caller.go":                  "package cmd\n\nimport _ \"" + module + "/pkg/filteredpkg\"\n",
		"pkg/filteredpkg/target.go":      "package target\n",
		"pkg/filteredpkg/target_test.go": "package target\n",
	}
	for path, content := range files {
		fullPath := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	filters := Filters{Exclude: []string{
		filepath.FromSlash("blocked/target.ts"),
		filepath.FromSlash("pkg/filteredpkg/target.go"),
	}}
	scanned, err := ScanFiles(root, NewGitIgnoreCache(root), filters.Only, filters.Exclude)
	if err != nil {
		t.Fatalf("ScanFiles() error: %v", err)
	}
	retainedTestTarget := false
	for _, file := range scanned {
		if file.Path == filepath.FromSlash("pkg/filteredpkg/target_test.go") {
			retainedTestTarget = true
			break
		}
	}
	if !retainedTestTarget {
		t.Fatalf("fixture did not retain the _test.go-only Go package target; scanned = %#v", scanned)
	}
	analyses := []FileAnalysis{
		{Path: filepath.FromSlash("src/app.ts"), Language: "typescript", Imports: []string{"@blocked/target"}},
		{Path: filepath.FromSlash("cmd/caller.go"), Language: "go", Imports: []string{module + "/pkg/filteredpkg"}},
	}

	assertFilteredTargets := func(t *testing.T, graph *FileGraph) {
		t.Helper()
		if got := graph.Imports[filepath.FromSlash("src/app.ts")]; len(got) != 0 {
			t.Errorf("filtered alias target resolved: %v", got)
		}
		if got := graph.Imports[filepath.FromSlash("cmd/caller.go")]; len(got) != 0 {
			t.Errorf("filtered Go package target resolved: %v", got)
		}
		if got := graph.Packages[module+"/pkg/filteredpkg"]; len(got) != 0 {
			t.Errorf("filtered Go package remained indexed: %v", got)
		}
	}

	precomputed, err := BuildFileGraphFromFilteredAnalyses(root, analyses, filters)
	if err != nil {
		t.Fatalf("BuildFileGraphFromFilteredAnalyses() error: %v", err)
	}
	assertFilteredTargets(t, precomputed)

	t.Run("live graph matches precomputed graph", func(t *testing.T) {
		if !NewAstGrepAnalyzer().Available() {
			t.Skip("ast-grep not available")
		}
		live, err := BuildFileGraphWithFilters(root, filters)
		if err != nil {
			t.Fatalf("BuildFileGraphWithFilters() error: %v", err)
		}
		assertFilteredTargets(t, live)
		if !reflect.DeepEqual(live.Imports, precomputed.Imports) {
			t.Errorf("live imports = %#v, precomputed imports = %#v", live.Imports, precomputed.Imports)
		}
		if !reflect.DeepEqual(live.Importers, precomputed.Importers) {
			t.Errorf("live importers = %#v, precomputed importers = %#v", live.Importers, precomputed.Importers)
		}
		if !reflect.DeepEqual(live.Packages, precomputed.Packages) {
			t.Errorf("live packages = %#v, precomputed packages = %#v", live.Packages, precomputed.Packages)
		}
	})
}

func TestDetectModule(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{name: "module exists", content: "module github.com/example/project\n\ngo 1.22\n", expected: "github.com/example/project"},
		{name: "module missing", content: "go 1.22\n", expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if tt.content != "" {
				if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(tt.content), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			got := detectModule(root)
			if got != tt.expected {
				t.Errorf("detectModule() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestFileGraphHubAndConnectedFiles(t *testing.T) {
	fg := &FileGraph{
		Imports: map[string][]string{
			"a.go": {"b.go", "c.go"},
			"d.go": {"a.go"},
		},
		Importers: map[string][]string{
			"a.go": {"d.go"},
			"b.go": {"a.go", "x.go", "y.go"},
			"c.go": {"a.go"},
		},
	}

	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{name: "hub file", path: "b.go", expected: true},
		{name: "non hub file", path: "a.go", expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fg.IsHub(tt.path)
			if got != tt.expected {
				t.Errorf("IsHub(%q) = %v, want %v", tt.path, got, tt.expected)
			}
		})
	}

	t.Run("hub files list", func(t *testing.T) {
		got := fg.HubFiles()
		sort.Strings(got)
		want := []string{"b.go"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("HubFiles() = %v, want %v", got, want)
		}
	})

	t.Run("connected files", func(t *testing.T) {
		got := fg.ConnectedFiles("a.go")
		sort.Strings(got)
		want := []string{"b.go", "c.go", "d.go"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("ConnectedFiles() = %v, want %v", got, want)
		}
	})
}

func TestTryDirMatch(t *testing.T) {
	files := []FileInfo{
		{Path: filepath.Join("MyApp", "Models", "User.cs")},
		{Path: filepath.Join("MyApp", "Models", "Product.cs")},
		{Path: filepath.Join("MyApp", "Services", "UserService.cs")},
	}
	idx := buildFileIndex(files, "")

	t.Run("matches directory with multiple files", func(t *testing.T) {
		got := tryDirMatch(filepath.Join("MyApp", "Models"), idx, "csharp")
		sort.Strings(got)
		want := []string{
			filepath.Join("MyApp", "Models", "Product.cs"),
			filepath.Join("MyApp", "Models", "User.cs"),
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("tryDirMatch = %v, want %v", got, want)
		}
	})

	t.Run("matches directory with single file", func(t *testing.T) {
		got := tryDirMatch(filepath.Join("MyApp", "Services"), idx, "csharp")
		want := []string{filepath.Join("MyApp", "Services", "UserService.cs")}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("tryDirMatch = %v, want %v", got, want)
		}
	})

	t.Run("suffix match strips namespace prefix", func(t *testing.T) {
		// Files are in Models/ but namespace is ProjectName/Models
		files2 := []FileInfo{
			{Path: filepath.Join("Models", "User.cs")},
			{Path: filepath.Join("Models", "Product.cs")},
		}
		idx2 := buildFileIndex(files2, "")
		got := tryDirMatch(filepath.Join("ProjectName", "Models"), idx2, "csharp")
		sort.Strings(got)
		want := []string{
			filepath.Join("Models", "Product.cs"),
			filepath.Join("Models", "User.cs"),
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("tryDirMatch suffix = %v, want %v", got, want)
		}
	})

	t.Run("no match for missing directory", func(t *testing.T) {
		got := tryDirMatch(filepath.Join("MyApp", "Missing"), idx, "csharp")
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})
}

func TestFuzzyResolveCsharpNamespace(t *testing.T) {
	userCS := filepath.Join("MyApp", "Models", "User.cs")
	productCS := filepath.Join("MyApp", "Models", "Product.cs")
	serviceCS := filepath.Join("MyApp", "Services", "UserService.cs")

	files := []FileInfo{
		{Path: userCS},
		{Path: productCS},
		{Path: serviceCS},
	}
	idx := buildFileIndex(files, "")

	t.Run("namespace with multiple files resolves via directory", func(t *testing.T) {
		got := fuzzyResolve("MyApp.Models", "MyApp/Services/UserService.cs", idx, "", nil, "")
		sort.Strings(got)
		want := []string{productCS, userCS}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("fuzzyResolve(MyApp.Models) = %v, want %v", got, want)
		}
	})

	t.Run("namespace with single file resolves via directory", func(t *testing.T) {
		got := fuzzyResolve("MyApp.Services", "MyApp/Program.cs", idx, "", nil, "")
		want := []string{serviceCS}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("fuzzyResolve(MyApp.Services) = %v, want %v", got, want)
		}
	})

	t.Run("external namespace does not resolve", func(t *testing.T) {
		got := fuzzyResolve("System.Collections.Generic", "MyApp/Program.cs", idx, "", nil, "")
		if got != nil {
			t.Errorf("expected nil for external namespace, got %v", got)
		}
	})
}
