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
			got := resolveRelative(tt.imp, tt.fromDir, idx)
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
		expected   []string
	}{
		{
			name:       "python package init fallback",
			normalized: filepath.Join("pkg"),
			expected:   []string{filepath.Join("src", "pkg", "__init__.py")},
		},
		{
			name:       "direct suffix with extension",
			normalized: filepath.Join("auth", "service"),
			expected:   []string{filepath.Join("src", "auth", "service.ts")},
		},
		{
			name:       "no match",
			normalized: "missing/path",
			expected:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := trySuffixMatch(tt.normalized, idx)
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
			fromFile: filepath.Join("src", "shared", "config.py"),
			aliases:  pathAliases,
			baseURL:  ".",
			expected: []string{filepath.Join("src", "modules", "auth", "login.ts")},
		},
		{
			name:     "normalized dotted exact strategy",
			imp:      "src.shared.config",
			fromFile: filepath.Join("src", "modules", "auth", "login.ts"),
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
got := tryDirMatch(filepath.Join("MyApp", "Models"), idx)
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
got := tryDirMatch(filepath.Join("MyApp", "Services"), idx)
want := []string{filepath.Join("MyApp", "Services", "UserService.cs")}
if !reflect.DeepEqual(got, want) {
t.Errorf("tryDirMatch = %v, want %v", got, want)
}
})

t.Run("no match for missing directory", func(t *testing.T) {
got := tryDirMatch(filepath.Join("MyApp", "Missing"), idx)
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
