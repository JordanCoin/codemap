package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"codemap/config"
	"codemap/handoff"
	"codemap/scanner"
	"codemap/skills"
)

// ContextEnvelope is the standardized output format that any AI tool can consume.
type ContextEnvelope struct {
	Version     int                `json:"version"`
	GeneratedAt time.Time          `json:"generated_at"`
	Project     ProjectContext     `json:"project"`
	Intent      *TaskIntent        `json:"intent,omitempty"`
	WorkingSet  *WorkingSetContext `json:"working_set,omitempty"`
	Skills      []SkillRef         `json:"skills,omitempty"`
	Handoff     *HandoffRef        `json:"handoff,omitempty"`
	Budget      BudgetInfo         `json:"budget"`
}

// ProjectContext contains high-level project metadata.
type ProjectContext struct {
	Root      string   `json:"root"`
	Branch    string   `json:"branch"`
	FileCount int      `json:"file_count"`
	Languages []string `json:"languages"`
	HubCount  int      `json:"hub_count"`
	TopHubs   []string `json:"top_hubs,omitempty"`
}

// WorkingSetContext is a summary of the current working set.
type WorkingSetContext struct {
	FileCount int                  `json:"file_count"`
	HubCount  int                  `json:"hub_count"`
	TopFiles  []WorkingFileContext `json:"top_files,omitempty"`
}

// WorkingFileContext is a single file in the working set summary.
type WorkingFileContext struct {
	Path      string `json:"path"`
	EditCount int    `json:"edit_count"`
	NetDelta  int    `json:"net_delta"`
	IsHub     bool   `json:"is_hub,omitempty"`
}

// SkillRef is a lightweight reference to a matched skill.
type SkillRef struct {
	Name   string `json:"name"`
	Score  int    `json:"score"`
	Reason string `json:"reason,omitempty"`
}

// HandoffRef points to the latest handoff artifact.
type HandoffRef struct {
	Path         string    `json:"path"`
	GeneratedAt  time.Time `json:"generated_at,omitempty"`
	ChangedFiles int       `json:"changed_files"`
	RiskFiles    int       `json:"risk_files"`
}

// BudgetInfo reports how much context was generated.
type BudgetInfo struct {
	TotalBytes int  `json:"total_bytes"`
	Compact    bool `json:"compact"`
}

// RunContext handles the "codemap context" subcommand.
func RunContext(args []string, root string) {
	fs := flag.NewFlagSet("context", flag.ContinueOnError)
	forPrompt := fs.String("for", "", "Pre-classify intent for this prompt")
	compact := fs.Bool("compact", false, "Minimal output for token-constrained agents")
	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	if fs.NArg() > 0 {
		root = fs.Arg(0)
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	envelope := buildContextEnvelope(absRoot, *forPrompt, *compact)

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(envelope); err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding context: %v\n", err)
		os.Exit(1)
	}
}

func buildContextEnvelope(root, prompt string, compact bool) ContextEnvelope {
	projCfg := config.Load(root)
	info := getHubInfoNoFallback(root)

	envelope := ContextEnvelope{
		Version:     1,
		GeneratedAt: time.Now(),
		Project:     buildProjectContext(root, info),
		Budget:      BudgetInfo{Compact: compact},
	}

	// Intent classification (if prompt provided)
	if prompt != "" {
		topK := projCfg.RoutingTopKOrDefault()
		files := extractMentionedFiles(prompt, topK)
		intent := classifyIntent(prompt, files, info, projCfg)
		envelope.Intent = &intent

		// Match skills against intent
		if !compact {
			if idx, err := skills.LoadSkills(root); err == nil && idx != nil {
				var langs []string
				for _, f := range files {
					if lang := scanner.DetectLanguage(f); lang != "" {
						langs = append(langs, lang)
					}
				}
				matches := idx.MatchSkills(intent.Category, files, langs, 3)
				for _, m := range matches {
					envelope.Skills = append(envelope.Skills, SkillRef{
						Name:   m.Skill.Meta.Name,
						Score:  m.Score,
						Reason: m.Reason,
					})
				}
			}
		}
	}

	// Working set from daemon
	if ws := loadWorkingSet(root); ws != nil {
		wsCtx := &WorkingSetContext{
			FileCount: ws.Size(),
			HubCount:  ws.HubCount(),
		}
		limit := 10
		if compact {
			limit = 3
		}
		for _, wf := range ws.HotFiles(limit) {
			wsCtx.TopFiles = append(wsCtx.TopFiles, WorkingFileContext{
				Path:      wf.Path,
				EditCount: wf.EditCount,
				NetDelta:  wf.NetDelta,
				IsHub:     wf.IsHub,
			})
		}
		envelope.WorkingSet = wsCtx
	}

	// Handoff reference
	if !compact {
		if artifact, err := handoff.ReadLatest(root); err == nil && artifact != nil {
			ref := &HandoffRef{
				Path:        handoff.LatestPath(root),
				GeneratedAt: artifact.GeneratedAt,
			}
			ref.ChangedFiles = len(artifact.Delta.Changed)
			ref.RiskFiles = len(artifact.Delta.RiskFiles)
			envelope.Handoff = ref
		}
	}

	// Calculate budget
	data, _ := json.Marshal(envelope)
	envelope.Budget.TotalBytes = len(data)

	return envelope
}

func buildProjectContext(root string, info *hubInfo) ProjectContext {
	ctx := ProjectContext{
		Root: root,
	}

	// Get branch
	if branch, ok := gitCurrentBranch(root); ok {
		ctx.Branch = branch
	}

	// Count files and detect languages from daemon state
	if stats, ok := loadProjectStats(root); ok {
		ctx.FileCount = stats.FileCount
		ctx.HubCount = len(stats.Hubs)
		if len(stats.Hubs) > 5 {
			ctx.TopHubs = stats.Hubs[:5]
		} else {
			ctx.TopHubs = stats.Hubs
		}
	}

	// Detect languages from multiple sources
	langSet := make(map[string]bool)

	// Source 1: dependency graph (importers + imports)
	if info != nil {
		for file := range info.Importers {
			if lang := scanner.DetectLanguage(file); lang != "" {
				langSet[lang] = true
			}
		}
		for file := range info.Imports {
			if lang := scanner.DetectLanguage(file); lang != "" {
				langSet[lang] = true
			}
		}
	}

	// Source 2: hubs
	for _, hub := range ctx.TopHubs {
		if lang := scanner.DetectLanguage(hub); lang != "" {
			langSet[lang] = true
		}
	}

	// Source 3: cheap fallback — scan for manifest files and top-level source files.
	// This runs when daemon isn't active and dep graph is empty.
	if len(langSet) == 0 {
		langSet = detectLanguagesFromFiles(root)
	}

	for lang := range langSet {
		ctx.Languages = append(ctx.Languages, lang)
	}
	sort.Strings(ctx.Languages)

	// Fallback file count from quick scan if daemon wasn't available
	if ctx.FileCount == 0 && len(ctx.Languages) > 0 {
		ctx.FileCount = countSourceFiles(root)
	}

	return ctx
}

// detectLanguagesFromFiles does a quick scan for language signals.
// Checks manifest files first (fast), then scans source files recursively.
func detectLanguagesFromFiles(root string) map[string]bool {
	langs := make(map[string]bool)
	addLang := func(lang string) {
		if lang != "" {
			langs[lang] = true
		}
	}

	// Manifest files → definitive language signal
	manifests := map[string][]string{
		"go.mod":           {"go"},
		"package.json":     {"javascript"},
		"Cargo.toml":       {"rust"},
		"pyproject.toml":   {"python"},
		"setup.py":         {"python"},
		"requirements.txt": {"python"},
		"Gemfile":          {"ruby"},
		"build.gradle":     {"java"},
		"build.gradle.kts": {"kotlin", "java"},
		"pom.xml":          {"java"},
		"Package.swift":    {"swift"},
		"Podfile":          {"swift"},
		"mix.exs":          {"elixir"},
		"composer.json":    {"php"},
		"build.sbt":        {"scala"},
		"tsconfig.json":    {"typescript"},
	}
	for file, signalLangs := range manifests {
		if _, err := os.Stat(filepath.Join(root, file)); err == nil {
			for _, lang := range signalLangs {
				addLang(lang)
			}
		}
	}

	// C# project files can have arbitrary names; detect by glob at repo root.
	for _, pattern := range []string{"*.csproj", "*.sln"} {
		matches, _ := filepath.Glob(filepath.Join(root, pattern))
		if len(matches) > 0 {
			addLang("csharp")
		}
	}

	// JS/TS monorepo signal: packages/*/package.json.
	if matches, _ := filepath.Glob(filepath.Join(root, "packages", "*", "package.json")); len(matches) > 0 {
		addLang("javascript")
	}

	// Makefile heuristics for C/C++ projects — check directly, no sentinel.
	if _, err := os.Stat(filepath.Join(root, "Makefile")); err == nil {
		applyMakefileHeuristics(filepath.Join(root, "Makefile"), addLang)
	}

	// Include subdirectory source files. Reuse the scan result for countSourceFiles too.
	gitCache := scanner.NewGitIgnoreCache(root)
	if files, err := scanner.ScanFiles(root, gitCache, nil, nil); err == nil {
		for _, f := range files {
			addLang(scanner.DetectLanguage(f.Path))
		}
		// Cache file count to avoid a second scan in countSourceFiles
		cachedFileCount = len(files)
	}

	return langs
}

// cachedFileCount avoids a second ScanFiles walk in countSourceFiles.
var cachedFileCount = -1

func applyMakefileHeuristics(path string, addLang func(string)) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	buf, err := io.ReadAll(io.LimitReader(f, 128*1024))
	if err != nil {
		return
	}
	content := strings.ToLower(string(buf))

	if strings.Contains(content, "g++") || strings.Contains(content, "clang++") || strings.Contains(content, ".cpp") || strings.Contains(content, ".cc") {
		addLang("cpp")
	}
	// Tighten C detection: exclude clang++ and .cpp/.cc false positives
	if strings.Contains(content, "gcc") ||
		(strings.Contains(content, "clang") && !strings.Contains(content, "clang++")) {
		addLang("c")
	}
}

// countSourceFiles does a quick count of source files in the project.
// Uses cached result from detectLanguagesFromFiles if available.
func countSourceFiles(root string) int {
	if cachedFileCount >= 0 {
		count := cachedFileCount
		cachedFileCount = -1 // reset for next call
		return count
	}
	gitCache := scanner.NewGitIgnoreCache(root)
	files, err := scanner.ScanFiles(root, gitCache, nil, nil)
	if err != nil {
		return 0
	}
	return len(files)
}
