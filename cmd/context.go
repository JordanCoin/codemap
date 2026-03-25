package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"codemap/config"
	"codemap/handoff"
	"codemap/scanner"
	"codemap/skills"
	"codemap/watch"
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
	if state := watch.ReadState(root); state != nil && state.WorkingSet != nil {
		ws := state.WorkingSet
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
	if state := watch.ReadState(root); state != nil {
		ctx.FileCount = state.FileCount
		ctx.HubCount = len(state.Hubs)
		if len(state.Hubs) > 5 {
			ctx.TopHubs = state.Hubs[:5]
		} else {
			ctx.TopHubs = state.Hubs
		}
	}

	// Detect languages from all known files (importers + imports + working set)
	langSet := make(map[string]bool)
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
	// Also check hubs for language detection (covers repos with no importers map)
	for _, hub := range ctx.TopHubs {
		if lang := scanner.DetectLanguage(hub); lang != "" {
			langSet[lang] = true
		}
	}
	for lang := range langSet {
		ctx.Languages = append(ctx.Languages, lang)
	}
	sort.Strings(ctx.Languages)

	return ctx
}
