package topology

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

func FormatGraph(graph *Graph, ecosystem string) string {
	graph = FilterGraph(graph, ecosystem)
	if graph == nil {
		return "PROJECT TOPOLOGY\nCoverage: unavailable\nModules: 0\n"
	}

	var builder strings.Builder
	builder.WriteString("PROJECT TOPOLOGY\n")
	_, _ = fmt.Fprintf(&builder, "Coverage: %s\n", graph.Coverage.Status)
	_, _ = fmt.Fprintf(&builder, "Modules: %d\n", len(graph.Nodes))
	_, _ = fmt.Fprintf(&builder, "Hubs: %d\n", len(graph.HubNodes()))
	if len(graph.Coverage.Issues) > 0 {
		_, _ = fmt.Fprintf(&builder, "Coverage notes: %d\n", len(graph.Coverage.Issues))
	}
	builder.WriteString("\n")

	for _, id := range sortedNodeIDs(graph.Nodes) {
		node := graph.Nodes[id]
		_, _ = fmt.Fprintf(&builder, "- %s (%s) [%s]\n", node.Name, node.ID, node.Kind)
		_, _ = fmt.Fprintf(&builder, "  root: %s; members: %d", node.Root, len(graph.Members[id]))
		if graph.IsHub(id) {
			builder.WriteString("; hub")
		}
		builder.WriteString("\n")
		for _, edge := range graph.Dependencies[id] {
			target, ok := graph.Nodes[edge.To]
			if !ok {
				continue
			}
			qualifiers := []string{string(edge.Kind)}
			if edge.Scope != "" {
				qualifiers = append(qualifiers, string(edge.Scope))
			}
			_, _ = fmt.Fprintf(&builder, "  %s -> %s [%s]\n", node.Name, target.Name, strings.Join(qualifiers, ", "))
		}
	}
	return builder.String()
}

func FormatGraphJSON(graph *Graph, ecosystem string) ([]byte, error) {
	filtered := FilterGraph(graph, ecosystem)
	return json.MarshalIndent(filtered, "", "  ")
}

func FormatModuleContext(graph *Graph, module, file string) (string, error) {
	if graph == nil {
		return "", fmt.Errorf("topology is unavailable")
	}
	if module != "" && file != "" {
		return "", fmt.Errorf("select either module or file, not both")
	}
	if module == "" && file == "" {
		return "", fmt.Errorf("module or file is required")
	}

	selectedFile := ""
	var node Node
	if file != "" {
		selectedFile = filepath.Clean(file)
		owners := graph.OwnersForFile(selectedFile)
		switch len(owners) {
		case 0:
			return "", fmt.Errorf("no topology module owns %s", selectedFile)
		case 1:
			node = graph.Nodes[owners[0]]
		default:
			var builder strings.Builder
			_, _ = fmt.Fprintf(&builder, "Ambiguous ownership for %s\n", selectedFile)
			builder.WriteString("Candidate module IDs:\n")
			for _, id := range owners {
				builder.WriteString("- " + string(id) + "\n")
			}
			builder.WriteString("Retry with an exact module ID.\n")
			return builder.String(), nil
		}
	} else {
		var candidates []ID
		var ok bool
		node, candidates, ok = graph.SelectModule(module)
		if !ok {
			if len(candidates) == 0 {
				return "", fmt.Errorf("topology module %q was not found", module)
			}
			var builder strings.Builder
			_, _ = fmt.Fprintf(&builder, "Ambiguous module %q\n", module)
			builder.WriteString("Candidate module IDs:\n")
			for _, id := range candidates {
				builder.WriteString("- " + string(id) + "\n")
			}
			builder.WriteString("Retry with an exact module ID.\n")
			return builder.String(), nil
		}
	}

	var builder strings.Builder
	builder.WriteString("MODULE CONTEXT\n")
	_, _ = fmt.Fprintf(&builder, "Module: %s\n", node.Name)
	_, _ = fmt.Fprintf(&builder, "ID: %s\n", node.ID)
	_, _ = fmt.Fprintf(&builder, "Kind: %s\n", node.Kind)
	_, _ = fmt.Fprintf(&builder, "Provider: %s\n", node.Provider)
	_, _ = fmt.Fprintf(&builder, "Root: %s\n", node.Root)
	_, _ = fmt.Fprintf(&builder, "Manifest: %s\n", node.Manifest)
	_, _ = fmt.Fprintf(&builder, "Members: %d\n", len(graph.Members[node.ID]))
	_, _ = fmt.Fprintf(&builder, "Hub: %s\n", yesNo(graph.IsHub(node.ID)))
	if selectedFile != "" {
		_, _ = fmt.Fprintf(&builder, "File: %s\n", selectedFile)
		_, _ = fmt.Fprintf(&builder, "Member of hub module: %s\n", yesNo(graph.IsHub(node.ID)))
	}
	_, _ = fmt.Fprintf(&builder, "Dependencies: %d\n", len(graph.Dependencies[node.ID]))
	for _, edge := range graph.Dependencies[node.ID] {
		builder.WriteString(formatContextEdge(graph, edge, "->"))
	}
	_, _ = fmt.Fprintf(&builder, "Dependents: %d\n", len(graph.Dependents[node.ID]))
	for _, edge := range graph.Dependents[node.ID] {
		builder.WriteString(formatContextEdge(graph, edge, "<-"))
	}
	return builder.String(), nil
}

func FilterGraph(graph *Graph, ecosystem string) *Graph {
	if graph == nil || strings.TrimSpace(ecosystem) == "" {
		return graph
	}
	ecosystem = strings.ToLower(strings.TrimSpace(ecosystem))
	nodes := make([]Node, 0, len(graph.Nodes))
	selected := make(map[ID]bool)
	for _, node := range graph.Nodes {
		if strings.EqualFold(node.Provider, ecosystem) {
			nodes = append(nodes, node)
			selected[node.ID] = true
		}
	}
	var edges []Edge
	for from, outgoing := range graph.Dependencies {
		if !selected[from] {
			continue
		}
		for _, edge := range outgoing {
			if selected[edge.To] {
				edges = append(edges, edge)
			}
		}
	}
	members := make(map[ID][]string)
	for id := range selected {
		members[id] = append([]string(nil), graph.Members[id]...)
	}
	var issues []Issue
	for _, issue := range graph.Coverage.Issues {
		if issue.Provider == "" || strings.EqualFold(issue.Provider, ecosystem) {
			issues = append(issues, issue)
		}
	}
	return MergeFragments(".", []Fragment{{
		Provider: ecosystem,
		Nodes:    nodes,
		Edges:    edges,
		Members:  members,
		Coverage: Coverage{Status: graph.Coverage.Status, Issues: issues},
	}})
}

func formatContextEdge(graph *Graph, edge Edge, arrow string) string {
	from := graph.Nodes[edge.From]
	to := graph.Nodes[edge.To]
	if arrow == "<-" {
		return fmt.Sprintf("- %s <- %s [%s]\n", to.Name, from.Name, edgeDescription(edge))
	}
	return fmt.Sprintf("- %s -> %s [%s]\n", from.Name, to.Name, edgeDescription(edge))
}

func edgeDescription(edge Edge) string {
	parts := []string{string(edge.Kind)}
	if edge.Scope != "" {
		parts = append(parts, string(edge.Scope))
	}
	if edge.Evidence.Manifest != "" {
		evidence := edge.Evidence.Manifest
		if edge.Evidence.Line > 0 {
			evidence += fmt.Sprintf(":%d", edge.Evidence.Line)
		}
		parts = append(parts, evidence)
	}
	if edge.Conditional {
		parts = append(parts, "conditional")
	}
	if edge.Incomplete {
		parts = append(parts, "incomplete")
	}
	return strings.Join(parts, ", ")
}

func sortedNodeIDs(nodes map[ID]Node) []ID {
	ids := make([]ID, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
