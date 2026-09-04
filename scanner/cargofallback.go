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
	for _, source := range outcome.Sources {
		if source.Name == "cargo-metadata" && source.Status == ScanSourceFallback {
			loader = nil
			break
		}
	}
	fg, err := buildFileGraphFromAnalysesWithCargoMetadataAndFilters(ctx, root, outcome.Analyses, filters, loader, outcome.Sources...)
	if err != nil {
		return nil, err
	}
	applyPrecomputedFileEdges(fg, outcome.precomputedEdges)
	fg.sortEdges()
	return fg, nil
}

func buildFileGraphWithFallbackWithFilters(ctx context.Context, root string, filters Filters, scan dependencyOutcomeScanner, loader cargoMetadataLoader) (*FileGraph, error) {
	outcome, usedFallback, err := scanForGraphOutcomeWithFilters(ctx, root, filters, scan, loader, true)
	if err != nil {
		return nil, err
	}
	graphLoader := loadCargoMetadata
	if usedFallback {
		graphLoader = nil
	}
	return buildFileGraphFromOutcomeWithCargoMetadataAndFilters(ctx, root, outcome, filters, graphLoader)
}

func scanForGraphOutcome(ctx context.Context, root string, scan dependencyOutcomeScanner, loader cargoMetadataLoader, allowCargoOnly ...bool) (ScanOutcome, bool, error) {
	allow := true
	if len(allowCargoOnly) > 0 {
		allow = allowCargoOnly[0]
	}
	return scanForGraphOutcomeWithFilters(ctx, root, Filters{}, scan, loader, allow)
}

func scanForGraphOutcomeWithFilters(ctx context.Context, root string, filters Filters, scan dependencyOutcomeScanner, loader cargoMetadataLoader, allowCargoOnly bool) (ScanOutcome, bool, error) {
	// The Cargo fallback resolves each package's absolute manifest_path
	// against root; a relative root (e.g. "." from the CLI) makes
	// filepath.Rel fail and silently drops every recovered package, so
	// absolutize here even though callers are expected to pass absRoot.
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return ScanOutcome{}, false, err
	}
	root = absRoot

	outcome, err := scan(root)
	degraded := false
	if err == nil {
		// A degraded ast-grep outcome (timeout/failure) carries a nil error;
		// treat it like an incomplete scan so the fallback still runs.
		if incomplete := degradedAstGrepError(outcome); incomplete != nil {
			err = incomplete
			degraded = true
		} else {
			outcome.Analyses, err = filterAnalysesContext(ctx, outcome.Analyses, filters)
			if err != nil {
				return ScanOutcome{}, false, err
			}
			return outcome, false, nil
		}
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
	if recovered || allowCargoOnly {
		if cargoFallback, fallbackErr := buildCargoFallbackOutcome(ctx, root, files, loader); fallbackErr == nil {
			mergeFallbackOutcome(&fallback, cargoFallback)
			recovered = true
		}
	}
	if err := ctx.Err(); err != nil {
		return ScanOutcome{}, false, err
	}
	if !recovered {
		if degraded {
			// Nothing recovered from a degraded scan: fail closed with the
			// degraded outcome, not a hard error.
			return outcome, false, nil
		}
		return ScanOutcome{}, false, err
	}
	return fallback, true, nil
}

// degradedAstGrepError turns a fail-closed ast-grep outcome into the
// incomplete error the fallback gate recognizes, unless it already recovered
// analyses.
func degradedAstGrepError(outcome ScanOutcome) *IncompleteScanError {
	if len(outcome.Analyses) > 0 {
		return nil
	}
	for _, source := range outcome.Sources {
		if source.Name != "ast-grep" {
			continue
		}
		if source.Status == ScanSourceTimeout || source.Status == ScanSourceFailed {
			return &IncompleteScanError{Outcome: source, Err: errors.New(source.Detail)}
		}
	}
	return nil
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
			// Recovered edges only reach the file graph, so don't claim them
			// in the `--deps` payload.
			Detail: fmt.Sprintf("Cargo metadata fallback used for %d of %d manifests", handled, len(manifests)),
		}},
		precomputedEdges: edges,
	}, nil
}

func cargoDependencyIsConditional(dependency cargoMetadataDependency) bool {
	target := strings.TrimSpace(string(dependency.Target))
	return target != "" && target != "null"
}
