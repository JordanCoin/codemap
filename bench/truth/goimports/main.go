// goimports prints, for each Go file path read from stdin (one per line), the
// import paths that file itself declares, using the Go toolchain's own parser
// (go/parser with ImportsOnly). It exists because `go list -json` reports
// imports per package, not per file, and the benchmark's edge rule is per
// file: file F imports package P => F -> every non-test file of P.
//
// Output: one JSON object {"file": "<path>", "imports": ["a/b", ...]} per line.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"strconv"
)

func main() {
	fset := token.NewFileSet()
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 1<<20)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	enc := json.NewEncoder(out)
	for in.Scan() {
		path := in.Text()
		if path == "" {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		rec := map[string]any{"file": path, "imports": []string{}}
		if err != nil {
			rec["error"] = err.Error()
			_ = enc.Encode(rec)
			continue
		}
		imports := make([]string, 0, len(f.Imports))
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			imports = append(imports, p)
		}
		rec["imports"] = imports
		_ = enc.Encode(rec)
	}
	if err := in.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
