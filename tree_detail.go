package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"

	"codemap/render"
	"codemap/scanner"
)

// countLines returns physical line counts for files that are not assets,
// not binary (NUL in the first 8000 bytes) and at most render.MaxLineCountBytes.
func countLines(root string, files []scanner.FileInfo) map[string]int {
	counts := make(map[string]int, len(files))
	for _, f := range files {
		if render.IsAssetExtension(strings.ToLower(f.Ext)) {
			continue
		}
		data, ok := readRegularFile(filepath.Join(root, f.Path))
		if !ok || bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
			continue
		}
		n := bytes.Count(data, []byte{'\n'})
		if len(data) > 0 && data[len(data)-1] != '\n' {
			n++
		}
		counts[filepath.ToSlash(f.Path)] = n
	}
	return counts
}

// readRegularFile reads path only when it is a regular file (not a symlink,
// FIFO or device) of at most render.MaxLineCountBytes. The scan's size is not
// trusted: the read itself is capped.
func readRegularFile(path string) ([]byte, bool) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, false
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	if opened, err := f.Stat(); err != nil || !os.SameFile(info, opened) {
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(f, render.MaxLineCountBytes+1))
	if err != nil || len(data) > render.MaxLineCountBytes {
		return nil, false
	}
	return data, true
}
