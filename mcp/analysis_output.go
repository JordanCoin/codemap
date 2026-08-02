package codemapmcp

import (
	"codemap/handoff"
	"codemap/scanner"
)

const maxStructuredItems = 500

type StructureOutput struct {
	Kind         string          `json:"kind"`
	Project      scanner.Project `json:"project"`
	Hubs         []string        `json:"hubs"`
	Truncated    bool            `json:"truncated"`
	OmittedCount int             `json:"omitted_count"`
}

type DiffOutput struct {
	Kind         string          `json:"kind"`
	Ref          string          `json:"ref"`
	Project      scanner.Project `json:"project"`
	Truncated    bool            `json:"truncated"`
	OmittedCount int             `json:"omitted_count"`
}

type ImportersOutput struct {
	Kind           string   `json:"kind"`
	Root           string   `json:"root"`
	Mode           string   `json:"mode"`
	File           string   `json:"file"`
	Importers      []string `json:"importers"`
	Imports        []string `json:"imports"`
	HubImports     []string `json:"hub_imports"`
	ImporterCount  int      `json:"importer_count"`
	IsHub          bool     `json:"is_hub"`
	CoverageStatus string   `json:"coverage_status"`
	CoverageNotes  []string `json:"coverage_notes"`
	Truncated      bool     `json:"truncated"`
	OmittedCount   int      `json:"omitted_count"`
}

type HandoffOutput struct {
	Kind         string                  `json:"kind"`
	Artifact     *handoff.Artifact       `json:"artifact,omitempty"`
	Prefix       *handoff.PrefixSnapshot `json:"prefix,omitempty"`
	Delta        *handoff.DeltaSnapshot  `json:"delta,omitempty"`
	FileDetail   *handoff.FileDetail     `json:"file_detail,omitempty"`
	Message      string                  `json:"message,omitempty"`
	Truncated    bool                    `json:"truncated"`
	OmittedCount int                     `json:"omitted_count"`
}

func boundedProject(project scanner.Project) (scanner.Project, int) {
	bounded := project
	var omittedCount int
	bounded.Files, omittedCount = boundedSlice(project.Files)
	bounded.Impact, omittedCount = addBounded(project.Impact, omittedCount)
	return bounded, omittedCount
}

func newStructureOutput(project scanner.Project, hubs []string) StructureOutput {
	bounded, omittedCount := boundedProject(project)
	boundedHubs, omittedCount := addBounded(hubs, omittedCount)
	return StructureOutput{Kind: "structure", Project: bounded, Hubs: boundedHubs, Truncated: omittedCount > 0, OmittedCount: omittedCount}
}

func newDiffOutput(kind, ref string, project scanner.Project) DiffOutput {
	bounded, omittedCount := boundedProject(project)
	return DiffOutput{Kind: kind, Ref: ref, Project: bounded, Truncated: omittedCount > 0, OmittedCount: omittedCount}
}

func newImportersOutput(root, file string, graph *scanner.FileGraph) ImportersOutput {
	importers := append([]string(nil), graph.Importers[file]...)
	imports := append([]string(nil), graph.Imports[file]...)
	hubImports := make([]string, 0, len(imports))
	for _, imported := range imports {
		if graph.IsHub(imported) {
			hubImports = append(hubImports, imported)
		}
	}
	boundedImporters, omittedCount := boundedSlice(importers)
	boundedImports, omittedCount := addBounded(imports, omittedCount)
	boundedHubs, omittedCount := addBounded(hubImports, omittedCount)
	kind := "importers"
	if len(importers) == 0 {
		kind = "empty"
	}
	return ImportersOutput{
		Kind: kind, Root: root, Mode: "importers", File: file,
		Importers: boundedImporters, Imports: boundedImports, HubImports: boundedHubs,
		ImporterCount: len(importers), IsHub: graph.IsHub(file),
		CoverageStatus: graph.Coverage.Status, CoverageNotes: nonNilCopy(graph.Coverage.Notes),
		Truncated: omittedCount > 0, OmittedCount: omittedCount,
	}
}

func newHandoffOutput(kind string, artifact *handoff.Artifact) HandoffOutput {
	output := HandoffOutput{Kind: kind}
	var omittedCount int
	switch kind {
	case "artifact":
		output.Artifact, omittedCount = boundedArtifact(artifact)
	case "prefix":
		bounded, omitted := boundedPrefix(artifact.Prefix)
		output.Prefix = &bounded
		omittedCount = omitted
	case "delta":
		bounded, omitted := addBoundedDelta(artifact.Delta, 0)
		output.Delta = &bounded
		omittedCount = omitted
	}
	output.Truncated = omittedCount > 0
	output.OmittedCount = omittedCount
	return output
}

func boundedFileDetail(detail *handoff.FileDetail) HandoffOutput {
	bounded := *detail
	var omittedCount int
	bounded.Importers, omittedCount = boundedSlice(detail.Importers)
	bounded.Imports, omittedCount = addBounded(detail.Imports, omittedCount)
	bounded.RecentEvents, omittedCount = addBounded(detail.RecentEvents, omittedCount)
	return HandoffOutput{Kind: "file_detail", FileDetail: &bounded, Truncated: omittedCount > 0, OmittedCount: omittedCount}
}

func boundedSlice[T any](values []T) ([]T, int) {
	if len(values) <= maxStructuredItems {
		return nonNilCopy(values), 0
	}
	return nonNilCopy(values[:maxStructuredItems]), len(values) - maxStructuredItems
}

func nonNilCopy[T any](values []T) []T {
	return append(make([]T, 0, len(values)), values...)
}

func boundedArtifact(artifact *handoff.Artifact) (*handoff.Artifact, int) {
	bounded := *artifact
	omittedCount := 0
	bounded.Prefix, omittedCount = boundedPrefix(artifact.Prefix)
	bounded.Delta, omittedCount = addBoundedDelta(artifact.Delta, omittedCount)
	bounded.AgentHistory, omittedCount = addBounded(artifact.AgentHistory, omittedCount)
	for index := range bounded.AgentHistory {
		bounded.AgentHistory[index].FilesEdited, omittedCount = addBounded(bounded.AgentHistory[index].FilesEdited, omittedCount)
	}
	bounded.ChangedFiles, omittedCount = addMirrorBounded(artifact.ChangedFiles, len(artifact.Delta.Changed), omittedCount)
	bounded.RiskFiles, omittedCount = addMirrorBounded(artifact.RiskFiles, len(artifact.Delta.RiskFiles), omittedCount)
	bounded.RecentEvents, omittedCount = addMirrorBounded(artifact.RecentEvents, len(artifact.Delta.RecentEvents), omittedCount)
	bounded.NextSteps, omittedCount = addMirrorBounded(artifact.NextSteps, len(artifact.Delta.NextSteps), omittedCount)
	bounded.OpenQuestions, omittedCount = addMirrorBounded(artifact.OpenQuestions, len(artifact.Delta.OpenQuestions), omittedCount)
	return &bounded, omittedCount
}

func boundedPrefix(prefix handoff.PrefixSnapshot) (handoff.PrefixSnapshot, int) {
	bounded := prefix
	var omittedCount int
	bounded.Hubs, omittedCount = boundedSlice(prefix.Hubs)
	return bounded, omittedCount
}

func addBoundedDelta(delta handoff.DeltaSnapshot, omittedCount int) (handoff.DeltaSnapshot, int) {
	bounded := delta
	bounded.Changed, omittedCount = addBounded(delta.Changed, omittedCount)
	bounded.RiskFiles, omittedCount = addBounded(delta.RiskFiles, omittedCount)
	bounded.RecentEvents, omittedCount = addBounded(delta.RecentEvents, omittedCount)
	bounded.NextSteps, omittedCount = addBounded(delta.NextSteps, omittedCount)
	bounded.OpenQuestions, omittedCount = addBounded(delta.OpenQuestions, omittedCount)
	return bounded, omittedCount
}

func addBounded[T any](values []T, omittedCount int) ([]T, int) {
	bounded, newlyOmitted := boundedSlice(values)
	return bounded, omittedCount + newlyOmitted
}

func addMirrorBounded[T any](values []T, mirroredLength, omittedCount int) ([]T, int) {
	bounded, newlyOmitted := boundedSlice(values)
	mirroredOmitted := max(0, mirroredLength-maxStructuredItems)
	return bounded, omittedCount + max(0, newlyOmitted-mirroredOmitted)
}
