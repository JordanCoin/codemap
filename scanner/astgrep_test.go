package scanner

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

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
