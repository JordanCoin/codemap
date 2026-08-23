package scanner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

type dartWorkspaceResolver struct {
	packageRoots map[string][]string
}

func buildDartWorkspaceResolver(ctx context.Context, root string, files []FileInfo) (*dartWorkspaceResolver, error) {
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
	}
	return resolver, nil
}

func (r *dartWorkspaceResolver) resolve(imp, fromFile string, idx *fileIndex) []string {
	if r == nil {
		return nil
	}

	uri := strings.Trim(strings.TrimSpace(imp), "\"'`")
	if packageURI, ok := strings.CutPrefix(uri, "package:"); ok {
		parts := strings.SplitN(packageURI, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil
		}
		roots := r.packageRoots[parts[0]]
		if len(roots) != 1 {
			return nil
		}
		candidate := filepath.Join(roots[0], "lib", filepath.FromSlash(parts[1]))
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
