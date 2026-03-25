package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"codemap/config"
)

// TaskIntent represents a parsed understanding of what the user wants to do.
type TaskIntent struct {
	Category    string              `json:"category"`    // "refactor", "feature", "bugfix", "explore", "test", "docs"
	Confidence  float64             `json:"confidence"`  // 0.0-1.0 confidence in category
	Files       []string            `json:"files"`       // mentioned files
	Subsystems  []string            `json:"subsystems"`  // matched subsystem IDs
	Scope       string              `json:"scope"`       // "single-file", "package", "cross-cutting"
	RiskLevel   string              `json:"risk"`        // "low", "medium", "high"
	Suggestions []ContextSuggestion `json:"suggestions"` // what to read/check next
}

// ContextSuggestion recommends a follow-up action based on code intelligence.
type ContextSuggestion struct {
	Type   string `json:"type"`   // "check-deps", "review-hub", "run-tests", "update-docs", "read-file"
	Target string `json:"target"` // file path or subsystem ID
	Reason string `json:"reason"` // human-readable explanation
}

// intentSignal represents a word/phrase that signals a specific category.
// Multi-word phrases (e.g. "how does") can be given higher weights for greater specificity.
type intentSignal struct {
	Phrase string
	Weight int
}

type categoryDef struct {
	Category string
	Signals  []intentSignal
}

var categoryDefs = []categoryDef{
	{
		Category: "explore",
		Signals: []intentSignal{
			{"how does", 5}, {"where is", 5}, {"what uses", 5}, {"what is", 4},
			{"show me", 4}, {"walk me through", 5}, {"tell me about", 5},
			{"understand", 4}, {"trace", 3}, {"find", 2},
		},
	},
	{
		Category: "refactor",
		Signals: []intentSignal{
			{"refactor", 5}, {"rename", 4}, {"restructure", 5}, {"reorganize", 4},
			{"clean up", 4}, {"cleanup", 4}, {"consolidate", 4},
			{"extract", 3}, {"split", 3}, {"dedup", 4}, {"deduplicate", 4}, {"simplify", 3},
			{"move", 2},
		},
	},
	{
		Category: "bugfix",
		Signals: []intentSignal{
			{"not working", 5}, {"doesn't work", 5}, {"regression", 5},
			{"fix", 4}, {"bug", 4}, {"broken", 4}, {"crash", 4},
			{"debug", 3}, {"failing", 3}, {"error", 2}, {"wrong", 2}, {"issue", 2},
		},
	},
	{
		Category: "test",
		Signals: []intentSignal{
			{"test coverage", 5}, {"coverage", 4}, {"benchmark", 4},
			{"test", 3}, {"spec", 3}, {"assert", 3},
		},
	},
	{
		Category: "docs",
		Signals: []intentSignal{
			{"document", 5}, {"readme", 5}, {"changelog", 5}, {"release notes", 5},
			{"jsdoc", 4}, {"godoc", 4}, {"docstring", 4},
		},
	},
	{
		Category: "feature",
		Signals: []intentSignal{
			{"implement", 4}, {"integrate", 4}, {"introduce", 4},
			{"add", 3}, {"create", 3}, {"build", 3}, {"new", 2},
			{"support", 2}, {"enable", 2},
		},
	},
}

// classifyIntent analyzes a user prompt against code intelligence to determine task intent.
func classifyIntent(prompt string, files []string, info *hubInfo, cfg config.ProjectConfig) TaskIntent {
	intent := TaskIntent{
		Category:  "feature", // default
		Files:     files,
		RiskLevel: "low",
		Scope:     "single-file",
	}

	// Score each category using weighted signals
	promptLower := strings.ToLower(prompt)
	scores := make(map[string]int)
	for _, cd := range categoryDefs {
		for _, sig := range cd.Signals {
			if strings.Contains(promptLower, sig.Phrase) {
				scores[cd.Category] += sig.Weight
			}
		}
	}

	// Find highest scoring category (deterministic: on tie, use categoryDefs order)
	bestScore := 0
	totalScore := 0
	for _, score := range scores {
		totalScore += score
	}
	// Iterate in definition order for deterministic tie-breaking
	for _, cd := range categoryDefs {
		score := scores[cd.Category]
		if score > bestScore {
			bestScore = score
			intent.Category = cd.Category
		}
	}

	// Confidence: ratio of best score to total (1.0 if only one category matched)
	if totalScore > 0 {
		intent.Confidence = float64(bestScore) / float64(totalScore)
	}

	// Compute scope from file distribution
	intent.Scope = computeScope(files)

	// Match subsystems
	if len(cfg.Routing.Subsystems) > 0 {
		matches := matchSubsystemRoutes(prompt, cfg, cfg.RoutingTopKOrDefault())
		for _, m := range matches {
			intent.Subsystems = append(intent.Subsystems, m.ID)
		}
	}

	// Compute risk level and generate suggestions from hub analysis
	if info != nil {
		intent.RiskLevel, intent.Suggestions = analyzeRisk(files, info, intent.Category)
	}

	return intent
}

// computeScope determines whether the task touches one file, one package, or crosses packages.
func computeScope(files []string) string {
	if len(files) == 0 {
		return "unknown"
	}
	if len(files) == 1 {
		return "single-file"
	}

	packages := make(map[string]struct{})
	for _, f := range files {
		packages[filepath.Dir(f)] = struct{}{}
	}

	if len(packages) <= 1 {
		return "package"
	}
	return "cross-cutting"
}

// analyzeRisk evaluates hub involvement and generates context suggestions.
func analyzeRisk(files []string, info *hubInfo, category string) (string, []ContextSuggestion) {
	risk := "low"
	var suggestions []ContextSuggestion

	hubCount := 0
	maxImporters := 0

	for _, file := range files {
		importers := info.Importers[file]
		importerCount := len(importers)

		if importerCount > maxImporters {
			maxImporters = importerCount
		}

		if importerCount >= 3 {
			hubCount++
			suggestions = append(suggestions, ContextSuggestion{
				Type:   "review-hub",
				Target: file,
				Reason: formatImporterReason(file, importerCount),
			})
			// For refactors and features on hubs, suggest checking deps
			if category == "refactor" || category == "feature" {
				suggestions = append(suggestions, ContextSuggestion{
					Type:   "check-deps",
					Target: file,
					Reason: "verify dependents still compile after changes",
				})
			}
		}
	}

	// Escalate risk based on hub involvement
	if hubCount > 0 {
		risk = "medium"
	}
	if hubCount >= 2 || maxImporters >= 8 {
		risk = "high"
	}

	// Category-specific suggestions
	switch category {
	case "bugfix":
		if len(files) > 0 {
			suggestions = append(suggestions, ContextSuggestion{
				Type:   "run-tests",
				Target: filepath.Dir(files[0]),
				Reason: "verify fix with existing tests",
			})
		}
	case "refactor":
		if len(files) > 0 {
			suggestions = append(suggestions, ContextSuggestion{
				Type:   "run-tests",
				Target: ".",
				Reason: "run full test suite after refactoring",
			})
		}
	}

	// Cap suggestions to avoid noise
	if len(suggestions) > 5 {
		suggestions = suggestions[:5]
	}

	return risk, suggestions
}

func formatImporterReason(file string, count int) string {
	return fmt.Sprintf("%s is a hub file imported by %d files — changes have wide impact", file, count)
}
