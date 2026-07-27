package scanner

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
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

func TestAstGrepScanDirectoryTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires shell script execution")
	}

	tmpDir := t.TempDir()
	fakeBinary := filepath.Join(tmpDir, "fake-sg.sh")
	if err := os.WriteFile(fakeBinary, []byte("#!/bin/sh\nsleep 5\n"), 0755); err != nil {
		t.Fatalf("failed to create fake ast-grep binary: %v", err)
	}

	scanner := &AstGrepScanner{
		rulesDir: tmpDir,
		binary:   fakeBinary,
	}

	prevTimeout := astGrepScanTimeout
	astGrepScanTimeout = 20 * time.Millisecond
	t.Cleanup(func() {
		astGrepScanTimeout = prevTimeout
	})

	results, err := scanner.ScanDirectory(tmpDir)
	if err != nil {
		t.Fatalf("expected graceful timeout handling, got error: %v", err)
	}
	if results != nil {
		t.Fatalf("expected nil results on timeout, got: %v", results)
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

	_, err := ScanForDeps(t.TempDir())
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
