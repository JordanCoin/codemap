package handoff

import (
	"context"
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
	return BuildFileDetailContext(context.Background(), root, artifact, targetPath, state)
}

// BuildFileDetailContext resolves detailed context while honoring caller cancellation.
func BuildFileDetailContext(ctx context.Context, root string, artifact *Artifact, targetPath string, state *watch.State) (*FileDetail, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	importers, imports, err := dependencyContextForFileContext(ctx, absRoot, state, target)
	if err != nil {
		return nil, err
	}
	importers = uniqueSorted(importers)
	imports = uniqueSorted(imports)

	events := make([]EventSummary, 0, len(artifact.Delta.RecentEvents))
	for _, event := range artifact.Delta.RecentEvents {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
	importers, imports, _ := dependencyContextForFileContext(context.Background(), root, state, path)
	return importers, imports
}

func dependencyContextForFileContext(ctx context.Context, root string, state *watch.State, path string) ([]string, []string, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if state != nil && (len(state.Importers) > 0 || len(state.Imports) > 0) {
		return append([]string{}, state.Importers[path]...), append([]string{}, state.Imports[path]...), nil
	}

	fileCount, err := resolveRepoFileCountContext(ctx, root)
	if err != nil {
		return nil, nil, err
	}
	if fileCount > limits.LargeRepoFileCount {
		return nil, nil, nil
	}

	fg, err := scanner.BuildFileGraph(ctx, root, scanner.ConfiguredFilters(root))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, nil, ctxErr
		}
		return nil, nil, nil
	}
	return append([]string{}, fg.Importers[path]...), append([]string{}, fg.Imports[path]...), ctx.Err()
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
