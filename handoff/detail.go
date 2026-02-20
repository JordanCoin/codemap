package handoff

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"codemap/limits"
	"codemap/scanner"
	"codemap/watch"
)

// BuildFileDetail resolves detailed context for one changed file stub.
func BuildFileDetail(root string, artifact *Artifact, targetPath string, state *watch.State) (*FileDetail, error) {
	if artifact == nil {
		return nil, fmt.Errorf("handoff artifact is nil")
	}
	normalizeArtifact(artifact)

	target := filepath.ToSlash(strings.TrimSpace(targetPath))
	if target == "" {
		return nil, fmt.Errorf("file path is required")
	}

	var selected *FileStub
	for i := range artifact.Delta.Changed {
		if artifact.Delta.Changed[i].Path == target {
			selected = &artifact.Delta.Changed[i]
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("file %q was not found in current handoff delta", target)
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if state == nil {
		state = watch.ReadState(absRoot)
	}

	importers, imports := dependencyContextForFile(absRoot, state, target)
	importers = uniqueSorted(importers)
	imports = uniqueSorted(imports)

	events := make([]EventSummary, 0, len(artifact.Delta.RecentEvents))
	for _, event := range artifact.Delta.RecentEvents {
		if event.Path == target {
			events = append(events, event)
		}
	}

	return &FileDetail{
		Path:         selected.Path,
		Hash:         selected.Hash,
		Size:         selected.Size,
		Status:       selected.Status,
		Importers:    importers,
		Imports:      imports,
		RecentEvents: events,
		IsHub:        len(importers) >= 3,
	}, nil
}

func dependencyContextForFile(root string, state *watch.State, path string) ([]string, []string) {
	if state != nil && (len(state.Importers) > 0 || len(state.Imports) > 0) {
		return append([]string{}, state.Importers[path]...), append([]string{}, state.Imports[path]...)
	}

	if state != nil && state.FileCount > limits.LargeRepoFileCount {
		return nil, nil
	}

	fg, err := scanner.BuildFileGraph(root)
	if err != nil {
		return nil, nil
	}
	return append([]string{}, fg.Importers[path]...), append([]string{}, fg.Imports[path]...)
}

func uniqueSorted(items []string) []string {
	if len(items) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item == "" {
			continue
		}
		seen[item] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for item := range seen {
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}
