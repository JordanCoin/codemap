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

// Artifact is the persisted handoff payload shared between agents.
type Artifact struct {
	SchemaVersion int            `json:"schema_version"`
	GeneratedAt   time.Time      `json:"generated_at"`
	Root          string         `json:"root"`
	Branch        string         `json:"branch"`
	BaseRef       string         `json:"base_ref"`
	ChangedFiles  []string       `json:"changed_files"`
	RiskFiles     []RiskFile     `json:"risk_files"`
	RecentEvents  []EventSummary `json:"recent_events"`
	NextSteps     []string       `json:"next_steps"`
	OpenQuestions []string       `json:"open_questions"`
}

// BuildOptions controls handoff generation behavior.
type BuildOptions struct {
	BaseRef    string
	Since      time.Duration
	State      *watch.State
	MaxChanged int
	MaxRisk    int
	MaxEvents  int
}
