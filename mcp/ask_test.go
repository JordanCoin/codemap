package codemapmcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codemap/ask"
)

func TestHandleAskMatchAndNoMatch(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":                 "module fixture\n\ngo 1.24\n",
		"theme/use_theme.go":     "package theme\n\nfunc UseTheme() {}\nfunc SaveTheme() {}\n",
		"storage/preferences.go": "package storage\n\nfunc LoadTheme() {}\n",
		"net/client.go":          "package net\n\nfunc FetchJSON() {}\n",
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

	match, structured, err := handleAsk(context.Background(), nil, AskInput{Path: root, Query: "UseTheme SaveTheme", Budget: 900})
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, match)
	if !strings.Contains(text, "EXPERIMENTAL") || !strings.Contains(text, ask.StatusMatch) {
		t.Fatalf("match text =\n%s", text)
	}
	answer, ok := structured.(ask.Answer)
	if !ok || answer.Status != ask.StatusMatch || len(answer.Cards) == 0 {
		t.Fatalf("structured = %#v", structured)
	}

	miss, structured, err := handleAsk(context.Background(), nil, AskInput{Path: root, Query: "quantum flux capacitor zucchini"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resultText(t, miss), ask.StatusNoMatch) {
		t.Fatalf("no-match text =\n%s", resultText(t, miss))
	}
	missAnswer, ok := structured.(ask.Answer)
	if !ok || missAnswer.Status != ask.StatusNoMatch || len(missAnswer.Cards) != 0 {
		t.Fatalf("no-match structured = %#v", structured)
	}
}

func TestHandleAskRejectsEmptyQuery(t *testing.T) {
	result, _, err := handleAsk(context.Background(), nil, AskInput{Path: t.TempDir(), Query: "  "})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(resultText(t, result), "needs a query") {
		t.Fatalf("result = %#v", result)
	}
}
