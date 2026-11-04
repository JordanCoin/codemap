package scanner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"codemap/analysis"
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
	"os/exec"
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
	if err := os.WriteFile(filepath.Join(rootDir, "src", "lib.rs"), []byte("use std::collections::HashMap;\n"), 0o644); err != nil {
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

func TestAstGrepCommandExecutesCmdShim(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only: cmd.exe shims")
	}

	tmpDir := t.TempDir()
	shim := filepath.Join(tmpDir, "ast-grep.cmd")
	script := "@echo ast-grep 0.45.1\n"
	if err := os.WriteFile(shim, []byte(script), 0644); err != nil {
		t.Fatalf("failed to create fake ast-grep.cmd shim: %v", err)
	}

	if !isAstGrepBinary(shim) {
		t.Fatalf("isAstGrepBinary should accept a working ast-grep.cmd shim: %s", shim)
	}
}

func TestScanDirectoryUsesCmdShimBinary(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only: cmd.exe shims")
	}

	tmpDir := t.TempDir()
	scanned := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(scanned, 0755); err != nil {
		t.Fatalf("failed to create scan target: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scanned, "main.ts"), []byte("import x from \"./y\";\n"), 0644); err != nil {
		t.Fatalf("failed to create source file: %v", err)
	}

	shim := filepath.Join(tmpDir, "ast-grep.cmd")
	// `exit 0` after printing the JSON array; cmd echo needs delayed expansion
	// avoided — the array is static.
	script := "@echo []\r\n@exit /b 0\r\n"
	if err := os.WriteFile(shim, []byte(script), 0644); err != nil {
		t.Fatalf("failed to create fake ast-grep.cmd shim: %v", err)
	}

	s, err := NewAstGrepScanner()
	if err != nil {
		t.Fatalf("NewAstGrepScanner: %v", err)
	}
	t.Cleanup(s.Close)
	s.binary = shim

	outcome, err := s.ScanDirectory(context.Background(), scanned)
	if err != nil {
		t.Fatalf("ScanDirectory via cmd shim failed: %v", err)
	}
	if outcome.Sources[0].Status != analysis.SourceAuthoritative {
		t.Fatalf("expected authoritative source status, got %v", outcome.Sources[0].Status)
	}
}

func TestAstGrepDartFlutter(t *testing.T) {
	analyzer := NewAstGrepAnalyzer()
	if !analyzer.Available() {
		t.Skip("ast-grep not available")
	}

	tmpDir := t.TempDir()
	dartFile := filepath.Join(tmpDir, "main.dart")
	source := `import 'dart:async';
import 'package:flutter/material.dart';
import 'src/platform_stub.dart'
    if (dart.library.io) 'src/platform_io.dart';
export 'src/routes.dart';
part 'main.g.dart';

T identity<T>(T value) => value;
void Function(int) makeCallback() => print;

void main() {}

class App extends StatelessWidget {
  Widget build(BuildContext context) {
    return const SizedBox();
  }
}
`
	if err := os.WriteFile(dartFile, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := analyzer.AnalyzeFile(dartFile)
	if err != nil {
		t.Fatalf("AnalyzeFile() error: %v", err)
	}
	if got == nil {
		t.Fatal("AnalyzeFile() returned nil")
	}
	if got.Language != "dart" {
		t.Fatalf("language = %q, want dart", got.Language)
	}

	functions := make(map[string]bool)
	for _, name := range got.Functions {
		functions[name] = true
	}
	for _, want := range []string{"main", "identity", "makeCallback", "build"} {
		if !functions[want] {
			t.Errorf("functions = %#v, missing %q", got.Functions, want)
		}
	}
	if functions["Function"] {
		t.Errorf("functions = %#v, extracted return type as function name", got.Functions)
	}

	imports := make(map[string]bool)
	for _, path := range got.Imports {
		imports[path] = true
	}
	for _, want := range []string{
		"dart:async",
		"package:flutter/material.dart",
		"src/platform_stub.dart",
		"src/platform_io.dart",
		"src/routes.dart",
		"main.g.dart",
	} {
		if !imports[want] {
			t.Errorf("imports = %#v, missing %q", got.Imports, want)
		}
	}
}

// ast-grep scans the file walker's inventory, passed after "--" as paths
// relative to cmd.Dir=root: hidden directories in, never-source ones out.
func TestScanDirectoryPassesWalkerInventoryAsRootRelativePaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires shell script execution")
	}

	tmpDir := t.TempDir()
	for _, name := range []string{".agents/a.js", "node_modules/pkg/dep.js"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(tmpDir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	capturedArgs := filepath.Join(tmpDir, "args.txt")
	capturedPwd := filepath.Join(tmpDir, "pwd.txt")
	fakeBinary := filepath.Join(tmpDir, "fake-sg.sh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CAPTURE_ARGS\"\npwd > \"$CAPTURE_PWD\"\nprintf '[]\\n'\n"
	if err := os.WriteFile(fakeBinary, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAPTURE_ARGS", capturedArgs)
	t.Setenv("CAPTURE_PWD", capturedPwd)

	scanner := &AstGrepScanner{rulesDir: tmpDir, binary: fakeBinary}
	if _, err := scanner.ScanDirectory(context.Background(), tmpDir); err != nil {
		t.Fatalf("ScanDirectory() error = %v", err)
	}

	args, err := os.ReadFile(capturedArgs)
	if err != nil {
		t.Fatalf("read captured args: %v", err)
	}
	argLines := strings.Split(strings.TrimRight(string(args), "\n"), "\n")

	sep := slices.Index(argLines, "--")
	if sep < 0 {
		t.Fatalf("expected -- before the scan paths, got: %s", args)
	}
	paths := argLines[sep+1:]
	if !slices.Contains(paths, filepath.Join(".agents", "a.js")) {
		t.Errorf("scan paths = %v, want the hidden-directory file root-relative (via cmd.Dir)", paths)
	}
	for _, path := range paths {
		if strings.Contains(path, "node_modules") {
			t.Errorf("scan paths = %v, include a never-source directory", paths)
		}
	}

	pwdBytes, err := os.ReadFile(capturedPwd)
	if err != nil {
		t.Fatalf("read captured pwd: %v", err)
	}
	gotPwd := canonicalTestPath(strings.TrimSpace(string(pwdBytes)))
	wantPwd := canonicalTestPath(tmpDir)
	if gotPwd != wantPwd {
		t.Fatalf("ast-grep cwd = %q, want %q (root)", gotPwd, wantPwd)
	}
}

// TestScanDirectoryIndexesHiddenDirectories is the end-to-end regression for
// the reported bug: a project whose source lives only under a dot-directory
// (not .git) was invisible to --deps/--importers with no way to opt in,
// because ast-grep skips hidden files and directories by default and
// codemap never overrode that default.
func TestScanDirectoryIndexesHiddenDirectories(t *testing.T) {
	scanner, err := NewAstGrepScanner()
	if err != nil {
		t.Fatalf("NewAstGrepScanner() error = %v", err)
	}
	t.Cleanup(scanner.Close)
	if !scanner.Available() {
		t.Skip("ast-grep not available")
	}

	root := t.TempDir()
	hidden := filepath.Join(root, ".agents")
	if err := os.MkdirAll(hidden, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hidden, "a.js"), []byte("const b = require('./b');\nmodule.exports = { b };\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hidden, "b.js"), []byte("module.exports = { hello: () => 'hi' };\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	outcome, err := scanner.ScanDirectory(context.Background(), root)
	if err != nil {
		t.Fatalf("ScanDirectory() error = %v", err)
	}
	paths := make(map[string]bool)
	for _, a := range outcome.Analyses {
		paths[filepath.ToSlash(a.Path)] = true
	}
	if !paths[".agents/a.js"] {
		t.Fatalf("analyses = %#v, want .agents/a.js indexed", outcome.Analyses)
	}

	fg, err := BuildFileGraphFromOutcome(context.Background(), root, outcome, Filters{})
	if err != nil {
		t.Fatalf("BuildFileGraphFromOutcome() error = %v", err)
	}
	importers := fg.Importers[".agents/b.js"]
	if len(importers) != 1 || importers[0] != ".agents/a.js" {
		t.Fatalf("importers of .agents/b.js = %v, want [.agents/a.js]", importers)
	}
}

// TestScanDirectoryDoesNotExcludeRootsNestedUnderAnIgnoredDirName is the
// regression for a bug the hidden-directory fix's first draft introduced:
// excluding codemap's never-source directory names (node_modules, vendor,
// testdata, ...) via bare --globs patterns matches those names anywhere in
// the walked path, including ancestors of root that happen to share the
// name. A project checked out under a path like ".../testdata/myproject"
// (exactly how this package's own fixtures live, under ../testdata/) must
// still be scanned in full.
func TestScanDirectoryDoesNotExcludeRootsNestedUnderAnIgnoredDirName(t *testing.T) {
	scanner, err := NewAstGrepScanner()
	if err != nil {
		t.Fatalf("NewAstGrepScanner() error = %v", err)
	}
	t.Cleanup(scanner.Close)
	if !scanner.Available() {
		t.Skip("ast-grep not available")
	}

	base := t.TempDir()
	// "testdata" and "vendor" are both in IgnoredDirs; root sits under one
	// of them as an ancestor, not as a subdirectory of root.
	root := filepath.Join(base, "testdata", "myproject")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.js"), []byte("const b = require('./b');\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	outcome, err := scanner.ScanDirectory(context.Background(), root)
	if err != nil {
		t.Fatalf("ScanDirectory() error = %v", err)
	}
	if len(outcome.Analyses) == 0 {
		t.Fatalf("analyses is empty; root nested under an ignored-dir-named ancestor was wrongly excluded in full")
	}
	paths := make(map[string]bool)
	for _, a := range outcome.Analyses {
		paths[filepath.ToSlash(a.Path)] = true
	}
	if !paths["a.js"] {
		t.Fatalf("analyses = %#v, want a.js indexed", outcome.Analyses)
	}
}

// TestScanDirectoryStillExcludesKnownNeverSourceDirs confirms that turning
// on hidden-directory scanning did not also start indexing .git internals
// or vendored/build directories that codemap's walker has always excluded.
func TestScanDirectoryStillExcludesKnownNeverSourceDirs(t *testing.T) {
	scanner, err := NewAstGrepScanner()
	if err != nil {
		t.Fatalf("NewAstGrepScanner() error = %v", err)
	}
	t.Cleanup(scanner.Close)
	if !scanner.Available() {
		t.Skip("ast-grep not available")
	}

	root := t.TempDir()
	for _, dir := range []string{".git/hooks", "node_modules/pkg", "vendor/pkg"} {
		full := filepath.Join(root, dir)
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(full, "dep.js"), []byte("const x = require('./x');\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "real.js"), []byte("const x = require('./x');\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	outcome, err := scanner.ScanDirectory(context.Background(), root)
	if err != nil {
		t.Fatalf("ScanDirectory() error = %v", err)
	}
	paths := make(map[string]bool)
	for _, a := range outcome.Analyses {
		paths[filepath.ToSlash(a.Path)] = true
	}
	if !paths["real.js"] {
		t.Fatalf("analyses = %#v, want real.js indexed", outcome.Analyses)
	}
	for path := range paths {
		if strings.Contains(path, ".git/") || strings.Contains(path, "node_modules/") || strings.Contains(path, "vendor/") {
			t.Fatalf("analyses = %#v, still indexed a never-source directory (%s)", outcome.Analyses, path)
		}
	}
}

// TestScanDirectoryExcludesNestedWorktreesByPath locks in that the ast-grep
// invocation excludes every nested repository the file walker skips (issue
// #131): a linked worktree under a dotted directory and a plain clone two
// levels down, each by its root-relative path, so --deps agrees with
// `codemap .` on what is project source.
func TestScanDirectoryExcludesNestedWorktreesByPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires shell script execution")
	}
	root := nestedRepoFixture(t)

	tmpDir := t.TempDir()
	capturedArgs := filepath.Join(tmpDir, "args.txt")
	fakeBinary := filepath.Join(tmpDir, "fake-sg.sh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CAPTURE_ARGS\"\nprintf '[]\\n'\n"
	if err := os.WriteFile(fakeBinary, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAPTURE_ARGS", capturedArgs)

	scanner := &AstGrepScanner{rulesDir: tmpDir, binary: fakeBinary}
	if _, err := scanner.ScanDirectory(context.Background(), root); err != nil {
		t.Fatalf("ScanDirectory() error = %v", err)
	}
	args, err := os.ReadFile(capturedArgs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(args), "agent-x") || strings.Contains(string(args), "sub-clone") {
		t.Errorf("nested repositories must not be scanned, got:\n%s", args)
	}
	if !strings.Contains(string(args), "--\nlib/lib.go\nlib/util.go\nmain.go\n") {
		t.Errorf("project files must be scanned, got:\n%s", args)
	}
}

// TestScanDirectoryRealAstGrepSkipsNestedWorktree runs the real ast-grep on
// the same fixture and checks the nested repositories' files are not indexed.
func TestScanDirectoryRealAstGrepSkipsNestedWorktree(t *testing.T) {
	scanner, err := NewAstGrepScanner()
	if err != nil {
		t.Fatalf("NewAstGrepScanner() error = %v", err)
	}
	t.Cleanup(scanner.Close)
	if !scanner.Available() {
		t.Skip("ast-grep not available")
	}
	root := nestedRepoFixture(t)
	for _, name := range []string{"a.js", ".claude/worktrees/agent-x/a.js", "tools/sub-clone/a.js"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("const b = require('./b');\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	outcome, err := scanner.ScanDirectory(context.Background(), root)
	if err != nil {
		t.Fatalf("ScanDirectory() error = %v", err)
	}
	var paths []string
	for _, a := range outcome.Analyses {
		paths = append(paths, filepath.ToSlash(a.Path))
	}
	sort.Strings(paths)
	if !reflect.DeepEqual(paths, []string{"a.js"}) {
		t.Fatalf("analyses = %v, want only a.js (nested worktree and clone excluded)", paths)
	}
}

// A batch that times out costs only its own files (#269): the rest of the
// graph survives, coverage reads partial, and the detail names what was skipped.
func TestScanDirectoryTimedOutBatchYieldsPartialGraph(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires shell script execution")
	}
	root := t.TempDir()
	for _, name := range []string{"fast.go", "slow.go"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("package p\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fakeBinary := filepath.Join(t.TempDir(), "fake-sg.sh")
	script := `#!/bin/sh
for a in "$@"; do [ "$a" = slow.go ] && sleep 30; done
printf '%s\n' '[{"file":"fast.go","ruleId":"go-imports","range":{"start":{"line":1,"column":1}},"text":"import \"fmt\"","metaVariables":{"single":{"PATH":{"text":"fmt"}}}}]'
`
	if err := os.WriteFile(fakeBinary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	prevTimeout, prevBatch := astGrepScanTimeout, astGrepBatchArgBytes
	astGrepScanTimeout, astGrepBatchArgBytes = 2*time.Second, 1
	t.Cleanup(func() { astGrepScanTimeout, astGrepBatchArgBytes = prevTimeout, prevBatch })

	scanner := &AstGrepScanner{rulesDir: t.TempDir(), binary: fakeBinary}
	outcome, err := scanner.ScanDirectory(context.Background(), root)
	if err != nil {
		t.Fatalf("ScanDirectory() error = %v", err)
	}
	if len(outcome.Analyses) != 1 || outcome.Analyses[0].Path != "fast.go" {
		t.Fatalf("analyses = %#v, want fast.go kept (sources %#v)", outcome.Analyses, outcome.Sources)
	}
	source := outcome.Sources[0]
	if source.Status != ScanSourceMixed || !strings.Contains(source.Detail, "timed out") || !strings.Contains(source.Detail, "slow.go") {
		t.Fatalf("source = %#v, want mixed with a timeout detail naming slow.go", source)
	}
	if got := CoverageFromSources(outcome.Sources).Status; got != analysis.CoveragePartial {
		t.Fatalf("coverage = %q, want partial", got)
	}
}

// A one-line bundle is left out of the scan with a note: ast-grep's JSON
// repeats the whole line for every match in it (#269).
func TestScanDirectorySkipsMinifiedFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires shell script execution")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "bundle.cjs"), []byte("var a=1;"+strings.Repeat("x", astGrepMaxLineBytes)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app.js"), []byte("require('./b');\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	capturedArgs := filepath.Join(t.TempDir(), "args.txt")
	fakeBinary := filepath.Join(t.TempDir(), "fake-sg.sh")
	if err := os.WriteFile(fakeBinary, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CAPTURE_ARGS\"\nprintf '[]\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAPTURE_ARGS", capturedArgs)

	scanner := &AstGrepScanner{rulesDir: t.TempDir(), binary: fakeBinary}
	outcome, err := scanner.ScanDirectory(context.Background(), root)
	if err != nil {
		t.Fatalf("ScanDirectory() error = %v", err)
	}
	args, err := os.ReadFile(capturedArgs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(args), "bundle.cjs") || !strings.Contains(string(args), "app.js") {
		t.Fatalf("args = %s, want app.js scanned and bundle.cjs skipped", args)
	}
	source := outcome.Sources[0]
	if source.Status != ScanSourceAuthoritative || !strings.Contains(source.Detail, "bundle.cjs") {
		t.Fatalf("source = %#v, want authoritative with a note naming bundle.cjs", source)
	}
}

// recordAstGrepCommands swaps the exec seam for one that records every argv
// and runs a harmless command printing "[]" in its place.
func recordAstGrepCommands(t *testing.T) *[][]string {
	t.Helper()
	var calls [][]string
	prev := execCommandContext
	execCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if slices.Contains(args, "--version") {
			return exec.CommandContext(ctx, name, args...)
		}
		calls = append(calls, append([]string{name}, args...))
		if runtime.GOOS == "windows" {
			return exec.CommandContext(ctx, "cmd.exe", "/c", "echo []")
		}
		return exec.CommandContext(ctx, "sh", "-c", "echo '[]'")
	}
	t.Cleanup(func() { execCommandContext = prev })
	return &calls
}

// hostileScanRoot holds files whose names cmd.exe would execute or expand.
func hostileScanRoot(t *testing.T) (string, []string) {
	t.Helper()
	names := []string{"x&calc.ts", "y%PATH%.ts", "z^w.ts", "-dash.ts"}
	if runtime.GOOS != "windows" {
		names = append(names, `q"r.ts`)
	}
	root := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(root, name), []byte("export {};\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, names
}

// A cmd shim with no resolvable native ast-grep scans "." in one pass: no
// repository path may reach the cmd.exe command line.
func TestCmdShimScanKeepsRepoPathsOffCmdLine(t *testing.T) {
	prevGOOS := goos
	goos = "windows"
	t.Cleanup(func() { goos = prevGOOS })
	calls := recordAstGrepCommands(t)
	root, names := hostileScanRoot(t)
	shim := filepath.Join(t.TempDir(), "ast-grep.cmd")
	if err := os.WriteFile(shim, []byte("@echo off\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &AstGrepScanner{binary: shim}
	t.Cleanup(s.Close)

	outcome, err := s.ScanDirectory(context.Background(), root)
	if err != nil {
		t.Fatalf("ScanDirectory() error = %v", err)
	}
	if len(*calls) == 0 {
		t.Fatal("ast-grep was never invoked")
	}
	for _, call := range *calls {
		if call[0] != "cmd.exe" {
			t.Fatalf("argv %q: want every call through cmd.exe for an unresolved shim", call)
		}
		for _, arg := range call {
			for _, name := range names {
				if strings.Contains(arg, strings.TrimSuffix(name, ".ts")) {
					t.Fatalf("repo path %q reached the cmd.exe line: %q", name, call)
				}
			}
		}
	}
	if detail := outcome.Sources[0].Detail; !strings.Contains(detail, "cmd.exe shim") {
		t.Fatalf("detail = %q, want the one-pass shim note", detail)
	}
}

// A cmd shim whose native target resolves runs that binary directly, so the
// batched paths go to it after "--" and never through cmd.exe.
func TestCmdShimResolvesToNativeBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake native binary is a shell script")
	}
	prevGOOS := goos
	goos = "windows"
	t.Cleanup(func() { goos = prevGOOS })
	binDir := t.TempDir()
	native := filepath.Join(binDir, "node_modules", "@ast-grep", "cli-win32-x64-msvc", "ast-grep.exe")
	if err := os.MkdirAll(filepath.Dir(native), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(native, []byte("#!/bin/sh\necho ast-grep 0.42.0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(binDir, "ast-grep.cmd")
	if err := os.WriteFile(shim, []byte("@ECHO off\r\n\"%dp0%\\node_modules\\@ast-grep\\cli-win32-x64-msvc\\ast-grep.exe\" %*\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := resolveCmdShimTarget(shim); got != native {
		t.Fatalf("resolveCmdShimTarget = %q, want %q", got, native)
	}

	calls := recordAstGrepCommands(t)
	root, names := hostileScanRoot(t)
	s := &AstGrepScanner{binary: shim}
	t.Cleanup(s.Close)
	if _, err := s.ScanDirectory(context.Background(), root); err != nil {
		t.Fatalf("ScanDirectory() error = %v", err)
	}
	var scan []string
	for _, call := range *calls {
		if call[0] == "cmd.exe" {
			t.Fatalf("argv %q went through cmd.exe", call)
		}
		if slices.Contains(call, "scan") {
			scan = call
		}
	}
	if scan == nil || scan[0] != native {
		t.Fatalf("scan argv = %q, want the native binary", scan)
	}
	sep := slices.Index(scan, "--")
	if sep < 0 {
		t.Fatalf("scan argv = %q, want -- before the paths", scan)
	}
	for _, name := range names {
		if i := slices.Index(scan, name); i <= sep {
			t.Fatalf("path %q at argv index %d, want it after -- (index %d)", name, i, sep)
		}
	}
}
