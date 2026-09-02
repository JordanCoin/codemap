package cmd

import (
	"context"
	"errors"
	pathpkg "path"
	"sort"
	"strings"
	"time"

	"codemap/analysis"
	"codemap/config"
	"codemap/limits"
	"codemap/scanner"
	"codemap/watch"
)

const (
	graphEvidenceAvailable       = "available"
	graphEvidenceUnavailable     = "unavailable"
	graphEvidenceFreshScan       = "fresh_scan"
	graphEvidenceWatchCache      = "watch_cache"
	graphEvidenceLargeRepository = "large_repository"
	graphEvidenceCancelled       = "cancelled"
	graphEvidenceDeadline        = "deadline"
	graphEvidenceScanFailed      = "scan_failed"
	graphEvidenceScanIncomplete  = "scan_incomplete"
	defaultContextGraphTimeout   = 8 * time.Second
)

// GraphEvidence describes whether dependency conclusions are supported.
type GraphEvidence struct {
	Status string `json:"status"`
	Source string `json:"source,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type contextEnvelopeDeps struct {
	readState           func(string) *watch.State
	scanConfiguredFiles func(context.Context, string) ([]scanner.FileInfo, error)
	buildFileGraph      func(context.Context, string) (*scanner.FileGraph, error)
	now                 func() time.Time
	graphTimeout        time.Duration
}

type contextRequestInputs struct {
	state      *watch.State
	files      []scanner.FileInfo
	info       *hubInfo
	evidence   GraphEvidence
	fileScanOK bool
}

func defaultContextEnvelopeDeps() contextEnvelopeDeps {
	return contextEnvelopeDeps{
		readState: watch.ReadState,
		scanConfiguredFiles: func(ctx context.Context, root string) ([]scanner.FileInfo, error) {
			return scanner.ScanConfiguredFiles(ctx, root, scanner.NewGitIgnoreCache(root))
		},
		buildFileGraph: func(ctx context.Context, root string) (*scanner.FileGraph, error) {
			return scanner.BuildFileGraph(ctx, root, scanner.ConfiguredFilters(root))
		},
		now:          time.Now,
		graphTimeout: defaultContextGraphTimeout,
	}
}

func loadContextRequestInputs(ctx context.Context, root string, deps contextEnvelopeDeps) contextRequestInputs {
	inputs := contextRequestInputs{
		state:    deps.readState(root),
		evidence: unavailableGraphEvidence(graphEvidenceScanFailed),
	}

	files, err := deps.scanConfiguredFiles(ctx, root)
	if err != nil {
		inputs.evidence = unavailableGraphEvidence(graphErrorReason(ctx, err))
		return inputs
	}
	inputs.files = files
	inputs.fileScanOK = true

	if len(files) == 0 {
		inputs.info = &hubInfo{Importers: map[string][]string{}, Imports: map[string][]string{}}
		inputs.evidence = GraphEvidence{Status: graphEvidenceAvailable, Source: graphEvidenceFreshScan}
		return inputs
	}
	if graph, _ := watch.ValidateCachedGraphForInventory(inputs.state, root, config.Load(root), contextFilePaths(files)); graph != nil {
		populateContextGraphInputs(&inputs, graph, graphEvidenceWatchCache)
		return inputs
	}
	if len(files) > limits.LargeRepoFileCount {
		inputs.evidence = unavailableGraphEvidence(graphEvidenceLargeRepository)
		return inputs
	}

	graph, err := deps.buildFileGraph(ctx, root)
	if err != nil {
		inputs.evidence = unavailableGraphEvidence(graphErrorReason(ctx, err))
		return inputs
	}
	if graph == nil {
		inputs.evidence = unavailableGraphEvidence(graphEvidenceScanIncomplete)
		return inputs
	}
	// Complete edge-free graphs are valid; unavailable provenance is not.
	if graph.Coverage.Status == analysis.CoverageUnavailable ||
		(len(graph.Coverage.Sources) > 0 && scanner.CoverageFromSources(graph.Coverage.Sources).Status == analysis.CoverageUnavailable) {
		inputs.evidence = unavailableGraphEvidence(graphEvidenceScanIncomplete)
		return inputs
	}

	populateContextGraphInputs(&inputs, graph, graphEvidenceFreshScan)
	return inputs
}

func contextFilePaths(files []scanner.FileInfo) []string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	return paths
}

func populateContextGraphInputs(inputs *contextRequestInputs, graph *scanner.FileGraph, source string) {
	hubs := sortedGraphHubs(graph.Importers)
	inputs.info = &hubInfo{
		Hubs:      hubs,
		Importers: graph.Importers,
		Imports:   graph.Imports,
		Coverage:  graph.Coverage,
	}
	inputs.evidence = GraphEvidence{Status: graphEvidenceAvailable, Source: source}
}

func unavailableGraphEvidence(reason string) GraphEvidence {
	return GraphEvidence{Status: graphEvidenceUnavailable, Reason: reason}
}

func graphErrorReason(ctx context.Context, err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return graphEvidenceCancelled
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return graphEvidenceDeadline
	}
	return graphEvidenceScanFailed
}

func sortedGraphHubs(importers map[string][]string) []string {
	hubs := make([]string, 0)
	for file, dependents := range importers {
		if len(dependents) >= 3 {
			hubs = append(hubs, file)
		}
	}
	sort.Slice(hubs, func(i, j int) bool {
		left, right := len(importers[hubs[i]]), len(importers[hubs[j]])
		if left != right {
			return left > right
		}
		return normalizeContextPath(hubs[i]) < normalizeContextPath(hubs[j])
	})
	return hubs
}

func normalizeContextPath(file string) string {
	return normalizeContextPathWithVolumeGuard(strings.TrimSpace(file), true)
}

func normalizeContextInventoryPath(file string) string {
	return normalizeContextPathWithVolumeGuard(file, false)
}

func normalizeContextPathWithVolumeGuard(file string, rejectVolume bool) string {
	file = strings.ReplaceAll(file, `\`, "/")
	volumePath := isContextVolumePath(file)
	file = strings.TrimPrefix(pathpkg.Clean(file), "./")
	if file == "." || file == "" || strings.HasPrefix(file, "../") || file == ".." || pathpkg.IsAbs(file) || (rejectVolume && volumePath) {
		return ""
	}
	return file
}

func isContextVolumePath(file string) bool {
	return strings.HasPrefix(file, "//") ||
		(len(file) >= 2 && file[1] == ':' && ((file[0] >= 'a' && file[0] <= 'z') || (file[0] >= 'A' && file[0] <= 'Z')))
}
