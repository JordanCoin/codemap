package handoff

import (
	"time"

	"codemap/watch"
)

const (
	SchemaVersion  = 1
	DefaultBaseRef = "main"
	DefaultSince   = 6 * time.Hour
)

// HubSummary captures stable hub metadata for prefix context.
type HubSummary struct {
	Path      string `json:"path"`
	Importers int    `json:"importers"`
}

// FileStub is a lightweight file descriptor for lazy detail loading.
type FileStub struct {
	Path   string `json:"path"`
	Hash   string `json:"hash,omitempty"`
	Size   int64  `json:"size,omitempty"`
	Status string `json:"status,omitempty"`
}

// RiskFile captures high-impact changed files in a handoff.
type RiskFile struct {
	Path      string `json:"path"`
	Importers int    `json:"importers"`
	IsHub     bool   `json:"is_hub"`
	Reason    string `json:"reason"`
}

// EventSummary is a compact event entry for handoff output.
type EventSummary struct {
	Time  time.Time `json:"time"`
	Op    string    `json:"op"`
	Path  string    `json:"path"`
	Delta int       `json:"delta,omitempty"`
	IsHub bool      `json:"is_hub,omitempty"`
}

// PrefixSnapshot contains slow-changing structural context.
type PrefixSnapshot struct {
	FileCount int          `json:"file_count,omitempty"`
	Hubs      []HubSummary `json:"hubs"`
}

// DeltaSnapshot contains fast-changing work-in-progress context.
type DeltaSnapshot struct {
	Changed       []FileStub     `json:"changed"`
	RiskFiles     []RiskFile     `json:"risk_files"`
	RecentEvents  []EventSummary `json:"recent_events"`
	NextSteps     []string       `json:"next_steps"`
	OpenQuestions []string       `json:"open_questions"`
}

// CacheMetrics tracks how much handoff context was reused from the previous artifact.
type CacheMetrics struct {
	PrefixBytes          int     `json:"prefix_bytes"`
	DeltaBytes           int     `json:"delta_bytes"`
	TotalBytes           int     `json:"total_bytes"`
	UnchangedBytes       int     `json:"unchanged_bytes"`
	ReuseRatio           float64 `json:"reuse_ratio"`
	PrefixReused         bool    `json:"prefix_reused"`
	DeltaReused          bool    `json:"delta_reused"`
	PreviousCombinedHash string  `json:"previous_combined_hash,omitempty"`
}

// Artifact is the persisted handoff payload shared between agents.
type Artifact struct {
	SchemaVersion int            `json:"schema_version"`
	GeneratedAt   time.Time      `json:"generated_at"`
	Root          string         `json:"root"`
	Branch        string         `json:"branch"`
	BaseRef       string         `json:"base_ref"`
	Prefix        PrefixSnapshot `json:"prefix"`
	Delta         DeltaSnapshot  `json:"delta"`
	PrefixHash    string         `json:"prefix_hash"`
	DeltaHash     string         `json:"delta_hash"`
	CombinedHash  string         `json:"combined_hash"`
	Metrics       CacheMetrics   `json:"metrics"`

	// Legacy top-level fields preserved for backward compatibility.
	ChangedFiles  []string       `json:"changed_files"`
	RiskFiles     []RiskFile     `json:"risk_files"`
	RecentEvents  []EventSummary `json:"recent_events"`
	NextSteps     []string       `json:"next_steps"`
	OpenQuestions []string       `json:"open_questions"`
}

// FileDetail is loaded lazily from a file stub when deeper context is requested.
type FileDetail struct {
	Path         string         `json:"path"`
	Hash         string         `json:"hash,omitempty"`
	Size         int64          `json:"size,omitempty"`
	Status       string         `json:"status,omitempty"`
	Importers    []string       `json:"importers"`
	Imports      []string       `json:"imports"`
	RecentEvents []EventSummary `json:"recent_events"`
	IsHub        bool           `json:"is_hub"`
}

// BuildOptions controls handoff generation behavior.
type BuildOptions struct {
	BaseRef    string
	Since      time.Duration
	State      *watch.State
	MaxChanged int
	MaxRisk    int
	MaxEvents  int
	MaxHubs    int
	Previous   *Artifact
}
