package scanner

import (
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
	if !ok || target.kind != rustTargetCustomBuild || target.rootFile != filepath.Clean(fromFile) {
		return ""
	}
	pkg, ok := workspace.packageForFile(fromFile)
	if !ok || !pkg.authoritative {
		return ""
	}

	path := filepath.Clean(filepath.FromSlash(input))
	if filepath.IsAbs(path) || filepath.VolumeName(path) != "" || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) || strings.ContainsAny(input, "*?[") {
		return ""
	}
	candidate := filepath.Clean(filepath.Join(pkg.root, path))
	files := idx.byExact[candidate]
	if len(files) != 1 || files[0] != candidate || candidate == fromFile {
		return ""
	}
	return candidate
}
