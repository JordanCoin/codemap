package scanner

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func sortedCopy(items []string) []string {
	cp := append([]string(nil), items...)
	sort.Strings(cp)
	return cp
}

func TestBuildFileIndex(t *testing.T) {
	files := []FileInfo{
		{Path: "main.go"},
		{Path: "pkg/util/helper.go"},
		{Path: "web/src/app.ts"},
	}

	idx := buildFileIndex(files, "example.com/proj")

	if got := idx.byExact["main"]; len(got) != 1 || got[0] != "main.go" {
		t.Fatalf("expected no-ext main to resolve main.go, got %v", got)
	}

	expectedSuffix := []string{"pkg/util/helper.go"}
	if got := idx.bySuffix["util/helper"]; !reflect.DeepEqual(got, expectedSuffix) {
		t.Fatalf("expected suffix util/helper => %v, got %v", expectedSuffix, got)
	}

	goPkg := "example.com/proj/pkg/util"
	if got := idx.goPkgs[goPkg]; len(got) != 1 || got[0] != "pkg/util/helper.go" {
		t.Fatalf("expected go pkg %q to contain helper.go, got %v", goPkg, got)
	}
}

func TestNormalizeImport(t *testing.T) {
	tests := []struct {
		name string
		imp  string
		want string
	}{
		{name: "quoted path", imp: "\"src/app\"", want: "src/app"},
		{name: "python dotted", imp: "app.core.config", want: filepath.Join("app", "core", "config")},
		{name: "crate prefix", imp: "crate::parser::ast", want: filepath.Join("parser", "ast")},
		{name: "super prefix", imp: "super::models::user", want: filepath.Join("super", "models", "user")},
		{name: "already slash path", imp: "pkg/sub/module", want: "pkg/sub/module"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeImport(tt.imp); got != tt.want {
				t.Fatalf("normalizeImport(%q) = %q, want %q", tt.imp, got, tt.want)
			}
		})
	}
}

func TestResolveRelative(t *testing.T) {
	idx := buildFileIndex([]FileInfo{
		{Path: "pkg/config/settings.go"},
		{Path: "pkg/shared/logger.go"},
	}, "")

	tests := []struct {
		name    string
		imp     string
		fromDir string
		want    []string
	}{
		{
			name:    "current dir import",
			imp:     "./settings",
			fromDir: "pkg/config",
			want:    []string{"pkg/config/settings.go"},
		},
		{
			name:    "parent dir import",
			imp:     "../shared/logger",
			fromDir: "pkg/config",
			want:    []string{"pkg/shared/logger.go"},
		},
		{
			name:    "missing import",
			imp:     "../shared/missing",
			fromDir: "pkg/config",
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveRelative(tt.imp, tt.fromDir, idx); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("resolveRelative(%q, %q) = %v, want %v", tt.imp, tt.fromDir, got, tt.want)
			}
		})
	}
}

func TestFuzzyResolve(t *testing.T) {
	idx := buildFileIndex([]FileInfo{
		{Path: "pkg/util/helper.go"},
		{Path: "src/modules/auth/login.ts"},
		{Path: "src/shared/math.ts"},
		{Path: "services/payment/client.py"},
	}, "example.com/proj")

	aliases := map[string][]string{
		"@modules/*": {"src/modules/*"},
		"@math":      {"src/shared/math"},
	}

	tests := []struct {
		name      string
		imp       string
		fromFile  string
		module    string
		aliases   map[string][]string
		baseURL   string
		want      []string
		wantIsNil bool
	}{
		{
			name:     "go module import",
			imp:      "example.com/proj/pkg/util",
			fromFile: "main.go",
			module:   "example.com/proj",
			want:     []string{"pkg/util/helper.go"},
		},
		{
			name:     "relative import",
			imp:      "./helper",
			fromFile: "pkg/util/file.go",
			module:   "example.com/proj",
			want:     []string{"pkg/util/helper.go"},
		},
		{
			name:     "path alias with wildcard",
			imp:      "@modules/auth/login",
			fromFile: "web/app.ts",
			module:   "example.com/proj",
			aliases:  aliases,
			want:     []string{"src/modules/auth/login.ts"},
		},
		{
			name:     "exact path alias",
			imp:      "@math",
			fromFile: "web/app.ts",
			module:   "example.com/proj",
			aliases:  aliases,
			want:     []string{"src/shared/math.ts"},
		},
		{
			name:     "suffix resolution from dotted import",
			imp:      "services.payment.client",
			fromFile: "runner.py",
			module:   "example.com/proj",
			want:     []string{"services/payment/client.py"},
		},
		{
			name:      "unresolvable import",
			imp:       "does/not/exist",
			fromFile:  "main.go",
			module:    "example.com/proj",
			wantIsNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fuzzyResolve(tt.imp, tt.fromFile, idx, tt.module, tt.aliases, tt.baseURL)
			if tt.wantIsNil {
				if got != nil {
					t.Fatalf("expected nil result, got %v", got)
				}
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("fuzzyResolve(%q) = %v, want %v", tt.imp, got, tt.want)
			}
		})
	}
}

func TestDetectModule(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, root string)
		wantMod string
	}{
		{
			name: "module found",
			setup: func(t *testing.T, root string) {
				t.Helper()
				writeTestFile(t, filepath.Join(root, "go.mod"), "module github.com/acme/project\n\ngo 1.24\n")
			},
			wantMod: "github.com/acme/project",
		},
		{
			name: "missing go mod",
			setup: func(t *testing.T, root string) {
				t.Helper()
			},
			wantMod: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			tt.setup(t, root)
			if got := detectModule(root); got != tt.wantMod {
				t.Fatalf("detectModule() = %q, want %q", got, tt.wantMod)
			}
		})
	}
}

func TestFileGraphHubAndConnectedFiles(t *testing.T) {
	fg := &FileGraph{
		Imports: map[string][]string{
			"service.go": {"db.go", "auth.go"},
			"worker.go":  {"db.go"},
		},
		Importers: map[string][]string{
			"db.go":   {"service.go", "worker.go", "api.go"},
			"auth.go": {"service.go"},
		},
	}

	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "hub file", path: "db.go", want: true},
		{name: "non hub file", path: "auth.go", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fg.IsHub(tt.path); got != tt.want {
				t.Fatalf("IsHub(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}

	hubs := sortedCopy(fg.HubFiles())
	if !reflect.DeepEqual(hubs, []string{"db.go"}) {
		t.Fatalf("HubFiles() = %v, want [db.go]", hubs)
	}

	connected := sortedCopy(fg.ConnectedFiles("service.go"))
	expectedConnected := []string{"auth.go", "db.go"}
	if !reflect.DeepEqual(connected, expectedConnected) {
		t.Fatalf("ConnectedFiles(service.go) = %v, want %v", connected, expectedConnected)
	}
}

func TestFileGraphDetectPathAliasesWithExtends(t *testing.T) {
	root := t.TempDir()

	writeTestFile(t, filepath.Join(root, "tsconfig.base.json"), `{
  "compilerOptions": {
    "baseUrl": "src",
    "paths": {
      "@shared/*": ["shared/*"],
      "@keep": ["shared/keep"]
    }
  }
}`)
	writeTestFile(t, filepath.Join(root, "tsconfig.json"), `{
  "extends": "./tsconfig.base",
  "compilerOptions": {
    "paths": {
      "@app/*": ["app/*"]
    }
  }
}`)

	paths, baseURL := detectPathAliases(root)

	if baseURL != "src" {
		t.Fatalf("baseURL = %q, want %q", baseURL, "src")
	}

	wantKeys := []string{"@app/*", "@keep", "@shared/*"}
	gotKeys := make([]string, 0, len(paths))
	for k := range paths {
		gotKeys = append(gotKeys, k)
	}
	sort.Strings(gotKeys)
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("path alias keys = %v, want %v", gotKeys, wantKeys)
	}
}

func TestFileGraphReadTSConfigAndResolvePathAlias(t *testing.T) {
	root := t.TempDir()

	paths, baseURL := readTSConfig(filepath.Join(root, "missing.json"), root)
	if paths != nil || baseURL != "" {
		t.Fatalf("missing config should return nil/empty, got paths=%v baseURL=%q", paths, baseURL)
	}

	invalid := filepath.Join(root, "invalid.json")
	writeTestFile(t, invalid, "{invalid")
	paths, baseURL = readTSConfig(invalid, root)
	if paths != nil || baseURL != "" {
		t.Fatalf("invalid config should return nil/empty, got paths=%v baseURL=%q", paths, baseURL)
	}

	idx := buildFileIndex([]FileInfo{
		{Path: "src/app/home.ts"},
		{Path: "src/lib/api/index.ts"},
		{Path: "src/lib/env.ts"},
	}, "")

	tests := []struct {
		name    string
		imp     string
		aliases map[string][]string
		baseURL string
		want    []string
	}{
		{
			name: "wildcard alias",
			imp:  "@app/home",
			aliases: map[string][]string{
				"@app/*": {"app/*"},
			},
			baseURL: "src",
			want:    []string{"src/app/home.ts"},
		},
		{
			name: "exact alias",
			imp:  "@env",
			aliases: map[string][]string{
				"@env": {"src/lib/env"},
			},
			want: []string{"src/lib/env.ts"},
		},
		{
			name: "fallback suffix alias",
			imp:  "@api",
			aliases: map[string][]string{
				"@api": {"lib/api"},
			},
			baseURL: "src",
			want:    []string{"src/lib/api/index.ts"},
		},
		{
			name: "unmatched alias",
			imp:  "@unknown/pkg",
			aliases: map[string][]string{
				"@app/*": {"src/app/*"},
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolvePathAlias(tt.imp, tt.aliases, tt.baseURL, idx); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("resolvePathAlias(%q) = %v, want %v", tt.imp, got, tt.want)
			}
		})
	}
}

func TestDetectLanguage(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "go file", path: "main.go", want: "go"},
		{name: "typescript file", path: "app.TS", want: "typescript"},
		{name: "unknown extension", path: "README.md", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectLanguage(tt.path); got != tt.want {
				t.Fatalf("DetectLanguage(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}
