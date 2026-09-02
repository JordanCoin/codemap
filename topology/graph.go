package topology

import (
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

const hubThreshold = 3

func MergeFragments(root string, fragments []Fragment) *Graph {
	graph := &Graph{
		Nodes:        make(map[ID]Node),
		Dependencies: make(map[ID][]Edge),
		Dependents:   make(map[ID][]Edge),
		Members:      make(map[ID][]string),
		Owners:       make(map[string][]ID),
		Coverage:     Coverage{Status: CoverageUnavailable},
	}
	if len(fragments) == 0 {
		return graph
	}

	fragments = append([]Fragment(nil), fragments...)
	sort.Slice(fragments, func(i, j int) bool {
		if fragments[i].Provider != fragments[j].Provider {
			return fragments[i].Provider < fragments[j].Provider
		}
		return fragmentKey(fragments[i]) < fragmentKey(fragments[j])
	})

	var issues []Issue
	invalidIDs := make(map[ID]bool)
	complete := true
	sawNotApplicable := false
	for _, fragment := range fragments {
		// A provider that found none of its manifests reports Unavailable with
		// nothing to say: "not applicable here", not "incomplete". Such a
		// fragment must not drag down a graph that other providers answered
		// fully, but it does mean an empty graph is unavailable rather than
		// completely known. A provider that declares Complete, or that carries
		// nodes, edges or issues, always counts toward completeness.
		if fragment.Coverage.Status == CoverageUnavailable && len(fragment.Nodes) == 0 &&
			len(fragment.Edges) == 0 && len(fragment.Coverage.Issues) == 0 && len(fragment.Members) == 0 {
			sawNotApplicable = true
			continue
		}
		if fragment.Coverage.Status != CoverageComplete {
			complete = false
		}
		for _, issue := range fragment.Coverage.Issues {
			issue.Candidates = uniqueSortedIDs(issue.Candidates)
			issues = append(issues, issue)
		}

		nodes := append([]Node(nil), fragment.Nodes...)
		sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
		for _, node := range nodes {
			node = normalizeNode(node, fragment.Provider)
			if node.ID == "" {
				issues = append(issues, Issue{Provider: fragment.Provider, Code: "missing-node-id", Message: "topology node has no ID"})
				complete = false
				continue
			}
			if err := validateNodePath(root, node); err != nil {
				issues = append(issues, Issue{Provider: fragment.Provider, Code: "invalid-node-path", Message: fmt.Sprintf("%s: %v", node.ID, err)})
				invalidIDs[node.ID] = true
				delete(graph.Nodes, node.ID)
				complete = false
				continue
			}
			if existing, ok := graph.Nodes[node.ID]; ok && !reflect.DeepEqual(existing, node) {
				issues = append(issues, Issue{Provider: fragment.Provider, Code: "conflicting-node-id", Message: fmt.Sprintf("conflicting definitions for %s", node.ID)})
				invalidIDs[node.ID] = true
				delete(graph.Nodes, node.ID)
				complete = false
				continue
			}
			if !invalidIDs[node.ID] {
				graph.Nodes[node.ID] = node
			}
		}
	}

	for _, fragment := range fragments {
		memberIDs := make([]ID, 0, len(fragment.Members))
		for id := range fragment.Members {
			memberIDs = append(memberIDs, id)
		}
		sort.Slice(memberIDs, func(i, j int) bool { return memberIDs[i] < memberIDs[j] })
		for _, id := range memberIDs {
			if _, ok := graph.Nodes[id]; !ok {
				issues = append(issues, Issue{Provider: fragment.Provider, Code: "unknown-member-node", Message: fmt.Sprintf("members reference unknown node %s", id)})
				complete = false
				continue
			}
			for _, member := range uniqueSortedStrings(fragment.Members[id]) {
				normalized, err := normalizeRepoPath(root, member)
				if err != nil {
					issues = append(issues, Issue{Provider: fragment.Provider, Code: "invalid-member-path", Message: fmt.Sprintf("%s: %v", member, err)})
					complete = false
					continue
				}
				graph.Members[id] = appendUniqueString(graph.Members[id], normalized)
			}
			sort.Strings(graph.Members[id])
		}
	}

	for id, members := range graph.Members {
		for _, member := range members {
			graph.Owners[member] = append(graph.Owners[member], id)
		}
	}
	for member, owners := range graph.Owners {
		graph.Owners[member] = uniqueSortedIDs(owners)
	}

	seenEdges := make(map[string]bool)
	for _, fragment := range fragments {
		edges := append([]Edge(nil), fragment.Edges...)
		sort.Slice(edges, func(i, j int) bool { return edgeKey(edges[i]) < edgeKey(edges[j]) })
		for _, edge := range edges {
			if !validEdgeKind(edge.Kind) {
				issues = append(issues, Issue{
					Provider: fragment.Provider,
					Code:     "invalid-edge-kind",
					Message:  fmt.Sprintf("edge %s -> %s has unsupported kind %q", edge.From, edge.To, edge.Kind),
				})
				complete = false
				continue
			}
			if _, ok := graph.Nodes[edge.From]; !ok {
				issues = append(issues, Issue{Provider: fragment.Provider, Code: "unknown-edge-source", Message: fmt.Sprintf("edge source %s does not exist", edge.From)})
				complete = false
				continue
			}
			if _, ok := graph.Nodes[edge.To]; !ok {
				issues = append(issues, Issue{Provider: fragment.Provider, Code: "unknown-edge-target", Message: fmt.Sprintf("edge target %s does not exist", edge.To)})
				complete = false
				continue
			}
			manifest, err := normalizeRepoPath(root, edge.Evidence.Manifest)
			if err != nil {
				issues = append(issues, Issue{Provider: fragment.Provider, Code: "invalid-edge-evidence", Message: fmt.Sprintf("%s: %v", edge.Evidence.Manifest, err)})
				complete = false
				continue
			}
			edge.Evidence.Manifest = manifest
			key := edgeKey(edge)
			if seenEdges[key] {
				continue
			}
			seenEdges[key] = true
			graph.Dependencies[edge.From] = append(graph.Dependencies[edge.From], edge)
			graph.Dependents[edge.To] = append(graph.Dependents[edge.To], edge)
		}
	}
	for id := range graph.Dependencies {
		sortEdges(graph.Dependencies[id])
	}
	for id := range graph.Dependents {
		sortEdges(graph.Dependents[id])
	}

	issues = uniqueSortedIssues(issues)
	switch {
	case len(graph.Nodes) == 0 && (!complete || sawNotApplicable):
		graph.Coverage.Status = CoverageUnavailable
	case complete && len(issues) == 0:
		graph.Coverage.Status = CoverageComplete
	default:
		graph.Coverage.Status = CoveragePartial
	}
	graph.Coverage.Issues = issues
	return graph
}

func validEdgeKind(kind EdgeKind) bool {
	switch kind {
	case EdgeDependency, EdgeInheritance, EdgeBuildBoundary:
		return true
	default:
		return false
	}
}

func (g *Graph) OwnersForFile(path string) []ID {
	if g == nil {
		return nil
	}
	clean := filepath.Clean(path)
	return append([]ID(nil), g.Owners[clean]...)
}

func (g *Graph) SelectModule(query string) (Node, []ID, bool) {
	if g == nil {
		return Node{}, nil, false
	}
	if node, ok := g.Nodes[ID(query)]; ok {
		return node, nil, true
	}
	var candidates []ID
	for id, node := range g.Nodes {
		if node.Name == query {
			candidates = append(candidates, id)
		}
	}
	candidates = uniqueSortedIDs(candidates)
	if len(candidates) != 1 {
		return Node{}, candidates, false
	}
	return g.Nodes[candidates[0]], nil, true
}

func (g *Graph) IsHub(id ID) bool {
	if g == nil || len(g.Members[id]) == 0 {
		return false
	}
	dependents := make(map[ID]bool)
	for _, edge := range g.Dependents[id] {
		if edge.Kind == EdgeDependency {
			dependents[edge.From] = true
		}
	}
	return len(dependents) >= hubThreshold
}

func (g *Graph) HubNodes() []ID {
	if g == nil {
		return nil
	}
	var hubs []ID
	for id := range g.Nodes {
		if g.IsHub(id) {
			hubs = append(hubs, id)
		}
	}
	sort.Slice(hubs, func(i, j int) bool { return hubs[i] < hubs[j] })
	return hubs
}

func ExpandReference(from ID, template Edge, resolution ReferenceResolution) ([]Edge, *Issue) {
	switch resolution.Status {
	case ResolutionResolved:
		targets := uniqueSortedIDs(resolution.Targets)
		if len(targets) == 0 {
			return nil, &Issue{Code: "unresolved-reference", Message: resolutionMessage(resolution, "reference resolved without targets")}
		}
		edges := make([]Edge, 0, len(targets))
		for _, target := range targets {
			edge := template
			edge.From = from
			edge.To = target
			edges = append(edges, edge)
		}
		return edges, nil
	case ResolutionUnresolved:
		return nil, &Issue{Code: "unresolved-reference", Message: resolutionMessage(resolution, "reference did not resolve")}
	case ResolutionAmbiguous:
		return nil, &Issue{
			Code:       "ambiguous-reference",
			Message:    resolutionMessage(resolution, "reference is ambiguous"),
			Candidates: uniqueSortedIDs(resolution.Candidates),
		}
	default:
		return nil, &Issue{Code: "invalid-reference-resolution", Message: resolutionMessage(resolution, "reference has an invalid resolution status")}
	}
}

func normalizeNode(node Node, provider string) Node {
	if node.Provider == "" {
		node.Provider = provider
	}
	if node.Manifest != "" {
		node.Manifest = filepath.Clean(node.Manifest)
	}
	if node.Root != "" {
		node.Root = filepath.Clean(node.Root)
	}
	node.SourceRoots = normalizePathList(node.SourceRoots)
	node.TestSourceRoots = normalizePathList(node.TestSourceRoots)
	return node
}

func validateNodePath(root string, node Node) error {
	for label, path := range map[string]string{
		"manifest": node.Manifest,
		"root":     node.Root,
	} {
		if _, err := normalizeRepoPath(root, path); err != nil {
			return fmt.Errorf("%s %q: %w", label, path, err)
		}
	}
	for _, path := range append(append([]string(nil), node.SourceRoots...), node.TestSourceRoots...) {
		if _, err := normalizeRepoPath(root, path); err != nil {
			return fmt.Errorf("source root %q: %w", path, err)
		}
	}
	return nil
}

func normalizeRepoPath(root, path string) (string, error) {
	if path == "" || filepath.IsAbs(path) {
		return "", fmt.Errorf("path must be non-empty and repository-relative")
	}
	clean := filepath.Clean(path)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes repository")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	joined, err := filepath.Abs(filepath.Join(absRoot, clean))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absRoot, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes repository")
	}
	return clean, nil
}

func normalizePathList(paths []string) []string {
	normalized := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			normalized = append(normalized, path)
			continue
		}
		normalized = append(normalized, filepath.Clean(path))
	}
	return uniqueSortedStrings(normalized)
}

func resolutionMessage(resolution ReferenceResolution, fallback string) string {
	if resolution.Note != "" {
		return resolution.Note
	}
	return fallback
}

func appendUniqueString(items []string, item string) []string {
	for _, existing := range items {
		if existing == item {
			return items
		}
	}
	return append(items, item)
}

func uniqueSortedIDs(ids []ID) []ID {
	seen := make(map[ID]bool, len(ids))
	result := make([]ID, 0, len(ids))
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func uniqueSortedStrings(items []string) []string {
	seen := make(map[string]bool, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	sort.Strings(result)
	return result
}

func uniqueSortedIssues(issues []Issue) []Issue {
	for i := range issues {
		issues[i].Candidates = uniqueSortedIDs(issues[i].Candidates)
	}
	sort.Slice(issues, func(i, j int) bool {
		return issueKey(issues[i]) < issueKey(issues[j])
	})
	result := issues[:0]
	for _, issue := range issues {
		if len(result) == 0 || issueKey(result[len(result)-1]) != issueKey(issue) {
			result = append(result, issue)
		}
	}
	return result
}

func sortEdges(edges []Edge) {
	sort.Slice(edges, func(i, j int) bool { return edgeKey(edges[i]) < edgeKey(edges[j]) })
}

func edgeKey(edge Edge) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%09d\x00%t\x00%t",
		edge.From, edge.To, edge.Kind, edge.Scope, edge.Evidence.Manifest,
		edge.Evidence.Line, edge.Conditional, edge.Incomplete)
}

func issueKey(issue Issue) string {
	ids := make([]string, len(issue.Candidates))
	for i, id := range issue.Candidates {
		ids[i] = string(id)
	}
	return issue.Provider + "\x00" + issue.Code + "\x00" + issue.Message + "\x00" + strings.Join(ids, "\x00")
}

func fragmentKey(fragment Fragment) string {
	var ids []string
	for _, node := range fragment.Nodes {
		ids = append(ids, string(node.ID))
	}
	sort.Strings(ids)
	return strings.Join(ids, "\x00")
}
