package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"codemap/limits"
)

// ProjectConfig holds per-project defaults from .codemap/config.json.
// All fields are optional; zero values mean "no preference".
type ProjectConfig struct {
	Only    []string      `json:"only,omitempty"`
	Exclude []string      `json:"exclude,omitempty"`
	Depth   int           `json:"depth,omitempty"`
	Mode    string        `json:"mode,omitempty"`
	Budgets HookBudgets   `json:"budgets,omitempty"`
	Routing RoutingConfig `json:"routing,omitempty"`
	Drift   DriftConfig   `json:"drift,omitempty"`
}

// HookBudgets configures per-hook output constraints.
// Values are clamped by safe defaults to avoid context blowups.
type HookBudgets struct {
	SessionStartBytes int `json:"session_start_bytes,omitempty"`
	DiffBytes         int `json:"diff_bytes,omitempty"`
	MaxHubs           int `json:"max_hubs,omitempty"`
}

// RoutingConfig controls prompt-submit retrieval hints.
type RoutingConfig struct {
	Retrieval  RetrievalConfig `json:"retrieval,omitempty"`
	Subsystems []Subsystem     `json:"subsystems,omitempty"`
}

// RetrievalConfig sets prompt-submit retrieval behavior.
type RetrievalConfig struct {
	Strategy string `json:"strategy,omitempty"`
	TopK     int    `json:"top_k,omitempty"`
}

// Subsystem is a lightweight task-routing entry used by prompt-submit hooks.
type Subsystem struct {
	ID           string   `json:"id,omitempty"`
	Paths        []string `json:"paths,omitempty"`
	Keywords     []string `json:"keywords,omitempty"`
	Docs         []string `json:"docs,omitempty"`
	Agents       []string `json:"agents,omitempty"`
	Instructions string   `json:"instructions,omitempty"` // markdown instructions injected when this subsystem matches
}

// DriftConfig stores optional doc drift policy metadata.
// The current hooks only read this for display/forward compatibility.
type DriftConfig struct {
	Enabled        bool     `json:"enabled,omitempty"`
	RecentCommits  int      `json:"recent_commits,omitempty"`
	RequireDocsFor []string `json:"require_docs_for,omitempty"`
}

const (
	defaultMode            = "auto"
	defaultRoutingStrategy = "keyword"
	defaultRoutingTopK     = 3
	defaultMaxHubs         = 10
	maxMaxHubs             = 100
)

type SetupState string

const (
	SetupStateReady       SetupState = "ready"
	SetupStateMissing     SetupState = "missing"
	SetupStateMalformed   SetupState = "malformed"
	SetupStateEmpty       SetupState = "empty"
	SetupStateBoilerplate SetupState = "boilerplate"
)

// SetupAssessment describes whether a repo's config needs setup attention.
type SetupAssessment struct {
	State   SetupState `json:"state"`
	Reasons []string   `json:"reasons,omitempty"`
}

// NeedsAttention reports whether the config should be initialized or tuned.
func (a SetupAssessment) NeedsAttention() bool {
	return a.State != SetupStateReady
}

func clampBudget(v, def, max int) int {
	if v <= 0 {
		return def
	}
	if max > 0 && v > max {
		return max
	}
	return v
}

func clampRange(v, def, min, max int) int {
	if v <= 0 {
		return def
	}
	if v < min {
		return min
	}
	if max > 0 && v > max {
		return max
	}
	return v
}

// IsZero reports whether the config has no active project policy.
func (c ProjectConfig) IsZero() bool {
	if len(c.Only) > 0 || len(c.Exclude) > 0 || c.Depth > 0 {
		return false
	}
	if strings.TrimSpace(c.Mode) != "" {
		return false
	}
	if c.Budgets.SessionStartBytes > 0 || c.Budgets.DiffBytes > 0 || c.Budgets.MaxHubs > 0 {
		return false
	}
	if strings.TrimSpace(c.Routing.Retrieval.Strategy) != "" || c.Routing.Retrieval.TopK > 0 || len(c.Routing.Subsystems) > 0 {
		return false
	}
	if c.Drift.Enabled || c.Drift.RecentCommits > 0 || len(c.Drift.RequireDocsFor) > 0 {
		return false
	}
	return true
}

// LooksBoilerplate reports whether the config resembles a bare bootstrap.
// This intentionally treats extension-only configs as a first draft that
// should usually be tuned with project-specific excludes or policy.
func (c ProjectConfig) LooksBoilerplate() bool {
	if c.IsZero() {
		return false
	}
	if len(c.Exclude) > 0 {
		return false
	}
	if strings.TrimSpace(c.Mode) != "" {
		return false
	}
	if c.Budgets.SessionStartBytes > 0 || c.Budgets.DiffBytes > 0 || c.Budgets.MaxHubs > 0 {
		return false
	}
	if strings.TrimSpace(c.Routing.Retrieval.Strategy) != "" || c.Routing.Retrieval.TopK > 0 || len(c.Routing.Subsystems) > 0 {
		return false
	}
	if c.Drift.Enabled || c.Drift.RecentCommits > 0 || len(c.Drift.RequireDocsFor) > 0 {
		return false
	}
	return len(c.Only) > 0
}

// ModeOrDefault returns a valid hook orchestration mode.
func (c ProjectConfig) ModeOrDefault() string {
	mode := strings.ToLower(strings.TrimSpace(c.Mode))
	switch mode {
	case "auto", "structured", "ad-hoc":
		return mode
	default:
		return defaultMode
	}
}

// SessionStartOutputBytes returns a bounded session-start structure budget.
func (c ProjectConfig) SessionStartOutputBytes() int {
	return clampBudget(c.Budgets.SessionStartBytes, limits.MaxStructureOutputBytes, limits.MaxContextOutputBytes)
}

// DiffOutputBytes returns a bounded diff output budget.
func (c ProjectConfig) DiffOutputBytes() int {
	return clampBudget(c.Budgets.DiffBytes, limits.MaxDiffOutputBytes, limits.MaxContextOutputBytes)
}

// HubDisplayLimit returns how many hubs session-start should print.
func (c ProjectConfig) HubDisplayLimit() int {
	return clampRange(c.Budgets.MaxHubs, defaultMaxHubs, 1, maxMaxHubs)
}

// RoutingStrategyOrDefault returns a supported routing strategy.
func (c ProjectConfig) RoutingStrategyOrDefault() string {
	strategy := strings.ToLower(strings.TrimSpace(c.Routing.Retrieval.Strategy))
	if strategy == defaultRoutingStrategy {
		return strategy
	}
	return defaultRoutingStrategy
}

// RoutingTopKOrDefault returns a bounded top-k value for prompt-submit routing.
func (c ProjectConfig) RoutingTopKOrDefault() int {
	return clampRange(c.Routing.Retrieval.TopK, defaultRoutingTopK, 1, 20)
}

// ConfigPath returns the path to .codemap/config.json for the given root.
func ConfigPath(root string) string {
	return filepath.Join(root, ".codemap", "config.json")
}

// Load reads .codemap/config.json from root.
// Returns zero-value ProjectConfig if the file is missing.
// Logs a warning to stderr and returns zero-value if JSON is malformed.
func Load(root string) ProjectConfig {
	data, err := os.ReadFile(ConfigPath(root))
	if err != nil {
		return ProjectConfig{}
	}

	var cfg ProjectConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: malformed .codemap/config.json: %v\n", err)
		return ProjectConfig{}
	}
	return cfg
}

// AssessSetup inspects .codemap/config.json and reports whether it should be
// initialized or tuned before deeper Codemap analysis.
func AssessSetup(root string) SetupAssessment {
	data, err := os.ReadFile(ConfigPath(root))
	if err != nil {
		if os.IsNotExist(err) {
			return SetupAssessment{
				State:   SetupStateMissing,
				Reasons: []string{"No .codemap/config.json is present yet."},
			}
		}
		return SetupAssessment{
			State:   SetupStateMalformed,
			Reasons: []string{fmt.Sprintf("Codemap could not read the config file: %v", err)},
		}
	}

	if strings.TrimSpace(string(data)) == "" {
		return SetupAssessment{
			State:   SetupStateEmpty,
			Reasons: []string{"The config file is blank and does not shape Codemap output yet."},
		}
	}

	var cfg ProjectConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return SetupAssessment{
			State:   SetupStateMalformed,
			Reasons: []string{"The existing .codemap/config.json is malformed JSON."},
		}
	}

	if cfg.IsZero() {
		return SetupAssessment{
			State:   SetupStateEmpty,
			Reasons: []string{"The config exists but does not shape Codemap output yet."},
		}
	}

	if cfg.LooksBoilerplate() {
		return SetupAssessment{
			State:   SetupStateBoilerplate,
			Reasons: []string{"The config only contains generic bootstrap filters and should be tuned for this repo."},
		}
	}

	return SetupAssessment{State: SetupStateReady}
}
