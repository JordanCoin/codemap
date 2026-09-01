package watch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"codemap/config"
	"codemap/scanner"
)

// GraphLifecycle describes whether persisted dependency maps are reusable.
type GraphLifecycle string

const (
	GraphLifecycleAvailable   GraphLifecycle = "available"
	GraphLifecycleStale       GraphLifecycle = "stale"
	GraphLifecycleSkippedSize GraphLifecycle = "skipped_size"
	GraphLifecycleFailed      GraphLifecycle = "failed"

	graphLifecycleAvailable   = GraphLifecycleAvailable
	graphLifecycleStale       = GraphLifecycleStale
	graphLifecycleSkippedSize = GraphLifecycleSkippedSize
	graphLifecycleFailed      = GraphLifecycleFailed

	graphBuilderRevision        = "filegraph-v1"
	graphCacheLegacy            = "legacy"
	graphCacheRootMismatch      = "root_mismatch"
	graphCacheFilterMismatch    = "filter_mismatch"
	graphCacheRevisionMismatch  = "revision_mismatch"
	graphCacheInventoryMismatch = "inventory_mismatch"
	graphCacheIncomplete        = "incomplete"
)

// GraphState identifies the project and policies that produced a graph.
type GraphState struct {
	Status               GraphLifecycle `json:"status"`
	ProjectRoot          string         `json:"project_root"`
	FilterFingerprint    string         `json:"filter_fingerprint"`
	InventoryFingerprint string         `json:"inventory_fingerprint"`
	BuilderRevision      string         `json:"builder_revision"`
	CompletedAt          time.Time      `json:"completed_at,omitempty"`
}

// NewGraphState records the provenance of a graph outcome.
func NewGraphState(root string, cfg config.ProjectConfig, status GraphLifecycle, completedAt time.Time, configuredPaths []string) GraphState {
	state := GraphState{
		Status:            status,
		ProjectRoot:       canonicalGraphRoot(root),
		FilterFingerprint: graphFilterFingerprint(cfg),
		BuilderRevision:   graphBuilderRevision,
	}
	if status == graphLifecycleAvailable {
		state.CompletedAt = completedAt
		state.InventoryFingerprint = ConfiguredInventoryFingerprint(configuredPaths)
	}
	return state
}

func newGraphState(root string, cfg config.ProjectConfig, status GraphLifecycle, completedAt time.Time, configuredPaths []string) GraphState {
	return NewGraphState(root, cfg, status, completedAt, configuredPaths)
}

func canonicalGraphRoot(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		return filepath.Clean(root)
	}
	if canonical, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(canonical)
	}
	return filepath.Clean(abs)
}

func graphFilterFingerprint(cfg config.ProjectConfig) string {
	only := append([]string(nil), cfg.Only...)
	exclude := append([]string(nil), cfg.Exclude...)
	sort.Strings(only)
	sort.Strings(exclude)
	payload, _ := json.Marshal(struct {
		Only    []string `json:"only"`
		Exclude []string `json:"exclude"`
	}{Only: only, Exclude: exclude})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// ConfiguredInventoryFingerprint identifies an exact configured path set.
func ConfiguredInventoryFingerprint(paths []string) string {
	normalized := normalizedConfiguredInventory(paths)
	payload, _ := json.Marshal(normalized)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func normalizedConfiguredInventory(paths []string) []string {
	set := make(map[string]struct{}, len(paths))
	for _, file := range paths {
		file = strings.TrimSpace(strings.ReplaceAll(file, `\`, "/"))
		file = strings.TrimPrefix(pathpkg.Clean(file), "./")
		if file != "" && file != "." {
			set[file] = struct{}{}
		}
	}
	normalized := make([]string, 0, len(set))
	for file := range set {
		normalized = append(normalized, file)
	}
	sort.Strings(normalized)
	return normalized
}

// ValidateCachedGraph returns a graph only when all producing invariants still
// match the current project.
func ValidateCachedGraph(state *State, root string, cfg config.ProjectConfig) (*scanner.FileGraph, string) {
	if state == nil || state.Graph == nil {
		return nil, graphCacheLegacy
	}
	graphState := state.Graph
	if graphState.Status != graphLifecycleAvailable {
		return nil, string(graphState.Status)
	}
	if graphState.ProjectRoot != canonicalGraphRoot(root) {
		return nil, graphCacheRootMismatch
	}
	if graphState.FilterFingerprint != graphFilterFingerprint(cfg) {
		return nil, graphCacheFilterMismatch
	}
	if graphState.BuilderRevision != graphBuilderRevision {
		return nil, graphCacheRevisionMismatch
	}
	if graphState.InventoryFingerprint == "" {
		return nil, graphCacheIncomplete
	}
	configuredCount, known := state.ConfiguredCount()
	if !known || (configuredCount > 0 && len(state.Imports) == 0 && len(state.Importers) == 0) {
		return nil, graphCacheIncomplete
	}
	return &scanner.FileGraph{
		Imports:   state.Imports,
		Importers: state.Importers,
		Coverage:  state.Coverage,
	}, ""
}

// ValidateCachedGraphForInventory also checks that current configured paths
// match the cached graph.
func ValidateCachedGraphForInventory(state *State, root string, cfg config.ProjectConfig, configuredPaths []string) (*scanner.FileGraph, string) {
	graph, reason := ValidateCachedGraph(state, root, cfg)
	if graph == nil {
		return nil, reason
	}
	configuredCount, _ := state.ConfiguredCount()
	if configuredCount != len(normalizedConfiguredInventory(configuredPaths)) || state.Graph.InventoryFingerprint != ConfiguredInventoryFingerprint(configuredPaths) {
		return nil, graphCacheInventoryMismatch
	}
	return graph, ""
}

func validateCachedGraph(state *State, root string, cfg config.ProjectConfig) (*scanner.FileGraph, string) {
	return ValidateCachedGraph(state, root, cfg)
}
