package watch

import (
	"sync"
	"time"

	"codemap/scanner"
)

// Event represents a file change event with timestamp and structural context
type Event struct {
	Time      time.Time `json:"time"`
	Op        string    `json:"op"`             // CREATE, WRITE, REMOVE, RENAME
	Path      string    `json:"path"`           // relative path
	Language  string    `json:"lang,omitempty"` // go, py, js, etc.
	Lines     int       `json:"lines,omitempty"`
	Delta     int       `json:"delta,omitempty"` // line count change (+/-)
	SizeDelta int64     `json:"size_delta,omitempty"`
	Dirty     bool      `json:"dirty,omitempty"` // uncommitted changes
	// Structural context from deps
	Importers  int      `json:"importers,omitempty"`   // how many files import this
	Imports    int      `json:"imports,omitempty"`     // how many files this imports
	IsHub      bool     `json:"is_hub,omitempty"`      // importers >= 3
	RelatedHot []string `json:"related_hot,omitempty"` // connected files also edited recently
}

// FileState tracks lightweight per-file state for delta calculations
type FileState struct {
	Lines int
	Size  int64
}

// DepContext holds pre-computed dependency context for a file
type DepContext struct {
	Imports   []string // files this file imports
	Importers []string // files that import this file
}

// Graph holds the live code graph state
type Graph struct {
	mu        sync.RWMutex
	Root      string
	Files     map[string]*scanner.FileInfo // path -> file info
	FileGraph *scanner.FileGraph           // internal file-to-file dependencies
	DepCtx    map[string]*DepContext       // path -> dependency context (precomputed)
	State     map[string]*FileState        // path -> line/size cache for deltas
	Events    []Event
	LastScan  time.Time
	IsGitRepo bool
	HasDeps   bool // whether deps were successfully computed
}

// State represents the daemon state that hooks can read
type State struct {
	UpdatedAt    time.Time           `json:"updated_at"`
	FileCount    int                 `json:"file_count"`
	Hubs         []string            `json:"hubs"`
	Importers    map[string][]string `json:"importers"`     // file -> files that import it
	Imports      map[string][]string `json:"imports"`       // file -> files it imports
	RecentEvents []Event             `json:"recent_events"` // last 50 events for timeline
}
