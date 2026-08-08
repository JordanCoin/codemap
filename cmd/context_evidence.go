package cmd

import (
	"context"
	"errors"
	pathpkg "path"
	"sort"
	"strings"
	"time"

	"codemap/limits"
	"codemap/scanner"
	"codemap/watch"
)

const (
	graphEvidenceAvailable       = "available"
	graphEvidenceUnavailable     = "unavailable"
	graphEvidenceFreshScan       = "fresh_scan"
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
	fileSet    map[string]string
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
		fileSet:  make(map[string]string),
		evidence: unavailableGraphEvidence(graphEvidenceScanFailed),
	}

	files, err := deps.scanConfiguredFiles(ctx, root)
	if err != nil {
		inputs.evidence = unavailableGraphEvidence(graphErrorReason(ctx, err))
		return inputs
	}
	inputs.files = files
	inputs.fileScanOK = true
	for _, file := range files {
		normalized := normalizeContextPath(file.Path)
		if normalized != "" {
			inputs.fileSet[normalized] = file.Path
		}
	}

	if len(files) == 0 {
		inputs.info = &hubInfo{Importers: map[string][]string{}, Imports: map[string][]string{}}
		inputs.evidence = GraphEvidence{Status: graphEvidenceAvailable, Source: graphEvidenceFreshScan}
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
	if graph == nil || (len(graph.Imports) == 0 && len(graph.Importers) == 0) {
		inputs.evidence = unavailableGraphEvidence(graphEvidenceScanIncomplete)
		return inputs
	}

	hubs := sortedGraphHubs(graph.Importers)
	inputs.info = &hubInfo{
		Hubs:      hubs,
		Importers: graph.Importers,
		Imports:   graph.Imports,
		Coverage:  graph.Coverage,
	}
	inputs.evidence = GraphEvidence{Status: graphEvidenceAvailable, Source: graphEvidenceFreshScan}
	return inputs
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
	file = strings.TrimSpace(strings.ReplaceAll(file, `\`, "/"))
	file = strings.TrimPrefix(pathpkg.Clean(file), "./")
	if file == "." || file == "" || strings.HasPrefix(file, "../") || file == ".." || pathpkg.IsAbs(file) {
		return ""
	}
	return file
}

func resolveExactConfiguredFiles(mentions []string, fileSet map[string]string) []string {
	resolved := make([]string, 0, len(mentions))
	seen := make(map[string]struct{})
	for _, mention := range mentions {
		normalized := normalizeContextPath(mention)
		actual, ok := fileSet[normalized]
		if !ok {
			continue
		}
		if _, duplicate := seen[actual]; duplicate {
			continue
		}
		seen[actual] = struct{}{}
		resolved = append(resolved, actual)
	}
	return resolved
}
