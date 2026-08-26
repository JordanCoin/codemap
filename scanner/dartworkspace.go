package scanner

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type dartWorkspaceResolver struct {
	packageRoots  map[string][]string
	packageScopes []dartPackageScope
}

type dartPackageScope struct {
	root     string
	manifest pubspecManifest
}

func buildDartWorkspaceResolver(ctx context.Context, root string, files []FileInfo) (*dartWorkspaceResolver, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resolver := &dartWorkspaceResolver{packageRoots: make(map[string][]string)}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if filepath.Base(file.Path) != "pubspec.yaml" {
			continue
		}

		content, err := os.ReadFile(filepath.Join(root, file.Path))
		if err != nil {
			continue
		}
		manifest, err := decodePubspec(content)
		if err != nil || manifest.Name == "" {
			continue
		}

		packageRoot := filepath.Dir(file.Path)
		if packageRoot == "." {
			packageRoot = ""
		}
		resolver.packageRoots[manifest.Name] = append(resolver.packageRoots[manifest.Name], packageRoot)
		resolver.packageScopes = append(resolver.packageScopes, dartPackageScope{
			root:     packageRoot,
			manifest: manifest,
		})
	}
	sort.Slice(resolver.packageScopes, func(i, j int) bool {
		return len(resolver.packageScopes[i].root) > len(resolver.packageScopes[j].root)
	})
	return resolver, nil
}

func (r *dartWorkspaceResolver) resolve(imp, fromFile string, idx *fileIndex) []string {
	if r == nil {
		return nil
	}

	uri := strings.Trim(strings.TrimSpace(imp), "\"'`")
	if packageURI, ok := strings.CutPrefix(uri, "package:"); ok {
		pkgName, pkgPath, _ := strings.Cut(packageURI, "/")
		if pkgName == "" || pkgPath == "" {
			return nil
		}
		scope := r.nearestPackageScope(fromFile)
		if scope == nil {
			return nil
		}
		if scope.manifest.Name != pkgName && !pubspecDeclaresDependency(scope.manifest, pkgName) {
			return nil
		}
		roots := r.packageRoots[pkgName]
		if len(roots) != 1 {
			return nil
		}
		libRoot := filepath.Join(roots[0], "lib")
		candidate := filepath.Join(libRoot, filepath.FromSlash(pkgPath))
		if !pathContains(libRoot, candidate) {
			return nil
		}
		return tryExactMatch(candidate, idx, "dart")
	}

	if uri == "" || strings.Contains(uri, ":") || filepath.IsAbs(uri) {
		return nil
	}
	fromDir := filepath.Dir(fromFile)
	if fromDir == "." {
		fromDir = ""
	}
	return tryExactMatch(filepath.Join(fromDir, filepath.FromSlash(uri)), idx, "dart")
}

func (r *dartWorkspaceResolver) nearestPackageScope(fromFile string) *dartPackageScope {
	for i := range r.packageScopes {
		if pathContains(r.packageScopes[i].root, fromFile) {
			return &r.packageScopes[i]
		}
	}
	return nil
}

func pubspecDeclaresDependency(manifest pubspecManifest, name string) bool {
	if _, ok := manifest.Dependencies[name]; ok {
		return true
	}
	if _, ok := manifest.DevDependencies[name]; ok {
		return true
	}
	_, ok := manifest.DependencyOverrides[name]
	return ok
}
