package topology

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"codemap/scanner"
)

type swiftPMProvider struct{}

func init() {
	RegisterProvider(swiftPMProvider{})
}

func (swiftPMProvider) Name() string        { return "swiftpm" }
func (swiftPMProvider) Version() string     { return "1" }
func (swiftPMProvider) Languages() []string { return []string{"swift"} }
func (swiftPMProvider) Manifests() ManifestSelector {
	return ManifestSelector{Names: []string{"Package.swift"}}
}

func (swiftPMProvider) Build(ctx context.Context, inventory Inventory) (Fragment, error) {
	manifests := make([]string, 0, len(inventory.Manifests))
	for _, manifest := range inventory.Manifests {
		if filepath.Base(manifest) == "Package.swift" {
			manifests = append(manifests, filepath.Clean(manifest))
		}
	}
	sort.Strings(manifests)
	if len(manifests) == 0 {
		return Fragment{Provider: "swiftpm", Coverage: Coverage{Status: CoverageUnavailable}}, nil
	}

	fragment := Fragment{
		Provider: "swiftpm",
		Members:  make(map[ID][]string),
		Coverage: Coverage{Status: CoverageComplete},
	}
	for _, manifest := range manifests {
		if err := ctx.Err(); err != nil {
			return Fragment{}, err
		}
		data, err := os.ReadFile(filepath.Join(inventory.Root, manifest))
		if err != nil {
			return Fragment{}, err
		}
		parsed := parseSwiftPMManifest(manifest, data)
		packageFragment := buildSwiftPMPackageFragment(inventory.Files, parsed)
		fragment.Nodes = append(fragment.Nodes, packageFragment.Nodes...)
		fragment.Edges = append(fragment.Edges, packageFragment.Edges...)
		for id, members := range packageFragment.Members {
			fragment.Members[id] = append(fragment.Members[id], members...)
		}
		fragment.Coverage.Issues = append(fragment.Coverage.Issues, packageFragment.Coverage.Issues...)
	}
	if len(fragment.Coverage.Issues) > 0 {
		fragment.Coverage.Status = CoveragePartial
	}
	return fragment, nil
}

func buildSwiftPMPackageFragment(files []scanner.FileInfo, parsed swiftPMManifest) Fragment {
	fragment := Fragment{
		Provider: "swiftpm",
		Members:  make(map[ID][]string),
		Coverage: Coverage{Status: CoverageComplete, Issues: append([]Issue(nil), parsed.issues...)},
	}
	targets := make(map[string][]ID)
	for _, target := range parsed.targets {
		id := swiftPMID(parsed.manifest, target.name)
		targets[target.name] = append(targets[target.name], id)
		fragment.Nodes = append(fragment.Nodes, Node{
			ID:              id,
			Kind:            target.kind,
			Name:            target.name,
			Manifest:        parsed.manifest,
			Root:            target.root,
			SourceRoots:     uniqueSortedStrings(target.sourceRoots),
			TestSourceRoots: uniqueSortedStrings(target.testRoots),
			Provider:        "swiftpm",
		})
		fragment.Members[id] = swiftPMMembers(files, target.memberRoots, target.excludes)
	}
	pathTargets := make(map[string][]ID)
	for _, target := range parsed.targets {
		pathTargets[target.root] = append(pathTargets[target.root], swiftPMID(parsed.manifest, target.name))
	}
	for root, ids := range pathTargets {
		if len(ids) > 1 {
			fragment.Coverage.Issues = append(fragment.Coverage.Issues, Issue{
				Provider:   "swiftpm",
				Code:       "ambiguous-swiftpm-target-path",
				Message:    fmt.Sprintf("%s target path %q is shared by multiple targets", parsed.manifest, root),
				Candidates: uniqueSortedIDs(ids),
			})
		}
	}

	products := make(map[string][]swiftPMProduct)
	for _, product := range parsed.products {
		products[product.name] = append(products[product.name], product)
	}
	for _, target := range parsed.targets {
		sourceID := swiftPMID(parsed.manifest, target.name)
		for _, dependency := range target.dependencies {
			edges, issue := resolveSwiftPMDependency(parsed.manifest, sourceID, dependency, targets, products)
			fragment.Edges = append(fragment.Edges, edges...)
			if issue != nil {
				fragment.Coverage.Issues = append(fragment.Coverage.Issues, *issue)
			}
		}
	}
	if len(fragment.Coverage.Issues) > 0 {
		fragment.Coverage.Status = CoveragePartial
	}
	return fragment
}

func resolveSwiftPMDependency(
	manifest string,
	sourceID ID,
	dependency swiftPMDependency,
	targets map[string][]ID,
	products map[string][]swiftPMProduct,
) ([]Edge, *Issue) {
	template := Edge{
		Kind: EdgeDependency, Scope: EdgeScope(dependency.kind),
		Evidence: Evidence{Manifest: manifest}, Conditional: dependency.conditional,
	}
	if dependency.kind == "product" {
		if dependency.packageName != "" {
			return nil, nil
		}
		definitions := products[dependency.name]
		if len(definitions) == 0 {
			return nil, nil
		}
		var candidates []ID
		for _, product := range definitions {
			for _, targetName := range product.targets {
				candidates = append(candidates, targets[targetName]...)
			}
		}
		if len(definitions) > 1 {
			return nil, &Issue{
				Provider: "swiftpm", Code: "ambiguous-swiftpm-product",
				Message:    fmt.Sprintf("%s product %q has multiple local definitions", manifest, dependency.name),
				Candidates: uniqueSortedIDs(candidates),
			}
		}
		if len(candidates) != len(definitions[0].targets) {
			return nil, &Issue{
				Provider: "swiftpm", Code: "unresolved-swiftpm-product-target",
				Message:    fmt.Sprintf("%s product %q references an unknown target", manifest, dependency.name),
				Candidates: uniqueSortedIDs(candidates),
			}
		}
		return ExpandReference(sourceID, template, ReferenceResolution{
			Status: ResolutionResolved, Targets: candidates,
		})
	}

	candidates := uniqueSortedIDs(targets[dependency.name])
	if len(candidates) == 0 && dependency.kind == "byName" {
		definitions := products[dependency.name]
		if len(definitions) == 1 {
			for _, targetName := range definitions[0].targets {
				candidates = append(candidates, targets[targetName]...)
			}
			candidates = uniqueSortedIDs(candidates)
		} else if len(definitions) > 1 {
			for _, product := range definitions {
				for _, targetName := range product.targets {
					candidates = append(candidates, targets[targetName]...)
				}
			}
			return nil, &Issue{
				Provider: "swiftpm", Code: "ambiguous-swiftpm-product",
				Message:    fmt.Sprintf("%s dependency %q matches multiple local products", manifest, dependency.name),
				Candidates: uniqueSortedIDs(candidates),
			}
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	status := ResolutionResolved
	if len(candidates) > 1 {
		status = ResolutionAmbiguous
	}
	edges, issue := ExpandReference(sourceID, template, ReferenceResolution{
		Status: status, Targets: candidates, Candidates: candidates,
		Note: fmt.Sprintf("%s dependency %q is ambiguous", manifest, dependency.name),
	})
	if issue != nil {
		issue.Provider = "swiftpm"
		issue.Code = "ambiguous-swiftpm-target"
	}
	return edges, issue
}

func swiftPMID(manifest, target string) ID {
	return ID("swiftpm:" + filepath.ToSlash(filepath.Clean(manifest)) + ":" + target)
}

func swiftPMMembers(files []scanner.FileInfo, roots, excludes []string) []string {
	var members []string
	for _, file := range files {
		included := false
		for _, root := range roots {
			if swiftPMPathWithin(file.Path, root) {
				included = true
				break
			}
		}
		if !included {
			continue
		}
		excluded := false
		for _, exclude := range excludes {
			if swiftPMPathWithin(file.Path, exclude) {
				excluded = true
				break
			}
		}
		if !excluded {
			members = append(members, filepath.Clean(file.Path))
		}
	}
	return uniqueSortedStrings(members)
}

func swiftPMPathWithin(path, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
