package codemapmcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"codemap/internal/projectpath"
	"codemap/limits"
	"codemap/topology"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type TopologyInput struct {
	Path      string `json:"path" jsonschema:"Path to the project directory"`
	Ecosystem string `json:"ecosystem,omitempty" jsonschema:"Optional provider/ecosystem filter such as jvm or swiftpm"`
}

type ModuleContextInput struct {
	Path   string `json:"path" jsonschema:"Path to the project directory"`
	Module string `json:"module,omitempty" jsonschema:"Exact topology module ID or unique display name"`
	File   string `json:"file,omitempty" jsonschema:"Repository-relative file path whose owning module should be resolved"`
}

type ModuleContextOutput struct {
	ID           topology.ID     `json:"id,omitempty"`
	Node         *topology.Node  `json:"node,omitempty"`
	File         string          `json:"file,omitempty"`
	Candidates   []topology.ID   `json:"candidates,omitempty"`
	Dependencies []topology.Edge `json:"dependencies,omitempty"`
	Dependents   []topology.Edge `json:"dependents,omitempty"`
	Members      []string        `json:"members,omitempty"`
	Hub          bool            `json:"hub"`
}

type TopologyOutput struct {
	Nodes               map[topology.ID]topology.Node   `json:"nodes"`
	Dependencies        map[topology.ID][]topology.Edge `json:"dependencies"`
	Dependents          map[topology.ID][]topology.Edge `json:"dependents"`
	Members             map[topology.ID][]string        `json:"members"`
	Owners              map[string][]topology.ID        `json:"owners"`
	Coverage            topology.Coverage               `json:"coverage"`
	Truncated           bool                            `json:"truncated,omitempty"`
	TotalNodes          int                             `json:"total_nodes,omitempty"`
	TotalDependencies   int                             `json:"total_dependencies,omitempty"`
	TotalMembers        int                             `json:"total_members,omitempty"`
	TotalOwners         int                             `json:"total_owners,omitempty"`
	TotalCoverageIssues *int                            `json:"total_coverage_issues,omitempty"`
}

var buildTopologyGraphOnly = topology.BuildGraph

func handleGetTopology(ctx context.Context, req *mcp.CallToolRequest, input TopologyInput) (*mcp.CallToolResult, any, error) {
	root, err := topologyProjectRoot(input.Path)
	if err != nil {
		return errorResult("Invalid path: " + err.Error()), nil, nil
	}
	graph, _, err := buildTopologyGraphOnly(ctx, root)
	if err != nil {
		return errorResult("Could not build topology: " + err.Error()), nil, nil
	}
	graph = topology.FilterGraph(graph, input.Ecosystem)
	return textResult(topology.FormatGraph(graph, "")), boundedTopologyOutput(graph), nil
}

func boundedTopologyOutput(graph *topology.Graph) TopologyOutput {
	output := TopologyOutput{
		Nodes: graph.Nodes, Dependencies: graph.Dependencies, Dependents: graph.Dependents,
		Members: graph.Members, Owners: graph.Owners, Coverage: graph.Coverage,
	}
	if encoded, err := json.Marshal(output); err == nil && len(encoded) <= limits.MaxContextOutputBytes {
		return output
	}

	coverageIssueCount := len(graph.Coverage.Issues)
	output = TopologyOutput{
		Nodes:               make(map[topology.ID]topology.Node),
		Dependencies:        make(map[topology.ID][]topology.Edge),
		Dependents:          make(map[topology.ID][]topology.Edge),
		Members:             make(map[topology.ID][]string),
		Owners:              make(map[string][]topology.ID),
		Coverage:            topology.Coverage{Status: graph.Coverage.Status},
		Truncated:           true,
		TotalNodes:          len(graph.Nodes),
		TotalDependencies:   topologyEdgeCount(graph.Dependencies),
		TotalMembers:        topologyMemberCount(graph.Members),
		TotalOwners:         len(graph.Owners),
		TotalCoverageIssues: &coverageIssueCount,
	}
	ids := make([]string, 0, len(graph.Nodes))
	for id := range graph.Nodes {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	for _, rawID := range ids {
		id := topology.ID(rawID)
		output.Nodes[id] = graph.Nodes[id]
		encoded, err := json.Marshal(output)
		if err != nil || len(encoded) > limits.MaxContextOutputBytes {
			delete(output.Nodes, id)
			break
		}
	}
	return output
}

func topologyEdgeCount(edges map[topology.ID][]topology.Edge) int {
	total := 0
	for _, entries := range edges {
		total += len(entries)
	}
	return total
}

func topologyMemberCount(members map[topology.ID][]string) int {
	total := 0
	for _, entries := range members {
		total += len(entries)
	}
	return total
}

func handleGetModuleContext(ctx context.Context, req *mcp.CallToolRequest, input ModuleContextInput) (*mcp.CallToolResult, any, error) {
	if (input.Module == "") == (input.File == "") {
		return errorResult("Specify either module or file, but not both."), nil, nil
	}
	root, err := topologyProjectRoot(input.Path)
	if err != nil {
		return errorResult("Invalid path: " + err.Error()), nil, nil
	}
	graph, _, err := buildTopologyGraphOnly(ctx, root)
	if err != nil {
		return errorResult("Could not build topology: " + err.Error()), nil, nil
	}
	text, err := topology.FormatModuleContext(graph, input.Module, input.File)
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}
	return textResult(text), structuredModuleContext(graph, input), nil
}

func topologyProjectRoot(path string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		path = filepath.Join(os.Getenv("HOME"), path[2:])
	}
	selection, err := projectpath.Select(path)
	if err != nil {
		return "", err
	}
	return selection.ProjectRoot, nil
}

func structuredModuleContext(graph *topology.Graph, input ModuleContextInput) ModuleContextOutput {
	result := ModuleContextOutput{}
	var node topology.Node
	var candidates []topology.ID
	var ok bool
	if input.File != "" {
		result.File = filepath.Clean(input.File)
		candidates = graph.OwnersForFile(result.File)
		if len(candidates) == 1 {
			node, ok = graph.Nodes[candidates[0]]
		}
	} else {
		node, candidates, ok = graph.SelectModule(input.Module)
	}
	if !ok {
		result.Candidates = candidates
		return result
	}
	result.ID = node.ID
	result.Node = &node
	result.Dependencies = graph.Dependencies[node.ID]
	result.Dependents = graph.Dependents[node.ID]
	result.Members = graph.Members[node.ID]
	result.Hub = graph.IsHub(node.ID)
	return result
}

func topologyOutputSchema[T any]() *jsonschema.Schema {
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		panic(err)
	}
	return schema
}
