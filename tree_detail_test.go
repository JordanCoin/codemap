package main

import (
	"os"
	"path/filepath"
	"testing"

	"codemap/scanner"
)

func TestCountLinesPhysicalLinesSkippingBinaryAndAssets(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) scanner.FileInfo {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return scanner.FileInfo{Path: name, Ext: filepath.Ext(name), Size: int64(len(body))}
	}
	files := []scanner.FileInfo{
		write("a.go", "one\ntwo\n"),
		write("b.go", "one\ntwo"),
		write("empty.go", ""),
		write("blob.dat", "x\x00y\n"),
		write("pic.png", "not\nreally\n"),
	}
	got := countLines(root, files)
	want := map[string]int{"a.go": 2, "b.go": 2, "empty.go": 0}
	if len(got) != len(want) {
		t.Fatalf("countLines = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("countLines = %v, want %v", got, want)
		}
	}
}

func TestCountLinesSkipsSymlinkOutsideRoot(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	target := filepath.Join(outside, "secret.go")
	if err := os.WriteFile(target, []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "link.go")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	got := countLines(root, []scanner.FileInfo{{Path: "link.go", Ext: ".go", Size: 4}})
	if _, counted := got["link.go"]; counted {
		t.Fatalf("symlink counted: %v", got)
	}
}

func TestExportedTopLevel(t *testing.T) {
	cases := []struct {
		lang, name, text string
		want             bool
	}{
		{"go", "Load", "func Load(root string) Config {", true},
		{"go", "load", "func load() {", false},
		{"go", "String", "func (c Config) String() string {", false},
		{"python", "run", "def run():", true},
		{"python", "_private", "def _private():", false},
		{"python", "method", "    def method(self):", false},
		{"typescript", "debug", "export function debug() {", true},
		{"typescript", "helper", "function helper() {", false},
	}
	for _, c := range cases {
		if got := exportedTopLevel(c.lang, c.name, c.text); got != c.want {
			t.Errorf("exportedTopLevel(%q, %q, %q) = %v, want %v", c.lang, c.name, c.text, got, c.want)
		}
	}
}
