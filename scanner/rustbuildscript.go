package scanner

import (
	pathpkg "path"
	"path/filepath"
	"strings"
)

func parseRustBuildScriptInput(literal string) (string, bool) {
	value, ok := parseRustStringLiteral(literal)
	if !ok {
		return "", false
	}
	var path string
	for _, prefix := range []string{"cargo:rerun-if-changed=", "cargo::rerun-if-changed="} {
		if strings.HasPrefix(value, prefix) {
			path = strings.TrimPrefix(value, prefix)
			break
		}
	}
	if path == "" || path != strings.TrimSpace(path) || strings.ContainsAny(path, "{}\r\n\x00") {
		return "", false
	}
	return path, true
}

func resolveRustBuildScriptInput(fromFile, input string, idx *fileIndex, workspace *rustWorkspaceIndex) string {
	target, ok := workspace.targetForFile(fromFile, idx)
	if !ok || target.kind != rustTargetCustomBuild || target.rootFile != keyPath(fromFile) {
		return ""
	}
	pkg, ok := workspace.packageForFile(fromFile)
	if !ok || !pkg.authoritative {
		return ""
	}

	path := keyPath(input)
	if filepath.IsAbs(path) || filepath.VolumeName(path) != "" || path == ".." || strings.HasPrefix(path, "../") || strings.ContainsAny(input, "*?[") {
		return ""
	}
	candidate := pathpkg.Join(pkg.root, path)
	if candidate == keyPath(fromFile) {
		return ""
	}
	// Duplicate inventory entries make ownership ambiguous.
	if idx.exact(candidate) != 1 {
		return ""
	}
	return candidate
}
