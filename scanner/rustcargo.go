package scanner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const cargoMetadataTimeout = 3 * time.Second

type cargoMetadataLoader func(context.Context, string) ([]byte, error)

type cargoMetadata struct {
	Packages []cargoMetadataPackage `json:"packages"`
}

type cargoMetadataPackage struct {
	Name         string                    `json:"name"`
	ManifestPath string                    `json:"manifest_path"`
	Targets      []cargoMetadataTarget     `json:"targets"`
	Dependencies []cargoMetadataDependency `json:"dependencies"`
}

type cargoMetadataTarget struct {
	Name    string   `json:"name"`
	Kind    []string `json:"kind"`
	SrcPath string   `json:"src_path"`
}

type cargoMetadataDependency struct {
	Name     string          `json:"name"`
	Rename   string          `json:"rename"`
	Path     string          `json:"path"`
	Kind     string          `json:"kind"`
	Optional bool            `json:"optional"`
	Target   json.RawMessage `json:"target"`
}

type pendingRustDependency struct {
	packageRoot string
	rename      string
	kind        string
}

func loadCargoMetadata(ctx context.Context, manifestPath string) ([]byte, error) {
	return runCargoMetadata(ctx, manifestPath, cargoMetadataArgs(manifestPath))
}

func loadCargoFallbackMetadata(ctx context.Context, manifestPath string) ([]byte, error) {
	return runCargoMetadata(ctx, manifestPath, cargoFallbackMetadataArgs(manifestPath))
}

func runCargoMetadata(ctx context.Context, manifestPath string, args []string) ([]byte, error) {
	cargo, err := exec.LookPath("cargo")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, cargoMetadataTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, cargo, args...)
	cmd.Dir = filepath.Dir(manifestPath)
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return output, err
}

func cargoMetadataArgs(manifestPath string) []string {
	return []string{
		"metadata", "--no-deps", "--format-version", "1", "--offline",
		"--manifest-path", manifestPath,
	}
}

func cargoFallbackMetadataArgs(manifestPath string) []string {
	return append(cargoMetadataArgs(manifestPath), "--locked")
}

func parseCargoMetadata(data []byte) (cargoMetadata, error) {
	var metadata cargoMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return metadata, err
	}
	for packageIndex := range metadata.Packages {
		for dependencyIndex := range metadata.Packages[packageIndex].Dependencies {
			dependency := &metadata.Packages[packageIndex].Dependencies[dependencyIndex]
			if strings.TrimSpace(string(dependency.Target)) == "null" {
				dependency.Target = nil
			}
		}
	}
	return metadata, nil
}

func buildRustWorkspaceIndex(ctx context.Context, root string, analyses []FileAnalysis, files []FileInfo, loader cargoMetadataLoader) (*rustWorkspaceIndex, *ScanSourceOutcome, error) {
	index := buildRustFallbackWorkspaceIndex(root, analyses)
	manifestPaths, err := discoverCargoManifests(ctx, root, files)
	if err != nil {
		return nil, nil, err
	}
	if len(manifestPaths) == 0 {
		return index, nil, nil
	}
	if loader == nil {
		if err := index.buildRustModuleLocations(ctx, root, files); err != nil {
			return nil, nil, err
		}
		outcome := cargoMetadataOutcome(0, len(manifestPaths))
		return index, &outcome, nil
	}
	ctx, cancel := context.WithTimeout(ctx, cargoMetadataTimeout)
	defer cancel()

	packagesByRoot := make(map[string]rustPackage, len(index.packages))
	for _, pkg := range index.packages {
		packagesByRoot[pkg.root] = pkg
	}
	pendingByRoot := make(map[string][]pendingRustDependency)
	handledManifests := make(map[string]bool)

	for _, manifestPath := range manifestPaths {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		manifestPath = filepath.Clean(manifestPath)
		if handledManifests[manifestPath] {
			continue
		}
		data, err := loader(ctx, manifestPath)
		if err != nil {
			continue
		}
		metadata, err := parseCargoMetadata(data)
		if err != nil {
			continue
		}

		covered := false
		for _, metadataPackage := range metadata.Packages {
			pkg, pending, ok := rustPackageFromCargoMetadata(root, metadataPackage)
			if !ok || (pkg.lib == nil && len(pkg.targets) == 0) {
				continue
			}
			covered = true
			packagesByRoot[pkg.root] = pkg
			pendingByRoot[pkg.root] = pending
			handledManifests[filepath.Clean(metadataPackage.ManifestPath)] = true
		}
		if covered {
			handledManifests[manifestPath] = true
		}
	}

	for rootPath, pending := range pendingByRoot {
		pkg, ok := packagesByRoot[rootPath]
		if !ok {
			continue
		}
		for _, dependency := range pending {
			target, ok := packagesByRoot[dependency.packageRoot]
			if !ok || target.lib == nil {
				continue
			}
			alias := dependency.rename
			if alias == "" {
				alias = target.crateID
			}
			pkg.dependencies = append(pkg.dependencies, rustLocalDependency{
				packageRoot: dependency.packageRoot,
				alias:       normalizeRustCrateID(alias),
				kind:        dependency.kind,
			})
		}
		packagesByRoot[rootPath] = pkg
	}

	index.packages = index.packages[:0]
	index.byCrateID = make(map[string][]rustPackage)
	index.byRoot = make(map[string]rustPackage)
	for _, pkg := range packagesByRoot {
		index.packages = append(index.packages, pkg)
		index.byCrateID[pkg.crateID] = append(index.byCrateID[pkg.crateID], pkg)
		index.byRoot[pkg.root] = pkg
	}
	sort.Slice(index.packages, func(i, j int) bool {
		if len(index.packages[i].root) == len(index.packages[j].root) {
			return index.packages[i].root < index.packages[j].root
		}
		return len(index.packages[i].root) > len(index.packages[j].root)
	})

	handled := 0
	for _, manifestPath := range manifestPaths {
		if handledManifests[filepath.Clean(manifestPath)] {
			handled++
		}
	}
	if err := index.buildRustModuleLocations(ctx, root, files); err != nil {
		return nil, nil, err
	}
	outcome := cargoMetadataOutcome(handled, len(manifestPaths))
	return index, &outcome, nil
}

func cargoMetadataOutcome(handled, total int) ScanSourceOutcome {
	outcome := ScanSourceOutcome{Name: "cargo-metadata", Status: ScanSourceAuthoritative}
	fallbacks := total - handled
	if fallbacks <= 0 {
		return outcome
	}
	outcome.Status = ScanSourceMixed
	if handled == 0 {
		outcome.Status = ScanSourceFallback
	}
	outcome.Detail = fmt.Sprintf("%d of %d Cargo manifests used fallback topology", fallbacks, total)
	return outcome
}

func discoverCargoManifests(ctx context.Context, root string, files []FileInfo) ([]string, error) {
	root = filepath.Clean(root)
	seen := make(map[string]bool)
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !strings.EqualFold(filepath.Ext(file.Path), ".rs") {
			continue
		}
		path := filepath.Clean(filepath.Join(root, file.Path))
		if _, ok := projectRelativePath(root, path); !ok {
			continue
		}
		for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			manifestPath := filepath.Join(dir, "Cargo.toml")
			if info, err := os.Stat(manifestPath); err == nil && !info.IsDir() {
				seen[filepath.Clean(manifestPath)] = true
			}
			if dir == root {
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir || !pathWithin(parent, root) {
				break
			}
		}
	}
	manifests := make([]string, 0, len(seen))
	for path := range seen {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		manifests = append(manifests, path)
	}
	sort.Slice(manifests, func(i, j int) bool {
		left, _ := filepath.Rel(root, manifests[i])
		right, _ := filepath.Rel(root, manifests[j])
		if pathDepth(left) == pathDepth(right) {
			return manifests[i] < manifests[j]
		}
		return pathDepth(left) < pathDepth(right)
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return manifests, nil
}

func rustPackageFromCargoMetadata(root string, metadata cargoMetadataPackage) (rustPackage, []pendingRustDependency, bool) {
	manifestPath, ok := projectRelativePath(root, metadata.ManifestPath)
	if !ok || filepath.Base(manifestPath) != "Cargo.toml" {
		return rustPackage{}, nil, false
	}
	pkg := rustPackage{
		crateID:       normalizeRustCrateID(metadata.Name),
		root:          filepath.Dir(manifestPath),
		authoritative: true,
	}
	if pkg.root == "" {
		pkg.root = "."
	}
	for _, metadataTarget := range metadata.Targets {
		rootFile, ok := projectRelativePath(root, metadataTarget.SrcPath)
		if !ok {
			continue
		}
		kind := rustCargoTargetKind(metadataTarget.Kind)
		if kind == "" {
			continue
		}
		target := rustTarget{rootFile: rootFile, sourceDir: filepath.Dir(rootFile), kind: kind}
		pkg.targets = append(pkg.targets, target)
		if kind == rustTargetLib && pkg.lib == nil {
			lib := target
			pkg.lib = &lib
			pkg.crateID = normalizeRustCrateID(metadataTarget.Name)
		}
	}
	sort.Slice(pkg.targets, func(i, j int) bool { return pkg.targets[i].rootFile < pkg.targets[j].rootFile })

	var pending []pendingRustDependency
	for _, dependency := range metadata.Dependencies {
		if dependency.Path == "" {
			continue
		}
		packageRoot, ok := projectRelativePath(root, dependency.Path)
		if !ok {
			continue
		}
		pending = append(pending, pendingRustDependency{
			packageRoot: packageRoot,
			rename:      dependency.Rename,
			kind:        dependency.Kind,
		})
	}
	return pkg, pending, true
}

func rustCargoTargetKind(kinds []string) string {
	for _, kind := range kinds {
		switch kind {
		case "lib", "proc-macro":
			return rustTargetLib
		case rustTargetBin, rustTargetExample, rustTargetTest, rustTargetBench, rustTargetCustomBuild:
			return kind
		}
	}
	return ""
}

func normalizeRustCrateID(name string) string {
	return strings.ReplaceAll(name, "-", "_")
}

func projectRelativePath(root, path string) (string, bool) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	rel, err := filepath.Rel(root, filepath.Clean(path))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.Clean(rel), true
}
