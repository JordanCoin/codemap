package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"codemap/config"
	"codemap/internal/gitexclude"
	"codemap/scanner"
)

var errConfigExists = errors.New("config already exists")

type configInitResult struct {
	Path         string
	TopExts      []string
	Exclude      []string
	TotalFiles   int
	MatchedFiles int
}

// noiseDirs are directories that are never project source and that the
// scanner does not already skip (Pods, build, dist, vendor, node_modules,
// target, DerivedData, testdata live in scanner.IgnoredDirs). Init excludes
// one only when it exists at the root or one level down, so a config never
// carries excludes for noise the repo does not have.
var noiseDirs = []string{"Carthage", "coverage", "fixtures", "__snapshots__", ".xcassets"}

// noiseExts are binary assets excluded when a repo carries them in volume.
var noiseExts = []string{"png", "jpg", "gif", "pdf", "otf", "ttf", "mp3", "mp4", "zip"}

const noiseExtThreshold = 10

// detectNoise returns exclude patterns for noise that is present in files.
func detectNoise(files []scanner.FileInfo) []string {
	dirs := make(map[string]bool)
	extCount := make(map[string]int)
	for _, f := range files {
		parts := strings.Split(filepath.ToSlash(f.Path), "/")
		for i := 0; i < len(parts)-1 && i < 2; i++ {
			for _, noise := range noiseDirs {
				if parts[i] == noise || (strings.HasPrefix(noise, ".") && strings.HasSuffix(parts[i], noise)) {
					dirs[noise] = true
				}
			}
		}
		extCount[strings.TrimPrefix(strings.ToLower(f.Ext), ".")]++
	}
	var out []string
	for _, noise := range noiseDirs {
		if dirs[noise] {
			out = append(out, noise)
		}
	}
	for _, ext := range noiseExts {
		if extCount[ext] >= noiseExtThreshold {
			out = append(out, "."+ext)
		}
	}
	return out
}

// EnsureProjectConfig writes an auto-detected .codemap/config.json when none
// exists and root is a git checkout. Every analysis entry point calls this so
// a repo never stays in the "missing" state past its first codemap command.
// Write failures (read-only checkout) are silent: the caller still runs.
func EnsureProjectConfig(root string) bool {
	if _, err := os.Stat(config.ConfigPath(root)); err == nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return false
	}
	result, err := initProjectConfig(root)
	if err != nil {
		return false
	}
	// The auto-write just created .codemap/ in a checkout that never asked
	// for it, so keep it out of `git status` (issue #184): the rule goes to
	// the clone-local info/exclude, never a tracked .gitignore, and only
	// when no existing rule already ignores the directory.
	excludeFile, _ := gitexclude.EnsureIgnoredIfNeeded(filepath.Dir(filepath.Dir(result.Path)))
	// Only a person at a terminal needs the notice; piped consumers that
	// merge stderr into a JSON stream must not see it.
	if info, err := os.Stderr.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
		fmt.Fprintln(os.Stderr, "codemap: wrote .codemap/config.json (auto-detected); tune with: codemap skill show config-setup")
		if excludeFile != "" {
			fmt.Fprintf(os.Stderr, "codemap: added %s to %s so git status stays clean; verify with: git check-ignore -v .codemap\n", gitexclude.Entry, excludeFile)
		}
	}
	return true
}

// nonCodeExtensions are extensions excluded from "config init" auto-detection.
// These are documentation, data, or lock files that rarely represent the
// project's primary code.
var nonCodeExtensions = map[string]bool{
	"md": true, "txt": true, "json": true, "lock": true,
	"sum": true, "mod": true, "csv": true, "xml": true,
	"svg": true, "png": true, "jpg": true, "jpeg": true,
	"gif": true, "ico": true, "woff": true, "woff2": true,
	"ttf": true, "eot": true, "map": true, "license": true,
	// Runtime and build residue: never worth an `only` slot even when a repo
	// has many of them (codemap's own repo once auto-detected "log").
	"log": true, "jsonl": true, "pid": true, "tmp": true,
	"bak": true, "out": true, "cache": true, "swp": true,
	"gitignore": true, "gitattributes": true, "editorconfig": true,
	// Binary assets: an `only` slot for these contradicts the noise excludes.
	"otf": true, "pdf": true, "mp3": true, "mp4": true, "zip": true,
}

// RunConfig dispatches the "config" subcommand.
func RunConfig(subCmd, root string) {
	absRoot, _, err := ResolveNearestGitRoot(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	absRoot, err = ValidateProjectPath(absRoot)
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
	result, err := initProjectConfig(root)
	if errors.Is(err, errConfigExists) {
		cfgPath := config.ConfigPath(root)
		fmt.Fprintf(os.Stderr, "Config already exists: %s\n", cfgPath)
		fmt.Fprintln(os.Stderr, "Use 'codemap config show' to view it, or edit directly.")
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Created %s\n", result.Path)
	reportEnsureIgnored(os.Stdout, root)
	fmt.Println()
	if len(result.TopExts) == 0 {
		fmt.Println("No code extensions detected — wrote empty config.")
	} else {
		fmt.Printf("  only: %s\n", strings.Join(result.TopExts, ", "))
		if len(result.Exclude) > 0 {
			fmt.Printf("  exclude: %s\n", strings.Join(result.Exclude, ", "))
		}
		if result.TotalFiles > 0 {
			fmt.Printf("  (%d of %d files)\n", result.MatchedFiles, result.TotalFiles)
		}
	}
	fmt.Println()
	fmt.Println("Edit the file to add 'exclude' patterns or adjust 'depth'.")
}

func initProjectConfig(root string) (configInitResult, error) {
	cfgPath := config.ConfigPath(root)
	result := configInitResult{Path: cfgPath}

	if _, err := os.Stat(cfgPath); err == nil {
		return result, errConfigExists
	} else if err != nil && !os.IsNotExist(err) {
		return result, err
	}

	gitCache := scanner.NewGitIgnoreCache(root)
	files, err := scanner.ScanFiles(context.Background(), root, gitCache, nil, nil)
	if err != nil {
		return result, fmt.Errorf("scan files: %w", err)
	}

	extCount := make(map[string]int)
	for _, f := range files {
		ext := strings.TrimPrefix(strings.ToLower(f.Ext), ".")
		if ext == "" || nonCodeExtensions[ext] {
			continue
		}
		extCount[ext]++
	}

	type extEntry struct {
		Ext   string
		Count int
	}
	var entries []extEntry
	for ext, count := range extCount {
		entries = append(entries, extEntry{Ext: ext, Count: count})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Count > entries[j].Count
	})

	for i, e := range entries {
		if i >= 5 {
			break
		}
		result.TopExts = append(result.TopExts, e.Ext)
	}

	// Mode is written explicitly: an init that looked for noise is a
	// decision, not a bootstrap, so the config assesses as ready.
	result.Exclude = detectNoise(files)
	cfg := config.ProjectConfig{Only: result.TopExts, Exclude: result.Exclude, Mode: "auto"}

	if err := os.MkdirAll(filepath.Dir(cfgPath), 0755); err != nil {
		return result, fmt.Errorf("create .codemap directory: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return result, fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(cfgPath, data, 0644); err != nil {
		return result, fmt.Errorf("write config: %w", err)
	}

	result.TotalFiles = len(files)
	if len(result.TopExts) > 0 {
		matchExts := make(map[string]bool, len(result.TopExts))
		for _, ext := range result.TopExts {
			matchExts[ext] = true
		}
		for _, f := range files {
			ext := strings.TrimPrefix(strings.ToLower(f.Ext), ".")
			if matchExts[ext] {
				result.MatchedFiles++
			}
		}
	}

	return result, nil
}

func configShow(root string) {
	assessment := config.AssessSetup(root)
	cfgPath := config.ConfigPath(root)
	switch assessment.State {
	case config.SetupStateMissing:
		fmt.Println("No config file found.")
		fmt.Printf("Run 'codemap config init' to create %s\n", cfgPath)
		return
	case config.SetupStateEmpty:
		fmt.Println("Config is empty (no filters active).")
		if len(assessment.Reasons) > 0 {
			fmt.Println(assessment.Reasons[0])
		}
		return
	case config.SetupStateMalformed:
		fmt.Println("Config is malformed or unreadable.")
		if len(assessment.Reasons) > 0 {
			fmt.Println(assessment.Reasons[0])
		}
		fmt.Printf("Fix %s or rerun 'codemap config init' to recreate it.\n", cfgPath)
		return
	}

	cfg := config.Load(root)
	fmt.Printf("Config: %s\n", cfgPath)
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

	if assessment.State == config.SetupStateBoilerplate {
		fmt.Println()
		fmt.Println("Note: this config still looks like a bootstrap.")
		fmt.Println("Run `codemap skill show config-setup` to tune it for this repo.")
	}
}

func isConfigEmpty(cfg config.ProjectConfig) bool {
	return cfg.IsZero()
}
