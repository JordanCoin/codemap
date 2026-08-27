package main

import (
	"context"
	"path/filepath"
	"sort"

	"codemap/scanner"
	"codemap/topology"
)

type blastRadiusModuleImpact struct {
	ID         topology.ID `json:"id"`
	Name       string      `json:"name"`
	Relation   string      `json:"relation"`
	Via        topology.ID `json:"via,omitempty"`
	Dependents int         `json:"dependents,omitempty"`
}

var buildBlastTopology = topology.BuildGraph

func collectTopologyImpacts(root string, changed []scanner.FileInfo) []blastRadiusModuleImpact {
	if len(changed) == 0 {
		return nil
	}
	graph, _, err := buildBlastTopology(context.Background(), root)
	if err != nil || graph == nil || graph.Coverage.Status == topology.CoverageUnavailable {
		return nil
	}
	return buildTopologyImpacts(graph, changed)
}

func buildTopologyImpacts(graph *topology.Graph, changed []scanner.FileInfo) []blastRadiusModuleImpact {
	if graph == nil {
		return nil
	}
	owners := make(map[topology.ID]bool)
	for _, file := range changed {
		candidates := graph.OwnersForFile(filepath.Clean(file.Path))
		if len(candidates) == 1 {
			owners[candidates[0]] = true
		}
	}

	impacts := make(map[topology.ID]blastRadiusModuleImpact)
	for ownerID := range owners {
		node, ok := graph.Nodes[ownerID]
		if !ok {
			continue
		}
		impacts[ownerID] = blastRadiusModuleImpact{
			ID:         ownerID,
			Name:       node.Name,
			Relation:   "owns-changed-file",
			Dependents: topologyDependentCount(graph, ownerID),
		}
		for _, edge := range graph.Dependents[ownerID] {
			if edge.Kind != topology.EdgeDependency || owners[edge.From] {
				continue
			}
			dependent, ok := graph.Nodes[edge.From]
			if !ok {
				continue
			}
			impacts[dependent.ID] = blastRadiusModuleImpact{
				ID:         dependent.ID,
				Name:       dependent.Name,
				Relation:   "depends-on-changed-module",
				Via:        ownerID,
				Dependents: topologyDependentCount(graph, dependent.ID),
			}
		}
	}

	result := make([]blastRadiusModuleImpact, 0, len(impacts))
	for _, impact := range impacts {
		result = append(result, impact)
	}
	sort.Slice(result, func(i, j int) bool {
		leftRank := moduleImpactRank(result[i].Relation)
		rightRank := moduleImpactRank(result[j].Relation)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		return result[i].ID < result[j].ID
	})
	return result
}

func topologyDependentCount(graph *topology.Graph, id topology.ID) int {
	dependents := make(map[topology.ID]bool)
	for _, edge := range graph.Dependents[id] {
		if edge.Kind == topology.EdgeDependency {
			dependents[edge.From] = true
		}
	}
	return len(dependents)
}

func moduleImpactRank(relation string) int {
	if relation == "owns-changed-file" {
		return 0
	}
	return 1
}
