package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAskSubcommandMatchAndNoMatch(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":             "module fixture\n\ngo 1.24\n",
		"theme/use_theme.go": "package theme\n\nfunc UseTheme() {}\nfunc SaveTheme() {}\n",
		"net/client.go":      "package net\n\nfunc FetchJSON() {}\n",
	}
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	out := captureMainOutput(func() {
		if code := runAskSubcommand([]string{"UseTheme", "SaveTheme"}, root); code != 0 {
			t.Fatalf("ask exit = %d", code)
		}
	})
	if !strings.Contains(out, "EXPERIMENTAL") || !strings.Contains(out, "MATCH") {
		t.Fatalf("output =\n%s", out)
	}

	out = captureMainOutput(func() {
		if code := runAskSubcommand([]string{"quantum", "flux", "capacitor", "zucchini"}, root); code != 0 {
			t.Fatalf("ask exit = %d", code)
		}
	})
	if !strings.Contains(out, "NO_MATCH") {
		t.Fatalf("output =\n%s", out)
	}
}

func TestAskSubcommandRequiresQuery(t *testing.T) {
	out := captureMainOutput(func() {
		if code := runAskSubcommand(nil, t.TempDir()); code != 2 {
			t.Fatalf("exit = %d, want 2", code)
		}
	})
	if !strings.Contains(out, "needs a question") {
		t.Fatalf("output =\n%s", out)
	}
}
