package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"codemap/config"
	"codemap/scanner"
)

// nonCodeExtensions are extensions excluded from "config init" auto-detection.
// These are documentation, data, or lock files that rarely represent the
// project's primary code.
var nonCodeExtensions = map[string]bool{
	"md": true, "txt": true, "json": true, "lock": true,
	"sum": true, "mod": true, "csv": true, "xml": true,
	"svg": true, "png": true, "jpg": true, "jpeg": true,
	"gif": true, "ico": true, "woff": true, "woff2": true,
	"ttf": true, "eot": true, "map": true, "license": true,
}

// RunConfig dispatches the "config" subcommand.
func RunConfig(subCmd, root string) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	switch subCmd {
	case "init":
		configInit(absRoot)
	case "show":
		configShow(absRoot)
	default:
		fmt.Fprintln(os.Stderr, "Usage: codemap config <init|show> [path]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "  init    Create .codemap/config.json with auto-detected extensions")
		fmt.Fprintln(os.Stderr, "  show    Display current project config")
		os.Exit(1)
	}
}

func configInit(root string) {
	cfgPath := config.ConfigPath(root)

	// Warn if config already exists
	if _, err := os.Stat(cfgPath); err == nil {
		fmt.Fprintf(os.Stderr, "Config already exists: %s\n", cfgPath)
		fmt.Fprintln(os.Stderr, "Use 'codemap config show' to view it, or edit directly.")
		os.Exit(1)
	}

	// Scan the repo to find top extensions
	gitCache := scanner.NewGitIgnoreCache(root)
	files, err := scanner.ScanFiles(root, gitCache, nil, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error scanning files: %v\n", err)
		os.Exit(1)
	}

	// Count extensions
	extCount := make(map[string]int)
	for _, f := range files {
		ext := strings.TrimPrefix(f.Ext, ".")
		if ext == "" {
			continue
		}
		ext = strings.ToLower(ext)
		if nonCodeExtensions[ext] {
			continue
		}
		extCount[ext]++
	}

	// Sort by frequency
	type extEntry struct {
		Ext   string
		Count int
	}
	var entries []extEntry
	for ext, count := range extCount {
		entries = append(entries, extEntry{ext, count})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Count > entries[j].Count
	})

	// Take top 5
	var topExts []string
	for i, e := range entries {
		if i >= 5 {
			break
		}
		topExts = append(topExts, e.Ext)
	}

	if len(topExts) == 0 {
		fmt.Println("No code extensions detected — writing empty config.")
		topExts = nil
	}

	cfg := config.ProjectConfig{
		Only: topExts,
	}

	// Ensure .codemap/ directory exists
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating directory: %v\n", err)
		os.Exit(1)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding config: %v\n", err)
		os.Exit(1)
	}
	data = append(data, '\n')

	if err := os.WriteFile(cfgPath, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Created %s\n", cfgPath)
	fmt.Println()
	fmt.Printf("  only: %s\n", strings.Join(topExts, ", "))
	if len(files) > 0 && len(topExts) > 0 {
		// Count how many files match the selected extensions
		matchExts := make(map[string]bool)
		for _, ext := range topExts {
			matchExts[ext] = true
		}
		matched := 0
		for _, f := range files {
			ext := strings.TrimPrefix(strings.ToLower(f.Ext), ".")
			if matchExts[ext] {
				matched++
			}
		}
		fmt.Printf("  (%d of %d files)\n", matched, len(files))
	}
	fmt.Println()
	fmt.Println("Edit the file to add 'exclude' patterns or adjust 'depth'.")
}

func configShow(root string) {
	cfg := config.Load(root)
	if isConfigEmpty(cfg) {
		cfgPath := config.ConfigPath(root)
		if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
			fmt.Println("No config file found.")
			fmt.Printf("Run 'codemap config init' to create %s\n", cfgPath)
		} else {
			fmt.Println("Config is empty (no filters active).")
		}
		return
	}

	fmt.Printf("Config: %s\n", config.ConfigPath(root))
	fmt.Println()
	if len(cfg.Only) > 0 {
		fmt.Printf("  only:    %s\n", strings.Join(cfg.Only, ", "))
	}
	if len(cfg.Exclude) > 0 {
		fmt.Printf("  exclude: %s\n", strings.Join(cfg.Exclude, ", "))
	}
	if cfg.Depth > 0 {
		fmt.Printf("  depth:   %d\n", cfg.Depth)
	}
	if strings.TrimSpace(cfg.Mode) != "" {
		fmt.Printf("  mode:    %s\n", cfg.ModeOrDefault())
	}
	if cfg.Budgets.SessionStartBytes > 0 || cfg.Budgets.DiffBytes > 0 || cfg.Budgets.MaxHubs > 0 {
		fmt.Println("  budgets:")
		if cfg.Budgets.SessionStartBytes > 0 {
			fmt.Printf("    session_start_bytes: %d\n", cfg.Budgets.SessionStartBytes)
		}
		if cfg.Budgets.DiffBytes > 0 {
			fmt.Printf("    diff_bytes:          %d\n", cfg.Budgets.DiffBytes)
		}
		if cfg.Budgets.MaxHubs > 0 {
			fmt.Printf("    max_hubs:            %d\n", cfg.Budgets.MaxHubs)
		}
	}
	if strings.TrimSpace(cfg.Routing.Retrieval.Strategy) != "" || cfg.Routing.Retrieval.TopK > 0 || len(cfg.Routing.Subsystems) > 0 {
		fmt.Println("  routing:")
		if strings.TrimSpace(cfg.Routing.Retrieval.Strategy) != "" || cfg.Routing.Retrieval.TopK > 0 {
			fmt.Printf("    retrieval: strategy=%s top_k=%d\n", cfg.RoutingStrategyOrDefault(), cfg.RoutingTopKOrDefault())
		}
		if len(cfg.Routing.Subsystems) > 0 {
			fmt.Printf("    subsystems: %d configured\n", len(cfg.Routing.Subsystems))
			const maxShown = 5
			for i, sub := range cfg.Routing.Subsystems {
				if i >= maxShown {
					fmt.Printf("      ... and %d more\n", len(cfg.Routing.Subsystems)-maxShown)
					break
				}
				label := strings.TrimSpace(sub.ID)
				if label == "" {
					label = fmt.Sprintf("(unnamed-%d)", i+1)
				}
				fmt.Printf("      - %s (keywords=%d docs=%d agents=%d)\n", label, len(sub.Keywords), len(sub.Docs), len(sub.Agents))
			}
		}
	}
	if cfg.Drift.Enabled || cfg.Drift.RecentCommits > 0 || len(cfg.Drift.RequireDocsFor) > 0 {
		fmt.Println("  drift:")
		fmt.Printf("    enabled: %t\n", cfg.Drift.Enabled)
		if cfg.Drift.RecentCommits > 0 {
			fmt.Printf("    recent_commits: %d\n", cfg.Drift.RecentCommits)
		}
		if len(cfg.Drift.RequireDocsFor) > 0 {
			fmt.Printf("    require_docs_for: %s\n", strings.Join(cfg.Drift.RequireDocsFor, ", "))
		}
	}
}

func isConfigEmpty(cfg config.ProjectConfig) bool {
	if len(cfg.Only) > 0 || len(cfg.Exclude) > 0 || cfg.Depth > 0 {
		return false
	}
	if strings.TrimSpace(cfg.Mode) != "" {
		return false
	}
	if cfg.Budgets.SessionStartBytes > 0 || cfg.Budgets.DiffBytes > 0 || cfg.Budgets.MaxHubs > 0 {
		return false
	}
	if strings.TrimSpace(cfg.Routing.Retrieval.Strategy) != "" || cfg.Routing.Retrieval.TopK > 0 || len(cfg.Routing.Subsystems) > 0 {
		return false
	}
	if cfg.Drift.Enabled || cfg.Drift.RecentCommits > 0 || len(cfg.Drift.RequireDocsFor) > 0 {
		return false
	}
	return true
}
