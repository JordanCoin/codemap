package main

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"codemap/analysis"
	"codemap/config"
	"codemap/limits"
	"codemap/scanner"
	"codemap/watch"
)

// treeHubs ranks hubs for the tree's star marks: the daemon's validated graph
// when it was built with the same filters, else a fresh scan. The note says
// why hubs are missing or partial; it is empty for a complete graph.
func treeHubs(root string, fileCount int, filters scanner.Filters) ([]scanner.HubEntry, string) {
	cfg := config.Load(root)
	var fg *scanner.FileGraph
	if slices.Equal(filters.Only, cfg.Only) && slices.Equal(filters.Exclude, cfg.Exclude) {
		fg, _ = watch.ValidateCachedGraph(watch.ReadState(root), root, cfg)
	}
	if fg == nil {
		if fileCount > limits.LargeRepoFileCount {
			return nil, fmt.Sprintf("%d files is above the %d-file graph limit; run codemap --deps", fileCount, limits.LargeRepoFileCount)
		}
		var err error
		if fg, err = scanner.BuildFileGraph(context.Background(), root, filters); err != nil {
			return nil, "dependency graph unavailable: " + err.Error()
		}
	}
	coverage := fg.Coverage.EffectiveStatus()
	if coverage == analysis.CoverageUnavailable {
		return nil, "dependency graph unavailable: " + strings.Join(fg.Coverage.Notes, "; ")
	}
	hubs := scanner.CollapseGoPackageHubs(fg.HubFiles(), fg.Importers)
	sort.SliceStable(hubs, func(i, j int) bool { return hubs[i].Importers > hubs[j].Importers })
	if hubs == nil {
		hubs = []scanner.HubEntry{}
	}
	if coverage == analysis.CoveragePartial {
		return hubs, "graph coverage partial, counts are lower bounds (codemap --deps lists the gaps)"
	}
	return hubs, ""
}
