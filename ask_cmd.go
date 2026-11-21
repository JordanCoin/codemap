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

	"codemap/ask"
	"codemap/cmd"
	"codemap/config"
	"codemap/scanner"
)

func runAskSubcommand(args []string, launchDir string) int {
	fs := flag.NewFlagSet("ask", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	limit := fs.Int("limit", ask.DefaultLimit, "Maximum candidate cards to consider before adaptive cut")
	budget := fs.Int("budget", ask.DefaultBudgetBytes, "Byte budget for the compact answer")
	jsonMode := fs.Bool("json", false, "Emit a single JSON object")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: codemap ask [options] <question>")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "EXPERIMENTAL — bartender-inspired smallest-sufficient context.")
		fmt.Fprintln(os.Stderr, "Builds compact deduplicated summary cards from codemap's map and")
		fmt.Fprintln(os.Stderr, "returns MATCH with the smallest useful set, or explicit NO_MATCH.")
		fmt.Fprintln(os.Stderr, "Does not change default codemap commands. See docs/design/BARTENDER-ASK.md.")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Credit: https://github.com/everoo/bartender (everoo)")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Options:")
		fs.PrintDefaults()
	}
	var words []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			return 2
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		words = append(words, rest[0])
		rest = rest[1:]
	}
	query := strings.TrimSpace(strings.Join(words, " "))
	if query == "" {
		fmt.Fprintln(os.Stderr, "Error: codemap ask needs a question")
		return 2
	}
	if *limit <= 0 {
		fmt.Fprintln(os.Stderr, "Error: --limit must be greater than zero")
		return 2
	}
	if *budget <= 0 {
		fmt.Fprintln(os.Stderr, "Error: --budget must be greater than zero")
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

	fmt.Fprintln(os.Stderr, "EXPERIMENTAL: codemap ask (bartender prototype). Default commands are unchanged.")

	cmd.EnsureAutoConfig(absRoot)
	cfg := config.Load(absRoot)
	answer, err := ask.Run(context.Background(), absRoot, scanner.Filters{Only: cfg.Only, Exclude: cfg.Exclude, MaxFiles: cfg.MaxFiles}, query, ask.Options{
		Limit:       *limit,
		BudgetBytes: *budget,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error answering question: %v\n", err)
		return 1
	}
	if *jsonMode {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(answer); err != nil {
			fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
			return 1
		}
		return 0
	}
	ask.Render(os.Stdout, answer)
	return 0
}
