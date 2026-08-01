package scanner

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

var errCargoFallbackUnavailable = errors.New("cargo dependency fallback unavailable")

type dependencyOutcomeScanner func(string) (ScanOutcome, error)

type cargoFallbackPackage struct {
	packageInfo  rustPackage
	dependencies []cargoMetadataDependency
}

func buildFileGraphWithFallback(ctx context.Context, root string, scan dependencyOutcomeScanner, loader cargoMetadataLoader) (*FileGraph, error) {
	return buildFileGraphWithFallbackWithFilters(ctx, root, Filters{}, scan, loader)
}

func buildFileGraphFromOutcomeWithCargoMetadataAndFilters(ctx context.Context, root string, outcome ScanOutcome, filters Filters, loader cargoMetadataLoader) (*FileGraph, error) {
	fg, err := buildFileGraphFromAnalysesWithCargoMetadataAndFilters(ctx, root, outcome.Analyses, filters, loader, outcome.Sources...)
	if err != nil {
		return nil, err
	}
	applyPrecomputedFileEdges(fg, outcome.precomputedEdges)
	return fg, nil
}

func buildFileGraphWithFallbackWithFilters(ctx context.Context, root string, filters Filters, scan dependencyOutcomeScanner, loader cargoMetadataLoader) (*FileGraph, error) {
	outcome, usedFallback, err := scanForGraphOutcomeWithFilters(ctx, root, filters, scan, loader)
	if err != nil {
		return nil, err
	}
	graphLoader := loadCargoMetadata
	if usedFallback {
		graphLoader = nil
	}
	return buildFileGraphFromOutcomeWithCargoMetadataAndFilters(ctx, root, outcome, filters, graphLoader)
}

func scanForGraphOutcomeWithFilters(ctx context.Context, root string, filters Filters, scan dependencyOutcomeScanner, loader cargoMetadataLoader) (ScanOutcome, bool, error) {
	outcome, err := scan(root)
	if err == nil {
		return outcome, false, nil
	}

	var incomplete *IncompleteScanError
	if !errors.As(err, &incomplete) || incomplete.Outcome.Name != "ast-grep" {
		return ScanOutcome{}, false, err
	}
	files, scanErr := ScanFiles(ctx, root, NewGitIgnoreCache(root), filters.Only, filters.Exclude)
	if scanErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ScanOutcome{}, false, ctxErr
		}
		return ScanOutcome{}, false, err
	}
	fallback := ScanOutcome{
		Sources: []ScanSourceOutcome{incomplete.Outcome},
	}
	recovered := false
	if goFallback, fallbackErr := buildGoFallbackOutcome(ctx, root, files); fallbackErr == nil {
		mergeFallbackOutcome(&fallback, goFallback)
		recovered = true
	} else if ctx.Err() != nil {
		return ScanOutcome{}, false, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return ScanOutcome{}, false, err
	}

	if cargoFallback, fallbackErr := buildCargoFallbackOutcome(ctx, root, files, loader); fallbackErr == nil {
		mergeFallbackOutcome(&fallback, cargoFallback)
		recovered = true
	}
	if err := ctx.Err(); err != nil {
		return ScanOutcome{}, false, err
	}
	if !recovered {
		return ScanOutcome{}, false, err
	}
	return fallback, true, nil
}

func mergeFallbackOutcome(dst *ScanOutcome, src ScanOutcome) {
	dst.Analyses = append(dst.Analyses, src.Analyses...)
	dst.Sources = append(dst.Sources, src.Sources...)
	dst.precomputedEdges = append(dst.precomputedEdges, src.precomputedEdges...)
}

func buildCargoFallbackOutcome(ctx context.Context, root string, files []FileInfo, loader cargoMetadataLoader) (ScanOutcome, error) {
	manifests, err := discoverCargoManifests(ctx, root, files)
	if err != nil {
		return ScanOutcome{}, err
	}
	if len(manifests) == 0 || loader == nil {
		return ScanOutcome{}, errCargoFallbackUnavailable
	}

	allowed := make(map[string]bool, len(files))
	for _, file := range files {
		allowed[filepath.Clean(file.Path)] = true
	}
	packages := make(map[string]cargoFallbackPackage)
	handledManifests := make(map[string]bool)

	manifest := filepath.Clean(manifests[0])
	data, err := loader(ctx, manifest)
	if err != nil {
		return ScanOutcome{}, errCargoFallbackUnavailable
	}
	metadata, err := parseCargoMetadata(data)
	if err != nil {
		return ScanOutcome{}, errCargoFallbackUnavailable
	}
	covered := false
	for _, metadataPackage := range metadata.Packages {
		pkg, _, ok := rustPackageFromCargoMetadata(root, metadataPackage)
		if !ok || len(pkg.targets) == 0 {
			continue
		}
		packages[pkg.root] = cargoFallbackPackage{packageInfo: pkg, dependencies: metadataPackage.Dependencies}
		handledManifests[filepath.Clean(metadataPackage.ManifestPath)] = true
		covered = true
	}
	if !covered {
		return ScanOutcome{}, errCargoFallbackUnavailable
	}
	handledManifests[manifest] = true

	edgeSet := make(map[fileEdge]bool)
	for _, pkg := range packages {
		for _, target := range pkg.packageInfo.targets {
			if !allowed[target.rootFile] {
				continue
			}
			for _, dependency := range pkg.dependencies {
				if dependency.Path == "" || dependency.Optional || cargoDependencyIsConditional(dependency) || !rustDependencyEligible(dependency.Kind, target.kind) {
					continue
				}
				dependencyRoot, ok := projectRelativePath(root, dependency.Path)
				if !ok {
					continue
				}
				dependencyPackage, ok := packages[dependencyRoot]
				if !ok || dependencyPackage.packageInfo.lib == nil || !allowed[dependencyPackage.packageInfo.lib.rootFile] {
					continue
				}
				edgeSet[fileEdge{from: target.rootFile, to: dependencyPackage.packageInfo.lib.rootFile}] = true
			}
		}
	}
	if len(edgeSet) == 0 {
		return ScanOutcome{}, errCargoFallbackUnavailable
	}

	edges := make([]fileEdge, 0, len(edgeSet))
	for edge := range edgeSet {
		edges = append(edges, edge)
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].from == edges[j].from {
			return edges[i].to < edges[j].to
		}
		return edges[i].from < edges[j].from
	})
	handled := 0
	for _, manifest := range manifests {
		if handledManifests[filepath.Clean(manifest)] {
			handled++
		}
	}
	return ScanOutcome{
		Sources: []ScanSourceOutcome{{
			Name:   "cargo-metadata",
			Status: ScanSourceFallback,
			Detail: fmt.Sprintf("Cargo metadata fallback recovered %d dependency edges from %d of %d manifests", len(edges), handled, len(manifests)),
		}},
		precomputedEdges: edges,
	}, nil
}

func cargoDependencyIsConditional(dependency cargoMetadataDependency) bool {
	target := strings.TrimSpace(string(dependency.Target))
	return target != "" && target != "null"
}
