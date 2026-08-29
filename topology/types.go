package topology

import (
	"codemap/scanner"
)

type ID string
type NodeKind string
type EdgeKind string
type EdgeScope string
type CoverageStatus string
type ResolutionStatus string

const (
	EdgeDependency    EdgeKind = "dependency"
	EdgeInheritance   EdgeKind = "inheritance"
	EdgeBuildBoundary EdgeKind = "build-boundary"

	CoverageComplete    CoverageStatus = "complete"
	CoveragePartial     CoverageStatus = "partial"
	CoverageUnavailable CoverageStatus = "unavailable"

	ResolutionResolved   ResolutionStatus = "resolved"
	ResolutionUnresolved ResolutionStatus = "unresolved"
	ResolutionAmbiguous  ResolutionStatus = "ambiguous"
)

type Node struct {
	ID              ID       `json:"id"`
	Kind            NodeKind `json:"kind"`
	Name            string   `json:"name"`
	Manifest        string   `json:"manifest"`
	Root            string   `json:"root"`
	SourceRoots     []string `json:"source_roots,omitempty"`
	TestSourceRoots []string `json:"test_source_roots,omitempty"`
	Provider        string   `json:"provider"`
}

type Evidence struct {
	Manifest string `json:"manifest"`
	Line     int    `json:"line,omitempty"`
}

type Edge struct {
	From        ID        `json:"from"`
	To          ID        `json:"to"`
	Kind        EdgeKind  `json:"kind"`
	Scope       EdgeScope `json:"scope,omitempty"`
	Evidence    Evidence  `json:"evidence"`
	Conditional bool      `json:"conditional,omitempty"`
	Incomplete  bool      `json:"incomplete,omitempty"`
}

type Issue struct {
	Provider   string `json:"provider,omitempty"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	Candidates []ID   `json:"candidates,omitempty"`
}

type Coverage struct {
	Status CoverageStatus `json:"status"`
	Issues []Issue        `json:"issues,omitempty"`
}

type Graph struct {
	Nodes        map[ID]Node     `json:"nodes"`
	Dependencies map[ID][]Edge   `json:"dependencies"`
	Dependents   map[ID][]Edge   `json:"dependents"`
	Members      map[ID][]string `json:"members"`
	Owners       map[string][]ID `json:"owners"`
	Coverage     Coverage        `json:"coverage"`
}

type Fragment struct {
	Provider string
	Nodes    []Node
	Edges    []Edge
	Members  map[ID][]string
	Coverage Coverage
}

type ReferenceResolution struct {
	Status     ResolutionStatus
	Targets    []ID
	Candidates []ID
	Note       string
}

type ProjectGraph struct {
	Files            *scanner.FileGraph `json:"files"`
	Topology         *Graph             `json:"topology,omitempty"`
	TopologyIdentity CacheIdentity      `json:"-"`
}
