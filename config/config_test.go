package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_MissingFile(t *testing.T) {
	cfg := Load("/nonexistent/path")
	if len(cfg.Only) != 0 || len(cfg.Exclude) != 0 || cfg.Depth != 0 {
		t.Errorf("expected zero-value config for missing file, got %+v", cfg)
	}
}

func TestLoad_ValidConfig(t *testing.T) {
	dir := t.TempDir()
	codemapDir := filepath.Join(dir, ".codemap")
	if err := os.MkdirAll(codemapDir, 0755); err != nil {
		t.Fatal(err)
	}

	data := `{
		"only": ["rs", "sh", "sql"],
		"exclude": ["docs/reference", "vendor"],
		"depth": 3
	}`
	if err := os.WriteFile(filepath.Join(codemapDir, "config.json"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := Load(dir)
	if len(cfg.Only) != 3 || cfg.Only[0] != "rs" || cfg.Only[1] != "sh" || cfg.Only[2] != "sql" {
		t.Errorf("unexpected Only: %v", cfg.Only)
	}
	if len(cfg.Exclude) != 2 || cfg.Exclude[0] != "docs/reference" || cfg.Exclude[1] != "vendor" {
		t.Errorf("unexpected Exclude: %v", cfg.Exclude)
	}
	if cfg.Depth != 3 {
		t.Errorf("unexpected Depth: %d", cfg.Depth)
	}
}

func TestLoad_PartialConfig(t *testing.T) {
	dir := t.TempDir()
	codemapDir := filepath.Join(dir, ".codemap")
	if err := os.MkdirAll(codemapDir, 0755); err != nil {
		t.Fatal(err)
	}

	data := `{"only": ["go", "ts"]}`
	if err := os.WriteFile(filepath.Join(codemapDir, "config.json"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := Load(dir)
	if len(cfg.Only) != 2 || cfg.Only[0] != "go" || cfg.Only[1] != "ts" {
		t.Errorf("unexpected Only: %v", cfg.Only)
	}
	if len(cfg.Exclude) != 0 {
		t.Errorf("expected empty Exclude, got %v", cfg.Exclude)
	}
	if cfg.Depth != 0 {
		t.Errorf("expected zero Depth, got %d", cfg.Depth)
	}
}

func TestLoad_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	codemapDir := filepath.Join(dir, ".codemap")
	if err := os.MkdirAll(codemapDir, 0755); err != nil {
		t.Fatal(err)
	}

	data := `{not valid json`
	if err := os.WriteFile(filepath.Join(codemapDir, "config.json"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := Load(dir)
	if len(cfg.Only) != 0 || len(cfg.Exclude) != 0 || cfg.Depth != 0 {
		t.Errorf("expected zero-value config for malformed JSON, got %+v", cfg)
	}
}

func TestLoad_EmptyObject(t *testing.T) {
	dir := t.TempDir()
	codemapDir := filepath.Join(dir, ".codemap")
	if err := os.MkdirAll(codemapDir, 0755); err != nil {
		t.Fatal(err)
	}

	data := `{}`
	if err := os.WriteFile(filepath.Join(codemapDir, "config.json"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := Load(dir)
	if len(cfg.Only) != 0 || len(cfg.Exclude) != 0 || cfg.Depth != 0 {
		t.Errorf("expected zero-value config for empty object, got %+v", cfg)
	}
}

func TestConfigPath(t *testing.T) {
	got := ConfigPath("/my/project")
	want := filepath.Join("/my/project", ".codemap", "config.json")
	if got != want {
		t.Errorf("ConfigPath = %q, want %q", got, want)
	}
}
