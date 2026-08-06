package scanner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func canonicalTestPath(path string) string {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	if resolvedDir, err := filepath.EvalSymlinks(dir); err == nil {
		return filepath.Join(resolvedDir, base)
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

func TestAstGrepScannerUsesEmbeddedRulesWithoutWritableTempDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires shell script execution")
	}

	tmpDir := t.TempDir()
	blockedTemp := filepath.Join(tmpDir, "not-a-directory")
	if err := os.WriteFile(blockedTemp, []byte("blocked"), 0644); err != nil {
		t.Fatalf("failed to create blocked temp path: %v", err)
	}

	capturedArgs := filepath.Join(tmpDir, "args.txt")
	fakeBinary := filepath.Join(tmpDir, "fake-sg.sh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CAPTURE_ARGS\"\nprintf '[]\\n'\n"
	if err := os.WriteFile(fakeBinary, []byte(script), 0755); err != nil {
		t.Fatalf("failed to create fake ast-grep binary: %v", err)
	}

	t.Setenv("TMPDIR", blockedTemp)
	t.Setenv("TMP", blockedTemp)
	t.Setenv("TEMP", blockedTemp)
	t.Setenv("CAPTURE_ARGS", capturedArgs)

	scanner, err := NewAstGrepScanner()
	if err != nil {
		t.Fatalf("NewAstGrepScanner should not require writable temp storage: %v", err)
	}
	t.Cleanup(scanner.Close)
	scanner.binary = fakeBinary

	if _, err := scanner.ScanDirectory(context.Background(), tmpDir); err != nil {
		t.Fatalf("ScanDirectory failed: %v", err)
	}

	args, err := os.ReadFile(capturedArgs)
	if err != nil {
		t.Fatalf("failed to read captured arguments: %v", err)
	}
	if !strings.Contains(string(args), "--inline-rules\n") || !strings.Contains(string(args), "id: go-imports\n") {
		t.Fatalf("expected embedded Go rules in --inline-rules argument, got: %s", args)
	}
}

func TestAstGrepAnalyzer(t *testing.T) {
	analyzer := NewAstGrepAnalyzer()
	if !analyzer.Available() {
		t.Skip("ast-grep (sg) not installed")
	}

	// Test Go file
	tmpDir := t.TempDir()
	goFile := filepath.Join(tmpDir, "test.go")
	os.WriteFile(goFile, []byte(`package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("hello")
}

func helper(x int) int {
	return x * 2
}
`), 0644)

	analysis, err := analyzer.AnalyzeFile(goFile)
	if err != nil {
		t.Fatalf("AnalyzeFile failed: %v", err)
	}

	if analysis == nil {
		t.Fatal("Expected analysis, got nil")
	}

	// Check functions
	funcs := make(map[string]bool)
	for _, f := range analysis.Functions {
		funcs[f] = true
	}
	if !funcs["main"] {
		t.Error("Expected main function")
	}
	if !funcs["helper"] {
		t.Error("Expected helper function")
	}

	// Check imports
	imports := make(map[string]bool)
	for _, i := range analysis.Imports {
		imports[i] = true
	}
	if !imports["fmt"] {
		t.Errorf("Expected fmt import, got: %v", analysis.Imports)
	}
	if !imports["os"] {
		t.Errorf("Expected os import, got: %v", analysis.Imports)
	}
}

func TestAstGrepPython(t *testing.T) {
	analyzer := NewAstGrepAnalyzer()
	if !analyzer.Available() {
		t.Skip("ast-grep (sg) not installed")
	}

	tmpDir := t.TempDir()
	pyFile := filepath.Join(tmpDir, "test.py")
	os.WriteFile(pyFile, []byte(`import os
from pathlib import Path

def hello(name):
    print(f"Hello {name}")

def greet():
    pass
`), 0644)

	analysis, err := analyzer.AnalyzeFile(pyFile)
	if err != nil {
		t.Fatalf("AnalyzeFile failed: %v", err)
	}

	funcs := make(map[string]bool)
	for _, f := range analysis.Functions {
		funcs[f] = true
	}
	if !funcs["hello"] {
		t.Errorf("Expected hello function, got: %v", analysis.Functions)
	}
	if !funcs["greet"] {
		t.Errorf("Expected greet function, got: %v", analysis.Functions)
	}

	imports := make(map[string]bool)
	for _, i := range analysis.Imports {
		imports[i] = true
	}
	if !imports["os"] {
		t.Errorf("Expected os import, got: %v", analysis.Imports)
	}
}

func TestAstGrepCSharp(t *testing.T) {
	analyzer := NewAstGrepAnalyzer()
	if !analyzer.Available() {
		t.Skip("ast-grep (sg) not installed")
	}

	tmpDir := t.TempDir()
	csFile := filepath.Join(tmpDir, "Program.cs")
	os.WriteFile(csFile, []byte(`using System;
using System.Collections.Generic;
using System.Linq;

namespace TestApp
{
    public class Program
    {
        public static void Main(string[] args)
        {
            Console.WriteLine("Hello");
        }
        
        public int Calculate(int x)
        {
            return x * 2;
        }
    }
}
`), 0644)

	analysis, err := analyzer.AnalyzeFile(csFile)
	if err != nil {
		t.Fatalf("AnalyzeFile failed: %v", err)
	}

	if analysis == nil {
		t.Fatal("Expected analysis, got nil")
	}

	funcs := make(map[string]bool)
	for _, f := range analysis.Functions {
		funcs[f] = true
	}
	if !funcs["Main"] {
		t.Errorf("Expected Main function, got: %v", analysis.Functions)
	}
	if !funcs["Calculate"] {
		t.Errorf("Expected Calculate function, got: %v", analysis.Functions)
	}

	imports := make(map[string]bool)
	for _, i := range analysis.Imports {
		imports[i] = true
	}
	if !imports["System"] {
		t.Errorf("Expected System import, got: %v", analysis.Imports)
	}
	if !imports["System.Collections.Generic"] {
		t.Errorf("Expected System.Collections.Generic import, got: %v", analysis.Imports)
	}
}

func TestAstGrepScanDirectoryTimeoutIsIncomplete(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires shell script execution")
	}

	tmpDir := t.TempDir()
	fakeBinary := filepath.Join(tmpDir, "fake-sg.sh")
	if err := os.WriteFile(fakeBinary, []byte("#!/bin/sh\nsleep 5\n"), 0755); err != nil {
		t.Fatalf("failed to create fake ast-grep binary: %v", err)
	}

	scanner := &AstGrepScanner{rulesDir: tmpDir, binary: fakeBinary}
	prevTimeout := astGrepScanTimeout
	astGrepScanTimeout = 20 * time.Millisecond
	t.Cleanup(func() { astGrepScanTimeout = prevTimeout })

	outcome, err := scanner.ScanDirectory(context.Background(), tmpDir)
	if err != nil {
		t.Fatalf("ScanDirectory() error = %v, want degraded outcome", err)
	}
	if got := outcome.Sources[0].Status; got != ScanSourceTimeout {
		t.Fatalf("status = %q, want %q", got, ScanSourceTimeout)
	}
}

func TestAstGrepScanDirectoryOutcomeDistinguishesEmptyFromFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires shell script execution")
	}

	for _, tt := range []struct {
		name       string
		script     string
		wantStatus ScanSourceStatus
		wantOK     bool
	}{
		{name: "valid empty result", script: "#!/bin/sh\nprintf '[]\\n'\n", wantStatus: ScanSourceAuthoritative, wantOK: true},
		{name: "nonzero JSON output", script: "#!/bin/sh\nprintf '[]\\n'\nexit 2\n", wantStatus: ScanSourceFailed},
		{name: "malformed JSON", script: "#!/bin/sh\nprintf '[{'\n", wantStatus: ScanSourceFailed},
		{name: "terminated by signal", script: "#!/bin/sh\nkill -TERM $$\n", wantStatus: ScanSourceFailed},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			fakeBinary := filepath.Join(tmpDir, "fake-sg.sh")
			if err := os.WriteFile(fakeBinary, []byte(tt.script), 0755); err != nil {
				t.Fatal(err)
			}
			scanner := &AstGrepScanner{rulesDir: tmpDir, binary: fakeBinary}

			outcome, err := scanner.ScanDirectory(context.Background(), tmpDir)
			if tt.wantOK {
				if err != nil {
					t.Fatalf("ScanDirectory() error = %v", err)
				}
				if len(outcome.Analyses) != 0 {
					t.Fatalf("analyses = %#v, want empty", outcome.Analyses)
				}
				if got := outcome.Sources[0].Status; got != tt.wantStatus {
					t.Fatalf("status = %q, want %q", got, tt.wantStatus)
				}
				return
			}

			// Degraded scans fail closed: the outcome carries the source status
			// instead of a hard error.
			if err != nil {
				t.Fatalf("ScanDirectory() error = %v, want degraded outcome", err)
			}
			if len(outcome.Analyses) != 0 {
				t.Fatalf("analyses = %#v, want empty on failure", outcome.Analyses)
			}
			if got := outcome.Sources[0].Status; got != tt.wantStatus {
				t.Fatalf("status = %q, want %q", got, tt.wantStatus)
			}
		})
	}
}

func TestAstGrepScanDirectoryUnavailableIsIncomplete(t *testing.T) {
	scanner := &AstGrepScanner{}
	_, err := scanner.ScanDirectory(context.Background(), t.TempDir())
	var incomplete *IncompleteScanError
	if !errors.As(err, &incomplete) {
		t.Fatalf("error = %v, want IncompleteScanError", err)
	}
	if got, want := incomplete.Outcome.Status, ScanSourceUnavailable; got != want {
		t.Fatalf("status = %q, want %q", got, want)
	}
}

// Regression for PR #105 follow-up: ast-grep extraction is authoritative even
// when the scan contains Rust files. The Rust caveat (macro-generated,
// string-routed, #[path] edges) is a *resolution* gap owned by rust-cargo, so
// ast-grep must not be demoted to mixed with a Rust-specific detail — doing so
// degrades a Go+Rust monorepo's Go analysis and duplicates the caveat.
func TestAstGrepScanDirectoryRustStaysAuthoritative(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires shell script execution")
	}

	tmpDir := t.TempDir()
	rootDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(filepath.Join(rootDir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeBinary := filepath.Join(tmpDir, "fake-sg.sh")
	// Emit a successful scan whose only analysis is a Rust file.
	match := `[{"file":"` + filepath.Join(rootDir, "src", "lib.rs") +
		`","ruleId":"rust-use-imports","range":{"start":{"line":1,"column":1}}` +
		`,"text":"use std::collections::HashMap;","metaVariables":{"single":{"PATH":{"text":"std::collections::HashMap"}}}}]`
	script := "#!/bin/sh\nprintf '%s\\n' '" + match + "'\n"
	if err := os.WriteFile(fakeBinary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	scanner := &AstGrepScanner{rulesDir: tmpDir, binary: fakeBinary}

	outcome, err := scanner.ScanDirectory(context.Background(), rootDir)
	if err != nil {
		t.Fatalf("ScanDirectory() error = %v", err)
	}
	if len(outcome.Analyses) != 1 || outcome.Analyses[0].Language != "rust" {
		t.Fatalf("analyses = %#v, want one Rust analysis", outcome.Analyses)
	}
	if len(outcome.Sources) != 1 {
		t.Fatalf("sources = %#v, want exactly ast-grep", outcome.Sources)
	}
	source := outcome.Sources[0]
	if source.Name != "ast-grep" || source.Status != ScanSourceAuthoritative {
		t.Fatalf("source = %#v, want authoritative ast-grep", source)
	}
	if source.Detail != "" {
		t.Fatalf("ast-grep detail = %q, want empty (Rust caveat belongs to rust-cargo)", source.Detail)
	}
}

func TestScanForDepsRejectsNonAstGrepSg(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires shell script execution")
	}

	tmpDir := t.TempDir()
	fakeBinary := filepath.Join(tmpDir, "sg")
	if err := os.WriteFile(fakeBinary, []byte("#!/bin/sh\necho 'setgroups utility' >&2\nexit 1\n"), 0755); err != nil {
		t.Fatalf("failed to create fake sg binary: %v", err)
	}

	oldPath := os.Getenv("PATH")
	if err := os.Setenv("PATH", tmpDir); err != nil {
		t.Fatalf("failed to set PATH: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("PATH", oldPath)
	})

	_, err := ScanForDeps(context.Background(), t.TempDir(), Filters{})
	if !errors.Is(err, ErrAstGrepNotFound) {
		t.Fatalf("expected ErrAstGrepNotFound, got %v", err)
	}
}

func TestBundledAstGrepCandidates(t *testing.T) {
	tmpDir := t.TempDir()
	exeName := "codemap"
	wantNames := []string{"ast-grep", "sg"}
	if runtime.GOOS == "windows" {
		exeName += ".exe"
		wantNames = []string{"ast-grep.exe", "sg.exe"}
	}

	exePath := filepath.Join(tmpDir, exeName)
	if err := os.WriteFile(exePath, []byte(""), 0755); err != nil {
		t.Fatalf("failed to create fake executable: %v", err)
	}

	got := bundledAstGrepCandidates(exePath)
	if len(got) != len(wantNames) {
		t.Fatalf("expected %d candidates, got %d: %v", len(wantNames), len(got), got)
	}

	for i, name := range wantNames {
		want := canonicalTestPath(filepath.Join(tmpDir, name))
		if got[i] != want {
			t.Fatalf("candidate %d: expected %q, got %q", i, want, got[i])
		}
	}
}

func TestFindBundledAstGrepBinaryPrefersSiblingAstGrep(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires shell script execution")
	}

	tmpDir := t.TempDir()
	exePath := filepath.Join(tmpDir, "codemap")
	if err := os.WriteFile(exePath, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("failed to create fake codemap binary: %v", err)
	}

	bundled := filepath.Join(tmpDir, "ast-grep")
	if err := os.WriteFile(bundled, []byte("#!/bin/sh\necho 'ast-grep 0.42.1'\n"), 0755); err != nil {
		t.Fatalf("failed to create fake bundled ast-grep: %v", err)
	}

	got := ""
	for _, candidate := range bundledAstGrepCandidates(exePath) {
		if isAstGrepBinary(candidate) {
			got = candidate
			break
		}
	}

	if got != canonicalTestPath(bundled) {
		t.Fatalf("expected bundled ast-grep %q, got %q", canonicalTestPath(bundled), got)
	}
}
