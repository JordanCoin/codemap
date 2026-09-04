package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
)

func writeRustCargoFixture(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for path, content := range files {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func cargoTestManifest(name string) string {
	return "[package]\nname = \"" + name + "\"\nversion = \"0.1.0\"\n"
}

func cargoMetadataJSON(t *testing.T, root string, packages []map[string]any) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"packages":          packages,
		"workspace_members": []string{},
		"workspace_root":    root,
		"resolve":           nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func cargoPackage(root, rel, name, libName string, dependencies []map[string]any) map[string]any {
	return cargoPackageWithTargets(root, rel, name, []map[string]any{cargoTargetJSON(root, filepath.Join(rel, "src", "lib.rs"), libName, rustTargetLib)}, dependencies)
}

func cargoPackageWithTargets(root, rel, name string, targets, dependencies []map[string]any) map[string]any {
	manifest := filepath.Join(root, filepath.FromSlash(rel), "Cargo.toml")
	return map[string]any{
		"id":            "path+file://" + filepath.ToSlash(filepath.Dir(manifest)) + "#0.1.0",
		"name":          name,
		"manifest_path": manifest,
		"targets":       targets,
		"dependencies":  dependencies,
	}
}

func cargoTargetJSON(root, rel, name, kind string) map[string]any {
	return map[string]any{
		"name":     name,
		"kind":     []string{kind},
		"src_path": filepath.Join(root, filepath.FromSlash(rel)),
	}
}

type cargoMetadataResponse struct {
	data []byte
	err  error
}

func scriptedCargoMetadataLoader(responses map[string]cargoMetadataResponse, calls *[]string) cargoMetadataLoader {
	return func(_ context.Context, manifestPath string) ([]byte, error) {
		manifestPath = filepath.Clean(manifestPath)
		*calls = append(*calls, manifestPath)
		response, ok := responses[manifestPath]
		if !ok {
			return nil, errors.New("unexpected manifest: " + manifestPath)
		}
		return response.data, response.err
	}
}

func TestCargoMetadataArgsAreOfflineAndTopologyOnly(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "Cargo.toml")
	got := cargoMetadataArgs(manifestPath)
	want := []string{
		"metadata", "--no-deps", "--format-version", "1", "--offline",
		"--manifest-path", manifestPath,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cargo metadata args = %#v, want %#v", got, want)
	}
}

func TestCargoMetadataSharesOneScanDeadline(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"one/Cargo.toml": cargoTestManifest("one"),
		"one/src/lib.rs": "",
		"two/Cargo.toml": cargoTestManifest("two"),
		"two/src/lib.rs": "",
	})
	files := []FileInfo{{Path: "one/src/lib.rs"}, {Path: "two/src/lib.rs"}}
	var deadlines []time.Time
	buildRustWorkspaceIndex(context.Background(), root, nil, files, func(ctx context.Context, _ string) ([]byte, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("metadata loader context has no deadline")
		}
		deadlines = append(deadlines, deadline)
		return nil, errors.New("metadata unavailable")
	})
	if len(deadlines) != 2 || !deadlines[0].Equal(deadlines[1]) {
		t.Fatalf("metadata deadlines = %v, want one shared deadline", deadlines)
	}
}

func TestCargoMetadataDeadlinePreservesFallbackTopology(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml":       "[workspace]\nmembers = [\"one\", \"two\"]\n",
		"one/Cargo.toml":   cargoTestManifest("one"),
		"one/src/lib.rs":   "mod local;\n",
		"one/src/local.rs": "",
		"two/Cargo.toml":   cargoTestManifest("two"),
		"two/src/lib.rs":   "",
	})
	analyses := []FileAnalysis{
		{Path: "one/src/lib.rs", Language: "rust", Imports: []string{"local"}},
		{Path: "one/src/local.rs", Language: "rust"},
		{Path: "two/src/lib.rs", Language: "rust"},
	}
	files := []FileInfo{
		{Path: "one/src/lib.rs"},
		{Path: "one/src/local.rs"},
		{Path: "two/src/lib.rs"},
	}

	index, outcome, err := buildRustWorkspaceIndexWithTimeout(
		context.Background(), root, analyses, files,
		func(ctx context.Context, _ string) ([]byte, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
		time.Millisecond,
	)
	if err != nil {
		t.Fatalf("buildRustWorkspaceIndexWithTimeout() error: %v", err)
	}
	if outcome == nil || outcome.Status != ScanSourceFallback {
		t.Fatalf("metadata outcome = %#v, want fallback", outcome)
	}
	if pkg, ok := index.packageForFile("one/src/local.rs"); !ok || pkg.root != "one" {
		t.Fatalf("fallback package = %#v, ok %v, want one", pkg, ok)
	}
	if pkg, ok := index.packageForFile("two/src/lib.rs"); !ok || pkg.root != "two" {
		t.Fatalf("fallback package = %#v, ok %v, want two", pkg, ok)
	}
}

func TestCargoMetadataRecoversLocalCargoTopology(t *testing.T) {
	tests := []struct {
		name          string
		workspacePath string
		appPath       string
		corePath      string
		dependency    map[string]any
		coreName      string
		coreLibName   string
		callPath      string
		standalone    bool
	}{
		{
			name: "implicit workspace path member", workspacePath: "Cargo.toml", appPath: "app", corePath: "helper",
			dependency: map[string]any{"name": "helper", "rename": nil, "kind": nil}, coreName: "helper", coreLibName: "helper", callPath: "helper::api::run",
		},
		{
			name: "nested workspace", workspacePath: "rust/Cargo.toml", appPath: "rust/app", corePath: "rust/core",
			dependency: map[string]any{"name": "core-lib", "rename": nil, "kind": nil}, coreName: "core-lib", coreLibName: "core_lib", callPath: "core_lib::api::run",
		},
		{
			name: "renamed dependency", workspacePath: "Cargo.toml", appPath: "app", corePath: "core",
			dependency: map[string]any{"name": "core-lib", "rename": "facade", "kind": nil}, coreName: "core-lib", coreLibName: "core_lib", callPath: "facade::api::run",
		},
		{
			name: "custom library name", workspacePath: "Cargo.toml", appPath: "app", corePath: "core",
			dependency: map[string]any{"name": "core-package", "rename": nil, "kind": nil}, coreName: "core-package", coreLibName: "engine", callPath: "engine::api::run",
		},
		{
			name: "standalone local path dependency", workspacePath: "Cargo.toml", appPath: ".", corePath: "helper",
			dependency: map[string]any{"name": "helper", "rename": nil, "kind": nil}, coreName: "helper", coreLibName: "helper", callPath: "helper::api::run", standalone: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			appLib := filepath.ToSlash(filepath.Join(tt.appPath, "src", "lib.rs"))
			coreLib := filepath.ToSlash(filepath.Join(tt.corePath, "src", "lib.rs"))
			coreAPI := filepath.ToSlash(filepath.Join(tt.corePath, "src", "api.rs"))
			workspaceMember, err := filepath.Rel(filepath.Dir(tt.workspacePath), tt.appPath)
			if err != nil {
				t.Fatal(err)
			}
			workspaceFixtureManifest := "[workspace]\n"
			if !tt.standalone {
				workspaceFixtureManifest += "members = [\"" + filepath.ToSlash(workspaceMember) + "\"]\n"
			}
			files := map[string]string{
				tt.workspacePath: workspaceFixtureManifest,
				filepath.ToSlash(filepath.Join(tt.appPath, "Cargo.toml")): cargoTestManifest("app"),
				appLib: "pub fn call() {}\n",
				filepath.ToSlash(filepath.Join(tt.corePath, "Cargo.toml")): cargoTestManifest(tt.coreName),
				coreLib: "pub mod api;\n",
				coreAPI: "pub fn run() {}\n",
			}
			writeRustCargoFixture(t, root, files)

			dependency := map[string]any{}
			for key, value := range tt.dependency {
				dependency[key] = value
			}
			dependency["path"] = filepath.Join(root, filepath.FromSlash(tt.corePath))
			appPackage := cargoPackage(root, tt.appPath, "app", "app", []map[string]any{dependency})
			corePackage := cargoPackage(root, tt.corePath, tt.coreName, tt.coreLibName, nil)
			responses := map[string]cargoMetadataResponse{}
			workspaceManifest := filepath.Join(root, filepath.FromSlash(tt.workspacePath))
			if tt.standalone {
				responses[workspaceManifest] = cargoMetadataResponse{data: cargoMetadataJSON(t, root, []map[string]any{appPackage})}
				responses[filepath.Join(root, filepath.FromSlash(tt.corePath), "Cargo.toml")] = cargoMetadataResponse{data: cargoMetadataJSON(t, filepath.Join(root, filepath.FromSlash(tt.corePath)), []map[string]any{corePackage})}
			} else {
				responses[workspaceManifest] = cargoMetadataResponse{data: cargoMetadataJSON(t, filepath.Dir(workspaceManifest), []map[string]any{appPackage, corePackage})}
			}
			var calls []string
			analyses := []FileAnalysis{
				{Path: appLib, Language: "rust", References: []ImportReference{{Path: tt.callPath, Kind: "rust-path"}}},
				{Path: coreLib, Language: "rust", References: []ImportReference{{Path: "api", Kind: "rust-module"}}},
			}
			graph, err := buildFileGraphFromAnalysesWithCargoMetadata(context.Background(), root, analyses, scriptedCargoMetadataLoader(responses, &calls))
			if err != nil {
				t.Fatal(err)
			}
			if got, want := graph.Imports[appLib], []string{coreAPI}; !reflect.DeepEqual(got, want) {
				t.Fatalf("imports = %#v, want %#v; metadata calls = %#v", got, want, calls)
			}
			wantCalls := 1
			if tt.standalone {
				wantCalls = 2
			}
			if len(calls) != wantCalls {
				t.Fatalf("metadata calls = %#v, want %d", calls, wantCalls)
			}
		})
	}
}

func TestCargoMetadataScopesDependenciesByCallerAndTargetKind(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"Cargo.toml":               "[workspace]\nmembers = [\"app\"]\n",
		"app/Cargo.toml":           cargoTestManifest("app"),
		"app/src/lib.rs":           "#[cfg(test)] mod unit_tests;\npub fn call() {}\n",
		"app/src/main.rs":          "fn main() {}\n",
		"app/src/unit_tests.rs":    "#[test] fn unit() {}\n",
		"app/build.rs":             "fn main() {}\n",
		"app/tests/integration.rs": "fn test() {}\n",
		"app/examples/demo.rs":     "fn main() {}\n",
		"app/benches/measure.rs":   "fn main() {}\n",
		"normal/Cargo.toml":        "[package]\nname = \"normal\"\nversion = \"0.1.0\"\n",
		"normal/src/lib.rs":        "pub mod api;\n",
		"normal/src/api.rs":        "pub fn run() {}\n",
		"build-dep/Cargo.toml":     "[package]\nname = \"build-dep\"\nversion = \"0.1.0\"\n",
		"build-dep/src/lib.rs":     "pub mod api;\n",
		"build-dep/src/api.rs":     "pub fn run() {}\n",
		"dev-dep/Cargo.toml":       "[package]\nname = \"dev-dep\"\nversion = \"0.1.0\"\n",
		"dev-dep/src/lib.rs":       "pub mod api;\n",
		"dev-dep/src/api.rs":       "pub fn run() {}\n",
	}
	writeRustCargoFixture(t, root, files)
	dependencies := []map[string]any{
		{"name": "normal", "rename": nil, "path": filepath.Join(root, "normal"), "kind": nil},
		{"name": "build-dep", "rename": "build_dep", "path": filepath.Join(root, "build-dep"), "kind": "build"},
		{"name": "dev-dep", "rename": "dev_dep", "path": filepath.Join(root, "dev-dep"), "kind": "dev"},
	}
	packages := []map[string]any{
		cargoPackageWithTargets(root, "app", "app", []map[string]any{
			cargoTargetJSON(root, "app/src/lib.rs", "app", rustTargetLib),
			cargoTargetJSON(root, "app/src/main.rs", "app-bin", rustTargetBin),
			cargoTargetJSON(root, "app/build.rs", "build-script-build", rustTargetCustomBuild),
			cargoTargetJSON(root, "app/tests/integration.rs", "integration", rustTargetTest),
			cargoTargetJSON(root, "app/examples/demo.rs", "demo", rustTargetExample),
			cargoTargetJSON(root, "app/benches/measure.rs", "measure", rustTargetBench),
		}, dependencies),
		cargoPackage(root, "normal", "normal", "normal", nil),
		cargoPackage(root, "build-dep", "build-dep", "build_dep", nil),
		cargoPackage(root, "dev-dep", "dev-dep", "dev_dep", nil),
	}
	metadata := cargoMetadataJSON(t, root, packages)
	refs := []ImportReference{
		{Path: "normal::api::run", Kind: "rust-path"},
		{Path: "build_dep::api::run", Kind: "rust-path"},
		{Path: "dev_dep::api::run", Kind: "rust-path"},
	}
	analyses := []FileAnalysis{
		{Path: "app/src/lib.rs", Language: "rust", References: append([]ImportReference{{Path: "unit_tests", Kind: "rust-module"}}, refs...)},
		{Path: "app/src/main.rs", Language: "rust", References: refs},
		{Path: "app/src/unit_tests.rs", Language: "rust", References: refs},
		{Path: "app/build.rs", Language: "rust", References: refs},
		{Path: "app/tests/integration.rs", Language: "rust", References: refs},
		{Path: "app/examples/demo.rs", Language: "rust", References: refs},
		{Path: "app/benches/measure.rs", Language: "rust", References: refs},
	}
	graph, err := buildFileGraphFromAnalysesWithCargoMetadata(context.Background(), root, analyses, func(context.Context, string) ([]byte, error) { return metadata, nil })
	if err != nil {
		t.Fatal(err)
	}
	for from, want := range map[string][]string{
		"app/src/lib.rs":           {"app/src/unit_tests.rs", "dev-dep/src/api.rs", "normal/src/api.rs"},
		"app/src/main.rs":          {"dev-dep/src/api.rs", "normal/src/api.rs"},
		"app/src/unit_tests.rs":    {"dev-dep/src/api.rs", "normal/src/api.rs"},
		"app/build.rs":             {"build-dep/src/api.rs"},
		"app/tests/integration.rs": {"dev-dep/src/api.rs", "normal/src/api.rs"},
		"app/examples/demo.rs":     {"dev-dep/src/api.rs", "normal/src/api.rs"},
		"app/benches/measure.rs":   {"dev-dep/src/api.rs", "normal/src/api.rs"},
	} {
		got := append([]string(nil), graph.Imports[from]...)
		sort.Strings(got)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s imports = %#v, want %#v", from, got, want)
		}
	}
}

func TestCargoMetadataRejectsUndeclaredWorkspaceCrateCollision(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml":              "[workspace]\nmembers = [\"app\", \"helper-crate\"]\n",
		"app/Cargo.toml":          cargoTestManifest("app"),
		"app/src/lib.rs":          "mod helper;\n",
		"app/src/helper.rs":       "pub mod api;\n",
		"app/src/helper/api.rs":   "pub fn run() {}\n",
		"helper-crate/Cargo.toml": "[package]\nname = \"helper\"\nversion = \"0.1.0\"\n",
		"helper-crate/src/lib.rs": "pub mod api;\n",
		"helper-crate/src/api.rs": "pub fn run() {}\n",
	})
	metadata := cargoMetadataJSON(t, root, []map[string]any{
		cargoPackage(root, "app", "app", "app", nil),
		cargoPackage(root, "helper-crate", "helper", "helper", nil),
	})
	analyses := []FileAnalysis{
		{Path: "app/src/lib.rs", Language: "rust", References: []ImportReference{{Path: "helper", Kind: "rust-module"}, {Path: "helper::api::run", Kind: "rust-path"}}},
		{Path: "app/src/helper.rs", Language: "rust", References: []ImportReference{{Path: "api", Kind: "rust-module"}}},
		{Path: "helper-crate/src/lib.rs", Language: "rust", References: []ImportReference{{Path: "api", Kind: "rust-module"}}},
	}
	graph, err := buildFileGraphFromAnalysesWithCargoMetadata(context.Background(), root, analyses, func(context.Context, string) ([]byte, error) { return metadata, nil })
	if err != nil {
		t.Fatal(err)
	}
	if got, want := graph.Imports["app/src/lib.rs"], []string{"app/src/helper.rs", "app/src/helper/api.rs"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("app imports = %#v, want only local module %#v", got, want)
	}
	if got, want := graph.Importers["helper-crate/src/api.rs"], []string{"helper-crate/src/lib.rs"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unrelated crate importers = %#v, want %#v", got, want)
	}
}

func TestCargoMetadataPrefersDeclaredModuleOverDependency(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml":              "[workspace]\nmembers = [\"app\", \"helper-crate\"]\n",
		"app/Cargo.toml":          cargoTestManifest("app"),
		"app/src/lib.rs":          "mod helper;\npub fn call() { helper::api::run(); }\n",
		"app/src/helper.rs":       "pub mod api;\n",
		"app/src/helper/api.rs":   "pub fn run() {}\n",
		"helper-crate/Cargo.toml": cargoTestManifest("helper"),
		"helper-crate/src/lib.rs": "pub mod api;\n",
		"helper-crate/src/api.rs": "pub fn run() {}\n",
	})
	metadata := cargoMetadataJSON(t, root, []map[string]any{
		cargoPackage(root, "app", "app", "app", []map[string]any{{
			"name": "helper", "path": filepath.Join(root, "helper-crate"), "kind": nil,
		}}),
		cargoPackage(root, "helper-crate", "helper", "helper", nil),
	})
	analyses := []FileAnalysis{
		{Path: "app/src/lib.rs", Language: "rust", References: []ImportReference{{Path: "helper", Kind: "rust-module"}, {Path: "helper::api::run", Kind: "rust-path"}}},
		{Path: "app/src/helper.rs", Language: "rust", References: []ImportReference{{Path: "api", Kind: "rust-module"}}},
		{Path: "helper-crate/src/lib.rs", Language: "rust", References: []ImportReference{{Path: "api", Kind: "rust-module"}}},
	}
	graph, err := buildFileGraphFromAnalysesWithCargoMetadata(context.Background(), root, analyses, func(context.Context, string) ([]byte, error) { return metadata, nil })
	if err != nil {
		t.Fatal(err)
	}
	if got, want := graph.Imports["app/src/lib.rs"], []string{"app/src/helper.rs", "app/src/helper/api.rs"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("app imports = %#v, want local module only %#v", got, want)
	}
}

func TestCargoMetadataRejectsModulesFromUnownedSourceFiles(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml":    "[package]\nname = \"app\"\nversion = \"0.1.0\"\nautolib = false\n",
		"src/lib.rs":    "mod common;\n",
		"src/common.rs": "pub fn run() {}\n",
		"tools/app.rs":  "fn main() {}\n",
	})
	metadata := cargoMetadataJSON(t, root, []map[string]any{
		cargoPackageWithTargets(root, ".", "app", []map[string]any{
			cargoTargetJSON(root, "tools/app.rs", "app", rustTargetBin),
		}, nil),
	})
	analyses := []FileAnalysis{{
		Path: "src/lib.rs", Language: "rust",
		References: []ImportReference{{Path: "common", Kind: "rust-module"}},
	}}
	graph, err := buildFileGraphFromAnalysesWithCargoMetadata(context.Background(), root, analyses, func(context.Context, string) ([]byte, error) {
		return metadata, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := graph.Imports["src/lib.rs"]; len(got) != 0 {
		t.Fatalf("unowned source imports = %#v, want none", got)
	}
}

func TestCargoMetadataRejectsAmbiguousDependencyAlias(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml":        "[workspace]\nmembers = [\"app\", \"first\", \"second\"]\n",
		"app/Cargo.toml":    cargoTestManifest("app"),
		"app/src/lib.rs":    "pub fn call() {}\n",
		"first/Cargo.toml":  "[package]\nname = \"first\"\nversion = \"0.1.0\"\n",
		"first/src/lib.rs":  "pub mod api;\n",
		"first/src/api.rs":  "pub fn run() {}\n",
		"second/Cargo.toml": "[package]\nname = \"second\"\nversion = \"0.1.0\"\n",
		"second/src/lib.rs": "pub mod api;\n",
		"second/src/api.rs": "pub fn run() {}\n",
	})
	metadata := cargoMetadataJSON(t, root, []map[string]any{
		// Cargo rejects duplicate alias sources, so the second declaration models malformed or untrusted metadata.
		cargoPackage(root, "app", "app", "app", []map[string]any{
			{"name": "first", "rename": "shared", "path": filepath.Join(root, "first"), "kind": nil},
			{"name": "second", "rename": "shared", "path": filepath.Join(root, "second"), "kind": nil},
		}),
		cargoPackage(root, "first", "first", "first", nil),
		cargoPackage(root, "second", "second", "second", nil),
	})
	analyses := []FileAnalysis{
		{Path: "app/src/lib.rs", Language: "rust", References: []ImportReference{{Path: "shared::api::run", Kind: "rust-path"}}},
		{Path: "first/src/lib.rs", Language: "rust", References: []ImportReference{{Path: "api", Kind: "rust-module"}}},
		{Path: "second/src/lib.rs", Language: "rust", References: []ImportReference{{Path: "api", Kind: "rust-module"}}},
	}
	graph, err := buildFileGraphFromAnalysesWithCargoMetadata(context.Background(), root, analyses, func(context.Context, string) ([]byte, error) { return metadata, nil })
	if err != nil {
		t.Fatal(err)
	}
	if got := graph.Imports["app/src/lib.rs"]; len(got) != 0 {
		t.Fatalf("ambiguous alias imports = %#v, want none", got)
	}
}

func TestCargoMetadataKeepsSuccessfulRootsWhenAnotherRootFallsBack(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml":               "[package]\nname = \"fallback\"\nversion = \"0.1.0\"\n",
		"src/lib.rs":               "mod helper;\n",
		"src/helper.rs":            "pub fn run() {}\n",
		"metadata/Cargo.toml":      "[workspace]\nmembers = [\"app\"]\n",
		"metadata/app/Cargo.toml":  cargoTestManifest("app"),
		"metadata/app/src/lib.rs":  "pub fn call() {}\n",
		"metadata/core/Cargo.toml": "[package]\nname = \"core-package\"\nversion = \"0.1.0\"\n",
		"metadata/core/src/lib.rs": "pub mod api;\n",
		"metadata/core/src/api.rs": "pub fn run() {}\n",
	})
	metadataRoot := filepath.Join(root, "metadata")
	metadata := cargoMetadataJSON(t, metadataRoot, []map[string]any{
		cargoPackage(root, "metadata/app", "app", "app", []map[string]any{{
			"name": "core-package", "rename": nil, "path": filepath.Join(metadataRoot, "core"), "kind": nil,
		}}),
		cargoPackage(root, "metadata/core", "core-package", "engine", nil),
	})
	responses := map[string]cargoMetadataResponse{
		filepath.Join(metadataRoot, "Cargo.toml"): {data: metadata},
		filepath.Join(root, "Cargo.toml"):         {err: errors.New("cargo unavailable")},
	}
	var calls []string
	analyses := []FileAnalysis{
		{Path: "metadata/app/src/lib.rs", Language: "rust", References: []ImportReference{{Path: "engine::api::run", Kind: "rust-path"}}},
		{Path: "metadata/core/src/lib.rs", Language: "rust", References: []ImportReference{{Path: "api", Kind: "rust-module"}}},
		{Path: "src/lib.rs", Language: "rust", References: []ImportReference{{Path: "helper", Kind: "rust-module"}}},
	}
	graph, err := buildFileGraphFromAnalysesWithCargoMetadata(context.Background(), root, analyses, scriptedCargoMetadataLoader(responses, &calls))
	if err != nil {
		t.Fatal(err)
	}
	for from, want := range map[string][]string{
		"metadata/app/src/lib.rs": {"metadata/core/src/api.rs"},
		"src/lib.rs":              {"src/helper.rs"},
	} {
		if got := graph.Imports[from]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s imports = %#v, want %#v", from, got, want)
		}
	}
	if len(calls) != 2 {
		t.Fatalf("metadata calls = %#v, want one per independent root", calls)
	}
}

func TestCargoMetadataFailuresPreserveManualFallback(t *testing.T) {
	for _, tt := range []struct {
		name string
		data []byte
		err  error
	}{
		{name: "cargo unavailable", err: errors.New("cargo not found")},
		{name: "metadata timeout", err: context.DeadlineExceeded},
		{name: "malformed metadata", data: []byte("{")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeRustCargoFixture(t, root, map[string]string{
				"Cargo.toml": "[package]\nname = \"fallback\"\nversion = \"0.1.0\"\n",
				"src/lib.rs": "mod api;\n",
				"src/api.rs": "pub fn run() {}\n",
			})
			analyses := []FileAnalysis{{Path: "src/lib.rs", Language: "rust", References: []ImportReference{{Path: "api", Kind: "rust-module"}}}}
			graph, err := buildFileGraphFromAnalysesWithCargoMetadata(context.Background(), root, analyses, func(context.Context, string) ([]byte, error) { return tt.data, tt.err })
			if err != nil {
				t.Fatal(err)
			}
			if got, want := graph.Imports["src/lib.rs"], []string{"src/api.rs"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("fallback imports = %#v, want %#v", got, want)
			}
		})
	}
}

func TestCargoMetadataFailurePreservesLegacyBinaryRoot(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml":    "[package]\nname = \"app\"\nversion = \"0.1.0\"\n",
		"src/main.rs":   "mod helper;\n",
		"src/helper.rs": "pub fn run() {}\n",
	})
	analyses := []FileAnalysis{{
		Path: "src/main.rs", Language: "rust",
		References: []ImportReference{{Path: "helper", Kind: "rust-module"}},
	}}
	graph, err := buildFileGraphFromAnalysesWithCargoMetadata(context.Background(), root, analyses, func(context.Context, string) ([]byte, error) {
		return nil, errors.New("cargo unavailable")
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := graph.Imports["src/main.rs"], []string{"src/helper.rs"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fallback binary imports = %#v, want %#v", got, want)
	}
}

func TestCargoMetadataFailurePreservesLegacySelfAndSuperPaths(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml":          "[package]\nname = \"app\"\nversion = \"0.1.0\"\n",
		"src/main.rs":         "fn main() {}\n",
		"src/helper.rs":       "pub fn run() {}\n",
		"src/branch.rs":       "pub fn run() {}\n",
		"src/branch/child.rs": "pub fn run() {}\n",
	})
	analyses := []FileAnalysis{
		{Path: "src/branch.rs", Language: "rust", References: []ImportReference{{Path: "self::child::run", Kind: "rust-path"}}},
		{Path: "src/branch/child.rs", Language: "rust", References: []ImportReference{{Path: "super::super::helper::run", Kind: "rust-path"}}},
		{Path: "src/helper.rs", Language: "rust"},
	}
	graph, err := buildFileGraphFromAnalysesWithCargoMetadata(context.Background(), root, analyses, func(context.Context, string) ([]byte, error) {
		return nil, errors.New("cargo unavailable")
	})
	if err != nil {
		t.Fatal(err)
	}
	for from, want := range map[string][]string{
		"src/branch.rs":       {"src/branch/child.rs"},
		"src/branch/child.rs": {"src/helper.rs"},
	} {
		if got := graph.Imports[from]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s fallback imports = %#v, want %#v", from, got, want)
		}
	}
}

func TestCargoMetadataHonorsCallerCancellation(t *testing.T) {
	root := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml": "[package]\nname = \"app\"\nversion = \"0.1.0\"\n",
		"src/lib.rs": "pub fn call() {}\n",
	})
	ctx, cancel := context.WithCancel(context.Background())
	graph, err := buildFileGraphFromAnalysesWithCargoMetadata(ctx, root, []FileAnalysis{{
		Path: "src/lib.rs", Language: "rust",
	}}, func(ctx context.Context, _ string) ([]byte, error) {
		cancel()
		return nil, ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if graph != nil {
		t.Fatalf("graph = %#v, want nil after cancellation", graph)
	}
}

func TestCargoMetadataRejectsPathsOutsideProject(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeRustCargoFixture(t, root, map[string]string{
		"Cargo.toml": "[package]\nname = \"app\"\nversion = \"0.1.0\"\n",
		"src/lib.rs": "pub fn call() {}\n",
	})
	metadata := cargoMetadataJSON(t, root, []map[string]any{
		cargoPackage(root, ".", "app", "app", []map[string]any{{"name": "outside", "rename": nil, "path": outside, "kind": nil}}),
		cargoPackage(outside, ".", "outside", "outside", nil),
	})
	analyses := []FileAnalysis{{Path: "src/lib.rs", Language: "rust", References: []ImportReference{{Path: "outside::api::run", Kind: "rust-path"}}}}
	graph, err := buildFileGraphFromAnalysesWithCargoMetadata(context.Background(), root, analyses, func(context.Context, string) ([]byte, error) { return metadata, nil })
	if err != nil {
		t.Fatal(err)
	}
	if got := graph.Imports["src/lib.rs"]; len(got) != 0 {
		t.Fatalf("outside-root imports = %#v, want none", got)
	}
}
