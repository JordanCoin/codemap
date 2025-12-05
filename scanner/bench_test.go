package scanner

import (
	"os"
	"testing"
	"time"
)

// TestBenchmarkAstGrep benchmarks the ast-grep scanner
func TestBenchmarkAstGrep(t *testing.T) {
	testDir := os.Getenv("BENCH_DIR")
	if testDir == "" {
		testDir = ".."
	}

	scanner, err := NewAstGrepScanner()
	if err != nil {
		t.Fatalf("ast-grep scanner error: %v", err)
	}
	if !scanner.Available() {
		t.Skip("sg not available")
	}
	defer scanner.Close()

	start := time.Now()
	results, err := scanner.ScanDirectory(testDir)
	if err != nil {
		t.Fatalf("ast-grep scan error: %v", err)
	}

	var totalFuncs, totalImports int
	for _, r := range results {
		totalFuncs += len(r.Functions)
		totalImports += len(r.Imports)
	}
	t.Logf("ast-grep: %v (%d functions, %d imports, %d files)",
		time.Since(start), totalFuncs, totalImports, len(results))
}

func TestAstGrepScanner(t *testing.T) {
	scanner, err := NewAstGrepScanner()
	if err != nil {
		t.Fatalf("Failed to create scanner: %v", err)
	}
	defer scanner.Close()

	if !scanner.Available() {
		t.Skip("sg not available")
	}

	// Test on codemap's own codebase
	results, err := scanner.ScanDirectory("..")
	if err != nil {
		t.Fatalf("ScanDirectory failed: %v", err)
	}

	if len(results) == 0 {
		t.Error("Expected some results")
	}

	var totalFuncs, totalImports int
	for _, r := range results {
		totalFuncs += len(r.Functions)
		totalImports += len(r.Imports)
	}

	t.Logf("Scanned %d files, found %d functions and %d imports", len(results), totalFuncs, totalImports)

	// Should find some Go functions
	if totalFuncs == 0 {
		t.Error("Expected to find some functions")
	}
}
