package watch

import (
	"sort"
	"time"
)

// WorkingSet tracks files actively being edited in the current session.
type WorkingSet struct {
	Files     map[string]*WorkingFile `json:"files"`
	StartedAt time.Time              `json:"started_at"`
}

// WorkingFile tracks per-file editing activity.
type WorkingFile struct {
	Path       string    `json:"path"`
	FirstTouch time.Time `json:"first_touch"`
	LastTouch  time.Time `json:"last_touch"`
	EditCount  int       `json:"edit_count"`
	NetDelta   int       `json:"net_delta"`
	IsHub      bool      `json:"is_hub"`
	Importers  int       `json:"importers"`
}

// NewWorkingSet creates an empty working set.
func NewWorkingSet() *WorkingSet {
	return &WorkingSet{
		Files:     make(map[string]*WorkingFile),
		StartedAt: time.Now(),
	}
}

// Touch records an edit to a file. Subsequent calls update the existing entry.
func (ws *WorkingSet) Touch(path string, delta int, isHub bool, importers int) {
	now := time.Now()
	if wf, exists := ws.Files[path]; exists {
		wf.LastTouch = now
		wf.EditCount++
		wf.NetDelta += delta
		wf.IsHub = isHub
		wf.Importers = importers
	} else {
		ws.Files[path] = &WorkingFile{
			Path:       path,
			FirstTouch: now,
			LastTouch:  now,
			EditCount:  1,
			NetDelta:   delta,
			IsHub:      isHub,
			Importers:  importers,
		}
	}
}

// Remove removes a file from the working set (e.g., on REMOVE/RENAME).
func (ws *WorkingSet) Remove(path string) {
	delete(ws.Files, path)
}

// ActiveFiles returns files edited since the given duration ago, sorted by last touch (newest first).
func (ws *WorkingSet) ActiveFiles(since time.Duration) []WorkingFile {
	cutoff := time.Now().Add(-since)
	var active []WorkingFile
	for _, wf := range ws.Files {
		if wf.LastTouch.After(cutoff) {
			active = append(active, *wf)
		}
	}
	sort.Slice(active, func(i, j int) bool {
		return active[i].LastTouch.After(active[j].LastTouch)
	})
	return active
}

// HotFiles returns the top N most-edited files, sorted by edit count descending.
func (ws *WorkingSet) HotFiles(topN int) []WorkingFile {
	if len(ws.Files) == 0 {
		return nil
	}

	all := make([]WorkingFile, 0, len(ws.Files))
	for _, wf := range ws.Files {
		all = append(all, *wf)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].EditCount != all[j].EditCount {
			return all[i].EditCount > all[j].EditCount
		}
		return all[i].LastTouch.After(all[j].LastTouch)
	})
	if topN > 0 && len(all) > topN {
		all = all[:topN]
	}
	return all
}

// Size returns the number of files in the working set.
func (ws *WorkingSet) Size() int {
	return len(ws.Files)
}

// HubCount returns the number of hub files in the working set.
func (ws *WorkingSet) HubCount() int {
	count := 0
	for _, wf := range ws.Files {
		if wf.IsHub {
			count++
		}
	}
	return count
}
