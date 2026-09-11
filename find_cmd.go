package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"codemap/cmd"
	"codemap/config"
	"codemap/find"
	"codemap/scanner"
)

func runFindSubcommand(args []string, launchDir string) int {
	fs := flag.NewFlagSet("find", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	limit := fs.Int("limit", find.DefaultLimit, "Maximum hits to return")
	jsonMode := fs.Bool("json", false, "Emit a single JSON object")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: codemap find [options] <query>")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Rank files by how well their path and function names match the query,")
		fmt.Fprintln(os.Stderr, "annotated with importer counts so a hit also says how risky it is to edit.")
		fmt.Fprintln(os.Stderr, "Lexical only: the query must share words with the code it is looking for.")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Options:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	query := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if query == "" {
		fmt.Fprintln(os.Stderr, "Error: codemap find needs a query")
		return 2
	}
	if *limit <= 0 {
		fmt.Fprintln(os.Stderr, "Error: --limit must be greater than zero")
		return 2
	}

	absRoot, err := filepath.Abs(launchDir)
	if err == nil {
		absRoot, _, err = cmd.ResolveNearestGitRoot(absRoot)
	}
	if err == nil {
		_, err = cmd.ValidateProjectPath(absRoot)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving project root: %v\n", err)
		return 1
	}

	cfg := config.Load(absRoot)
	report, err := find.Run(context.Background(), absRoot, scanner.Filters{Only: cfg.Only, Exclude: cfg.Exclude}, query, *limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error scanning project: %v\n", err)
		return 1
	}
	if *jsonMode {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
			return 1
		}
		return 0
	}
	find.Render(os.Stdout, report)
	return 0
}
