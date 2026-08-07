package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunDoctorUsesNearestGitRootFromNestedDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	setupCode := RunSetup([]string{"--agent", "claude"}, root)
	if setupCode != 0 {
		t.Fatalf("RunSetup() exit code = %d, want 0", setupCode)
	}

	prevLookPath := doctorLookPath
	prevVersionProbe := doctorVersionProbe
	prevMCPProbe := doctorMCPProbe
	doctorLookPath = func(name string) (string, error) {
		return filepath.Join("/tmp", name), nil
	}
	doctorVersionProbe = func(launch doctorManagedLaunch) error { return nil }
	doctorMCPProbe = func(launch doctorManagedLaunch) error { return nil }
	t.Cleanup(func() {
		doctorLookPath = prevLookPath
		doctorVersionProbe = prevVersionProbe
		doctorMCPProbe = prevMCPProbe
	})

	nested := filepath.Join(root, "pkg", "feature")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	var code int
	out := captureOutput(func() {
		code = RunDoctor([]string{"--agent", "claude"}, nested)
	})
	if code != 0 {
		t.Fatalf("RunDoctor() exit code = %d, want 0 with output:\n%s", code, out)
	}

	rootConfig := filepath.Join(root, ".codemap", "config.json")
	nestedConfig := filepath.Join(nested, ".codemap", "config.json")
	if !strings.Contains(out, rootConfig) {
		t.Fatalf("expected doctor output to validate root config %q, got:\n%s", rootConfig, out)
	}
	if strings.Contains(out, nestedConfig) {
		t.Fatalf("expected doctor output to avoid nested config path %q, got:\n%s", nestedConfig, out)
	}
}
