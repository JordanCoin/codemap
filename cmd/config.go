package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"codemap/config"
	"codemap/internal/projectpath"
	"codemap/internal/runtimefile"
	"codemap/scanner"
)

var errConfigExists = errors.New("config already exists")

// sharedConfigPrimary reports the primary checkout whose .codemap/ this root
// reads when root is a linked git worktree. Config is deliberately shared
// across worktrees (one policy per repository), but a command run inside the
// worktree must say so and must never write into the other checkout (#152).
func sharedConfigPrimary(root string) (string, bool) {
	selection, err := projectpath.Select(root)
	if err != nil || selection.Source != projectpath.SourceLinkedWorktree {
		return "", false
	}
	return selection.SetupRoot, true
}

func printSharedConfigNote(root string) {
	if primary, ok := sharedConfigPrimary(root); ok {
		fmt.Printf("  (shared from primary checkout %s; %s is a linked worktree of it)\n", primary, root)
	}
}

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

// AutoConfigActive reports whether root, a git checkout, has no
// .codemap/config.json and so runs on the auto-detected config.
func AutoConfigActive(root string) bool {
	if _, err := os.Stat(config.ConfigPath(root)); !os.IsNotExist(err) {
		return false
	}
	_, err := os.Stat(filepath.Join(root, ".git"))
	return err == nil
}

// EnsureAutoConfig stores an auto-detected config in the project's state dir
// (config.AutoConfigPath) when it is missing or unreadable, so a repo that
// never ran setup still gets `only` filters (a few Go files among thousands
// of data files stay under the large-repo cutoff). It scans, so hooks never
// call it; the tree keeps the stored copy current through RefreshAutoConfig.
// `codemap config init` is the explicit, committable path. It never writes
// inside the repo, and failures are silent. It reports whether it wrote.
func EnsureAutoConfig(root string) bool {
	path := config.AutoConfigPath(root)
	if !AutoConfigActive(root) || path == "" {
		return false
	}
	if data, err := runtimefile.Read(path); err == nil && json.Valid(data) {
		return false
	}
	cfg, err := detectProjectConfig(root, &configInitResult{})
	if err != nil {
		return false
	}
	return writeAutoConfig(root, cfg)
}

// writeAutoConfig stores cfg as root's auto config when it differs from the
// stored copy, creating the state dir only now that there is something to
// write.
func writeAutoConfig(root string, cfg config.ProjectConfig) bool {
	dir, err := projectpath.CheckedRuntimeCodemapDir(root)
	if err != nil {
		return false
	}
	path := filepath.Join(dir, config.AutoConfigFile)
	data := encodeConfig(cfg)
	if stored, err := runtimefile.Read(path); err == nil && bytes.Equal(stored, data) {
		return false
	}
	return runtimefile.WriteAtomic(path, data, 0o600) == nil
}

// RefreshAutoConfig keeps root's stored auto config in step with its file
// mix and returns the listing it used. The mix comes from git's file list,
// which is unbounded and far cheaper than a walk, so a repo over the scan
// bound never derives filters from a truncated sample. Without git the stored
// copy stays as it is (EnsureAutoConfig fills a missing one) and the listing
// is nil.
func RefreshAutoConfig(root string) []scanner.FileInfo {
	if !AutoConfigActive(root) {
		return nil
	}
	files, err := gitListedFiles(root)
	if err != nil {
		EnsureAutoConfig(root)
		return nil
	}
	writeAutoConfig(root, configFromFiles(files, &configInitResult{}))
	return files
}

// gitListedFiles is every tracked or unignored untracked file under root,
// minus the directories the scanner never enters.
func gitListedFiles(root string) ([]scanner.FileInfo, error) {
	cmd := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var files []scanner.FileInfo
	for _, path := range strings.Split(string(out), "\x00") {
		if path == "" || inIgnoredDir(path) {
			continue
		}
		files = append(files, scanner.FileInfo{Path: path, Ext: filepath.Ext(path)})
	}
	return files, nil
}

func inIgnoredDir(path string) bool {
	parts := strings.Split(path, "/")
	for _, dir := range parts[:len(parts)-1] {
		if scanner.IgnoredDirs[dir] {
			return true
		}
	}
	return false
}

// AutoFilterNote names the auto filter in one line, so it is never silent,
// but only when it hides at least one of files (a Go-only repo filtered to
// Go hides nothing and gets no note).
func AutoFilterNote(cfg config.ProjectConfig, files []scanner.FileInfo) string {
	hides := false
	for _, f := range files {
		if !scanner.MatchesFilters(f.Path, f.Ext, cfg.Only, cfg.Exclude) {
			hides = true
			break
		}
	}
	if !hides {
		return ""
	}
	note := "auto filter:"
	if len(cfg.Only) > 0 {
		note += " only " + strings.Join(cfg.Only, ",")
	}
	if len(cfg.Exclude) > 0 {
		note += " exclude " + strings.Join(cfg.Exclude, ",")
	}
	return note + "; codemap config show"
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
	if primary, ok := sharedConfigPrimary(root); ok {
		return result, fmt.Errorf("%s is a linked worktree; its config lives in the primary checkout, run `codemap config init` in %s", root, primary)
	}
	cfg, err := detectProjectConfig(root, &result)
	if err != nil {
		return result, err
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0755); err != nil {
		return result, fmt.Errorf("create .codemap directory: %w", err)
	}
	if err := os.WriteFile(cfgPath, encodeConfig(cfg), 0644); err != nil {
		return result, fmt.Errorf("write config: %w", err)
	}
	return result, nil
}

func encodeConfig(cfg config.ProjectConfig) []byte {
	data, _ := json.MarshalIndent(cfg, "", "  ") // a plain struct always encodes
	return append(data, '\n')
}

// scanForConfig is the full scan config detection runs; tests count it.
var scanForConfig = func(root string) ([]scanner.FileInfo, error) {
	return scanner.ScanFiles(context.Background(), root, scanner.NewGitIgnoreCache(root), nil, nil)
}

// detectProjectConfig picks `only` and `exclude` from the files under root
// and fills result's counts; it writes nothing.
func detectProjectConfig(root string, result *configInitResult) (config.ProjectConfig, error) {
	files, err := scanForConfig(root)
	if err != nil {
		return config.ProjectConfig{}, fmt.Errorf("scan files: %w", err)
	}
	return configFromFiles(files, result), nil
}

// configFromFiles derives the auto config from an unfiltered file list.
func configFromFiles(files []scanner.FileInfo, result *configInitResult) config.ProjectConfig {
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
		if entries[i].Count != entries[j].Count {
			return entries[i].Count > entries[j].Count
		}
		return entries[i].Ext < entries[j].Ext
	})

	// The top five by count bound the list, but a source extension the
	// scanner parses is never cut: dropping a minority one (a few .mjs files
	// in a .js repo) silently loses its files and their import edges.
	for i, e := range entries {
		if i < 5 || scanner.IsSourceExt("."+e.Ext) {
			result.TopExts = append(result.TopExts, e.Ext)
		}
	}

	// Mode is written explicitly: an init that looked for noise is a
	// decision, not a bootstrap, so the config assesses as ready.
	result.Exclude = detectNoise(files)
	cfg := config.ProjectConfig{Only: result.TopExts, Exclude: result.Exclude, Mode: "auto"}

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
	return cfg
}

func configShow(root string) {
	assessment := config.AssessSetup(root)
	cfgPath := config.ConfigPath(root)
	switch assessment.State {
	case config.SetupStateMissing:
		fmt.Println("No config file found.")
		if auto := config.AutoConfigPath(root); auto != "" {
			if _, err := os.Stat(auto); err == nil {
				fmt.Printf("Using auto-detected filters from %s (not committed; computed once)\n", auto)
			}
		}
		if primary, ok := sharedConfigPrimary(root); ok {
			fmt.Printf("Run 'codemap config init' in the primary checkout %s to create %s\n", primary, cfgPath)
			return
		}
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
	printSharedConfigNote(root)
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
