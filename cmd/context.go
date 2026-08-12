package cmd

import (
	"context"
	"encoding/json"
	"errors"
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
	Root          string        `json:"root"`
	Branch        string        `json:"branch"`
	FileCount     int           `json:"file_count"`
	Languages     []string      `json:"languages"`
	HubCount      *int          `json:"hub_count"`
	TopHubs       []string      `json:"top_hubs,omitempty"`
	GraphEvidence GraphEvidence `json:"graph_evidence"`
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
	if err := fs.Parse(args); errors.Is(err, flag.ErrHelp) {
		return
	} else if err != nil {
		os.Exit(1)
	}

	explicitRoot := fs.NArg() > 0
	if explicitRoot {
		root = fs.Arg(0)
	}

	absRoot, err := filepath.Abs(root)
	if !explicitRoot {
		absRoot, _, err = ResolveNearestGitRoot(root)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	absRoot, err = ValidateProjectPath(absRoot)
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
	return buildContextEnvelopeContext(context.Background(), root, prompt, compact)
}

func buildContextEnvelopeContext(ctx context.Context, root, prompt string, compact bool) ContextEnvelope {
	return buildContextEnvelopeWithDeps(ctx, root, prompt, compact, defaultContextEnvelopeDeps())
}

func buildContextEnvelopeWithDeps(parent context.Context, root, prompt string, compact bool, deps contextEnvelopeDeps) ContextEnvelope {
	timeout := deps.graphTimeout
	if timeout <= 0 {
		timeout = defaultContextGraphTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	projCfg := config.Load(root)
	inputs := loadContextRequestInputs(ctx, root, deps)

	envelope := ContextEnvelope{
		Version:     2,
		GeneratedAt: deps.now(),
		Project:     buildProjectContext(root, inputs),
		Budget:      BudgetInfo{Compact: compact},
	}

	// Intent classification (if prompt provided)
	if prompt != "" {
		topK := projCfg.RoutingTopKOrDefault()
		mentions := extractMentionedFiles(strings.ReplaceAll(prompt, `\`, "/"), topK)
		files := resolveExactConfiguredFiles(mentions, inputs.fileSet)
		intent := classifyIntent(prompt, files, inputs.info, projCfg)
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

	// Working-set activity comes from the daemon, but structural labels must be
	// recomputed from this request's fresh graph evidence.
	if state := inputs.state; inputs.evidence.Status == graphEvidenceAvailable && inputs.info != nil && state != nil && state.WorkingSet != nil {
		ws := state.WorkingSet
		wsCtx := &WorkingSetContext{
			FileCount: ws.Size(),
		}
		for path := range ws.Files {
			if len(inputs.info.Importers[path]) >= 3 {
				wsCtx.HubCount++
			}
		}
		limit := 10
		if compact {
			limit = 3
		}
		for _, wf := range ws.HotFiles(limit) {
			isHub := len(inputs.info.Importers[wf.Path]) >= 3
			wsCtx.TopFiles = append(wsCtx.TopFiles, WorkingFileContext{
				Path:      wf.Path,
				EditCount: wf.EditCount,
				NetDelta:  wf.NetDelta,
				IsHub:     isHub,
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

func buildProjectContext(root string, inputs contextRequestInputs) ProjectContext {
	ctx := ProjectContext{
		Root:          root,
		FileCount:     len(inputs.files),
		GraphEvidence: inputs.evidence,
	}

	// Get branch
	if branch, ok := gitCurrentBranch(root); ok {
		ctx.Branch = branch
	}

	if inputs.evidence.Status == graphEvidenceAvailable {
		count := 0
		if inputs.info != nil {
			count = len(inputs.info.Hubs)
			if count > 5 {
				ctx.TopHubs = append([]string(nil), inputs.info.Hubs[:5]...)
			} else {
				ctx.TopHubs = append([]string(nil), inputs.info.Hubs...)
			}
		}
		ctx.HubCount = &count
	}

	// Detect languages from multiple sources
	langSet := make(map[string]bool)

	for _, file := range inputs.files {
		if lang := scanner.DetectLanguage(file.Path); lang != "" {
			langSet[lang] = true
		}
	}

	if len(langSet) == 0 {
		langSet = detectManifestLanguages(root)
	}

	for lang := range langSet {
		ctx.Languages = append(ctx.Languages, lang)
	}
	sort.Strings(ctx.Languages)

	return ctx
}

func detectManifestLanguages(root string) map[string]bool {
	langs := make(map[string]bool)
	manifests := map[string][]string{
		"go.mod": {"go"}, "package.json": {"javascript"}, "Cargo.toml": {"rust"},
		"pyproject.toml": {"python"}, "Package.swift": {"swift"},
		"build.gradle": {"java"}, "build.gradle.kts": {"kotlin", "java"},
	}
	for file, signalLangs := range manifests {
		if _, err := os.Stat(filepath.Join(root, file)); err == nil {
			for _, lang := range signalLangs {
				langs[lang] = true
			}
		}
	}
	return langs
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

	// Include subdirectory source files.
	gitCache := scanner.NewGitIgnoreCache(root)
	if files, err := scanner.ScanConfiguredFiles(context.Background(), root, gitCache); err == nil {
		for _, f := range files {
			addLang(scanner.DetectLanguage(f.Path))
		}
	}

	return langs
}

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
