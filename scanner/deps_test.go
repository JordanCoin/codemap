package scanner

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestParseGoMod(t *testing.T) {
	gomod := `module example.com/myapp

go 1.21

require (
	github.com/foo/bar v1.0.0
	github.com/baz/qux v2.0.0
	// This is a comment
	golang.org/x/text v0.3.0
)

require github.com/indirect/dep v1.0.0 // indirect
`

	deps := parseGoMod(gomod)

	expected := []string{
		"github.com/foo/bar",
		"github.com/baz/qux",
		"golang.org/x/text",
	}

	if len(deps) != len(expected) {
		t.Errorf("Expected %d deps, got %d: %v", len(expected), len(deps), deps)
	}

	for i, exp := range expected {
		if i < len(deps) && deps[i] != exp {
			t.Errorf("Dep %d: expected %q, got %q", i, exp, deps[i])
		}
	}
}

func TestParseGoModEmpty(t *testing.T) {
	gomod := `module example.com/myapp

go 1.21
`
	deps := parseGoMod(gomod)
	if len(deps) != 0 {
		t.Errorf("Expected no deps, got %v", deps)
	}
}

func TestParseRequirements(t *testing.T) {
	requirements := `# Python dependencies
flask==2.0.0
requests>=2.25.0
numpy~=1.21.0
pandas
scikit-learn[extra]
pytest<7.0.0

# Comment line
django>3.0,<4.0
`

	deps := parseRequirements(requirements)

	expected := []string{
		"flask",
		"requests",
		"numpy",
		"pandas",
		"scikit-learn",
		"pytest",
		"django",
	}

	if len(deps) != len(expected) {
		t.Errorf("Expected %d deps, got %d: %v", len(expected), len(deps), deps)
	}

	for i, exp := range expected {
		if i < len(deps) && deps[i] != exp {
			t.Errorf("Dep %d: expected %q, got %q", i, exp, deps[i])
		}
	}
}

func TestParseRequirementsEmpty(t *testing.T) {
	requirements := `# Just comments
# No actual deps
`
	deps := parseRequirements(requirements)
	if len(deps) != 0 {
		t.Errorf("Expected no deps, got %v", deps)
	}
}

func TestParsePackageJson(t *testing.T) {
	packageJson := `{
  "name": "my-app",
  "version": "1.0.0",
  "dependencies": {
    "react": "^18.0.0",
    "react-dom": "^18.0.0",
    "axios": "^1.0.0"
  },
  "devDependencies": {
    "typescript": "^5.0.0",
    "jest": "^29.0.0"
  }
}`

	deps := parsePackageJson(packageJson)

	expected := []string{"react", "react-dom", "axios", "typescript", "jest"}

	if len(deps) != len(expected) {
		t.Errorf("Expected %d deps, got %d: %v", len(expected), len(deps), deps)
	}

	// Check all expected deps are present (order may vary)
	depsMap := make(map[string]bool)
	for _, d := range deps {
		depsMap[d] = true
	}

	for _, exp := range expected {
		if !depsMap[exp] {
			t.Errorf("Expected dep %q not found in %v", exp, deps)
		}
	}
}

func TestParsePackageJsonEmpty(t *testing.T) {
	packageJson := `{
  "name": "my-app",
  "version": "1.0.0"
}`
	deps := parsePackageJson(packageJson)
	if len(deps) != 0 {
		t.Errorf("Expected no deps, got %v", deps)
	}
}

func TestParsePodfile(t *testing.T) {
	podfile := `platform :ios, '14.0'

target 'MyApp' do
  use_frameworks!

  pod 'Alamofire', '~> 5.0'
  pod 'SwiftyJSON'
  pod "Kingfisher", "~> 7.0"
  pod 'SnapKit', :git => 'https://github.com/SnapKit/SnapKit.git'

end
`

	deps := parsePodfile(podfile)

	expected := []string{"Alamofire", "SwiftyJSON", "Kingfisher", "SnapKit"}

	if len(deps) != len(expected) {
		t.Errorf("Expected %d deps, got %d: %v", len(expected), len(deps), deps)
	}

	depsMap := make(map[string]bool)
	for _, d := range deps {
		depsMap[d] = true
	}

	for _, exp := range expected {
		if !depsMap[exp] {
			t.Errorf("Expected dep %q not found in %v", exp, deps)
		}
	}
}

func TestParsePackageSwift(t *testing.T) {
	packageSwift := `// swift-tools-version:5.5
import PackageDescription

let package = Package(
    name: "MyPackage",
    dependencies: [
        .package(url: "https://github.com/apple/swift-argument-parser", from: "1.0.0"),
        .package(url: "https://github.com/vapor/vapor.git", from: "4.0.0"),
    ],
    targets: [
        .target(name: "MyTarget", dependencies: ["ArgumentParser", "Vapor"]),
    ]
)
`

	deps := parsePackageSwift(packageSwift)

	expected := []string{"swift-argument-parser", "vapor"}

	if len(deps) != len(expected) {
		t.Errorf("Expected %d deps, got %d: %v", len(expected), len(deps), deps)
	}

	depsMap := make(map[string]bool)
	for _, d := range deps {
		depsMap[d] = true
	}

	for _, exp := range expected {
		if !depsMap[exp] {
			t.Errorf("Expected dep %q not found in %v", exp, deps)
		}
	}
}

func TestReadExternalDeps(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a go.mod file
	gomod := `module example.com/test

go 1.21

require (
	github.com/test/dep v1.0.0
)
`
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(gomod), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a requirements.txt
	requirements := `flask==2.0.0
requests
`
	if err := os.WriteFile(filepath.Join(tmpDir, "requirements.txt"), []byte(requirements), 0644); err != nil {
		t.Fatal(err)
	}

	deps := ReadExternalDeps(tmpDir)

	// Check Go deps
	if goDeps, ok := deps["go"]; !ok {
		t.Error("Expected go deps")
	} else if len(goDeps) != 1 || goDeps[0] != "github.com/test/dep" {
		t.Errorf("Unexpected go deps: %v", goDeps)
	}

	// Check Python deps
	if pyDeps, ok := deps["python"]; !ok {
		t.Error("Expected python deps")
	} else {
		sort.Strings(pyDeps)
		expected := []string{"flask", "requests"}
		sort.Strings(expected)
		if !reflect.DeepEqual(pyDeps, expected) {
			t.Errorf("Expected python deps %v, got %v", expected, pyDeps)
		}
	}
}

func TestReadExternalDepsIgnoresNodeModules(t *testing.T) {
	tmpDir := t.TempDir()

	// Create package.json in node_modules (should be ignored)
	nodeModules := filepath.Join(tmpDir, "node_modules", "some-pkg")
	if err := os.MkdirAll(nodeModules, 0755); err != nil {
		t.Fatal(err)
	}
	ignoredPackageJson := `{
  "dependencies": {
    "ignored": "1.0.0"
  }
}`
	if err := os.WriteFile(filepath.Join(nodeModules, "package.json"), []byte(ignoredPackageJson), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a real package.json at root (multi-line for parser compatibility)
	rootPackageJson := `{
  "dependencies": {
    "real-dep": "1.0.0"
  }
}`
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(rootPackageJson), 0644); err != nil {
		t.Fatal(err)
	}

	deps := ReadExternalDeps(tmpDir)

	// Should only have the root package.json deps
	if jsDeps, ok := deps["javascript"]; ok {
		for _, d := range jsDeps {
			if d == "ignored" {
				t.Error("node_modules/package.json should be ignored")
			}
		}
		found := false
		for _, d := range jsDeps {
			if d == "real-dep" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected real-dep from root package.json, got: %v", jsDeps)
		}
	} else {
		t.Errorf("Expected javascript deps, got: %v", deps)
	}
}

func TestDetectPathAliases(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a tsconfig.json with path aliases
	tsconfig := `{
  "compilerOptions": {
    "baseUrl": ".",
    "paths": {
      "@modules/*": ["src/modules/*"],
      "@shared/*": ["src/shared/*"],
      "@utils": ["src/utils/index"]
    }
  }
}`
	if err := os.WriteFile(filepath.Join(tmpDir, "tsconfig.json"), []byte(tsconfig), 0644); err != nil {
		t.Fatal(err)
	}

	paths, baseURL := detectPathAliases(tmpDir)

	if baseURL != "." {
		t.Errorf("Expected baseUrl '.', got %q", baseURL)
	}

	if len(paths) != 3 {
		t.Errorf("Expected 3 path aliases, got %d: %v", len(paths), paths)
	}

	if targets, ok := paths["@modules/*"]; !ok {
		t.Error("Expected @modules/* alias")
	} else if len(targets) != 1 || targets[0] != "src/modules/*" {
		t.Errorf("Expected @modules/* -> src/modules/*, got %v", targets)
	}

	if targets, ok := paths["@shared/*"]; !ok {
		t.Error("Expected @shared/* alias")
	} else if len(targets) != 1 || targets[0] != "src/shared/*" {
		t.Errorf("Expected @shared/* -> src/shared/*, got %v", targets)
	}
}

func TestDetectPathAliasesWithExtends(t *testing.T) {
	tmpDir := t.TempDir()

	// Create base tsconfig
	baseConfig := `{
  "compilerOptions": {
    "baseUrl": ".",
    "paths": {
      "@base/*": ["src/base/*"]
    }
  }
}`
	if err := os.WriteFile(filepath.Join(tmpDir, "tsconfig.base.json"), []byte(baseConfig), 0644); err != nil {
		t.Fatal(err)
	}

	// Create extending tsconfig
	tsconfig := `{
  "extends": "./tsconfig.base.json",
  "compilerOptions": {
    "paths": {
      "@app/*": ["src/app/*"]
    }
  }
}`
	if err := os.WriteFile(filepath.Join(tmpDir, "tsconfig.json"), []byte(tsconfig), 0644); err != nil {
		t.Fatal(err)
	}

	paths, baseURL := detectPathAliases(tmpDir)

	if baseURL != "." {
		t.Errorf("Expected baseUrl '.', got %q", baseURL)
	}

	// Should have both parent and child paths
	if len(paths) != 2 {
		t.Errorf("Expected 2 path aliases (merged), got %d: %v", len(paths), paths)
	}

	if _, ok := paths["@app/*"]; !ok {
		t.Error("Expected @app/* alias from child config")
	}

	if _, ok := paths["@base/*"]; !ok {
		t.Error("Expected @base/* alias from parent config")
	}
}

func TestDetectPathAliasesJsconfig(t *testing.T) {
	tmpDir := t.TempDir()

	// Create jsconfig.json (used in JavaScript projects without TypeScript)
	jsconfig := `{
  "compilerOptions": {
    "baseUrl": "src",
    "paths": {
      "@/*": ["./*"]
    }
  }
}`
	if err := os.WriteFile(filepath.Join(tmpDir, "jsconfig.json"), []byte(jsconfig), 0644); err != nil {
		t.Fatal(err)
	}

	paths, baseURL := detectPathAliases(tmpDir)

	if baseURL != "src" {
		t.Errorf("Expected baseUrl 'src', got %q", baseURL)
	}

	if len(paths) != 1 {
		t.Errorf("Expected 1 path alias, got %d: %v", len(paths), paths)
	}
}

func TestResolvePathAlias(t *testing.T) {
	// Build a simple file index
	files := []FileInfo{
		{Path: "src/modules/auth/index.ts"},
		{Path: "src/modules/auth/login.ts"},
		{Path: "src/shared/utils/helpers.ts"},
		{Path: "src/utils/index.ts"},
	}
	idx := buildFileIndex(files, "")

	pathAliases := map[string][]string{
		"@modules/*": {"src/modules/*"},
		"@shared/*":  {"src/shared/*"},
		"@utils":     {"src/utils/index"},
	}

	tests := []struct {
		name     string
		imp      string
		expected string
	}{
		{"wildcard alias", "@modules/auth/login", "src/modules/auth/login.ts"},
		{"wildcard with index", "@modules/auth", "src/modules/auth/index.ts"},
		{"nested wildcard", "@shared/utils/helpers", "src/shared/utils/helpers.ts"},
		{"exact alias", "@utils", "src/utils/index.ts"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolvePathAlias(tt.imp, pathAliases, ".", idx)
			if len(result) == 0 {
				t.Errorf("Expected to resolve %q, got no results", tt.imp)
				return
			}
			if result[0] != tt.expected {
				t.Errorf("Expected %q to resolve to %q, got %q", tt.imp, tt.expected, result[0])
			}
		})
	}
}

func TestResolvePathAliasNoMatch(t *testing.T) {
	files := []FileInfo{
		{Path: "src/modules/auth/index.ts"},
	}
	idx := buildFileIndex(files, "")

	pathAliases := map[string][]string{
		"@modules/*": {"src/modules/*"},
	}

	// Import that doesn't match any alias
	result := resolvePathAlias("lodash", pathAliases, ".", idx)
	if len(result) != 0 {
		t.Errorf("Expected no results for non-alias import, got %v", result)
	}

	// Import that matches alias but file doesn't exist
	result = resolvePathAlias("@modules/nonexistent", pathAliases, ".", idx)
	if len(result) != 0 {
		t.Errorf("Expected no results for non-existent file, got %v", result)
	}
}
