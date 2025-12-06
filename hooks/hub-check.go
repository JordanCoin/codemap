//go:build ignore

package main

import (
	"fmt"
	"os"
	"strings"

	"codemap/scanner"
)

func main() {
	if len(os.Args) < 2 {
		os.Exit(0)
	}

	file := os.Args[1]

	fg, err := scanner.BuildFileGraph(".")
	if err != nil {
		os.Exit(0)
	}

	importers := fg.Importers[file]
	if len(importers) >= 3 {
		fmt.Printf("⚠️  HUB FILE: %s\n", file)
		fmt.Printf("   Imported by %d files - changes have wide impact!\n", len(importers))
		fmt.Println()
		fmt.Println("   Dependents:")
		for i, imp := range importers {
			if i >= 5 {
				fmt.Printf("   ... and %d more\n", len(importers)-5)
				break
			}
			fmt.Printf("   • %s\n", imp)
		}
	}

	// Also check if this file imports any hubs
	imports := fg.Imports[file]
	var hubImports []string
	for _, imp := range imports {
		if fg.IsHub(imp) {
			hubImports = append(hubImports, imp)
		}
	}
	if len(hubImports) > 0 {
		if len(importers) < 3 {
			fmt.Printf("📍 File: %s\n", file)
		}
		fmt.Printf("   Imports %d hub(s): %s\n", len(hubImports), strings.Join(hubImports, ", "))
	}
}
