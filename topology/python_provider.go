package topology

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"codemap/scanner"
)

type pythonProvider struct{}

type pythonWorkspace struct {
	RootManifest string
	Root         string
	Members      map[string]bool
}

func init() {
	RegisterProvider(pythonProvider{})
}

func (pythonProvider) Name() string        { return "python" }
func (pythonProvider) Version() string     { return "1" }
func (pythonProvider) Languages() []string { return []string{"python", "py"} }
func (pythonProvider) Manifests() ManifestSelector {
	return ManifestSelector{Names: []string{"pyproject.toml"}}
}

func (pythonProvider) Build(ctx context.Context, inventory Inventory) (Fragment, error) {
	fragment := Fragment{
		Provider: "python",
		Members:  make(map[ID][]string),
		Coverage: Coverage{Status: CoverageComplete},
	}
	manifests := make([]pythonManifest, 0, len(inventory.Manifests))
	for _, manifestPath := range inventory.Manifests {
		if err := ctx.Err(); err != nil {
			return Fragment{}, err
		}
		if filepath.Base(manifestPath) != "pyproject.toml" {
			continue
		}
		parsed, issues, err := parsePythonManifest(inventory.Root, manifestPath)
		fragment.Coverage.Issues = append(fragment.Coverage.Issues, issues...)
		if err != nil {
			fragment.Coverage.Issues = append(fragment.Coverage.Issues, pythonIssue(
				"malformed-manifest",
				fmt.Sprintf("%s could not be parsed: %v", manifestPath, err),
			))
			continue
		}
		manifests = append(manifests, parsed)
	}

	nodeByManifest := make(map[string]Node)
	for _, manifest := range manifests {
		if manifest.NormalizedName == "" {
			continue
		}
		node := pythonNode(manifest, inventory.Files)
		nodeByManifest[manifest.Path] = node
		fragment.Nodes = append(fragment.Nodes, node)
	}
	workspaces, workspaceIssues := pythonWorkspaces(manifests, nodeByManifest)
	fragment.Coverage.Issues = append(fragment.Coverage.Issues, workspaceIssues...)

	for _, manifest := range manifests {
		if err := ctx.Err(); err != nil {
			return Fragment{}, err
		}
		from, ok := nodeByManifest[manifest.Path]
		if !ok {
			continue
		}
		for _, dependency := range manifest.Dependencies {
			for _, source := range manifest.UVSources[dependency.Name] {
				targets, issues := resolvePythonSource(inventory.Root, manifest, dependency.Name, source, workspaces, nodeByManifest)
				fragment.Coverage.Issues = append(fragment.Coverage.Issues, issues...)
				fragment.Edges = appendPythonEdges(
					fragment.Edges,
					from.ID,
					targets,
					manifest.Path,
					source.Conditional || dependency.Conditional,
				)
			}
		}
		names := make([]string, 0, len(manifest.PoetrySources))
		for name := range manifest.PoetrySources {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, dependency := range names {
			for _, source := range manifest.PoetrySources[dependency] {
				targets, issues := resolvePythonSource(inventory.Root, manifest, dependency, source, workspaces, nodeByManifest)
				fragment.Coverage.Issues = append(fragment.Coverage.Issues, issues...)
				fragment.Edges = appendPythonEdges(fragment.Edges, from.ID, targets, manifest.Path, source.Conditional)
			}
		}
	}

	fragment.Members = pythonMembers(inventory.Files, fragment.Nodes)
	if len(fragment.Coverage.Issues) > 0 {
		fragment.Coverage.Status = CoveragePartial
	}
	return fragment, nil
}

func pythonNode(manifest pythonManifest, files []scanner.FileInfo) Node {
	root := filepath.Clean(manifest.Root)
	node := Node{
		ID:       ID("python:" + filepath.ToSlash(manifest.Path) + ":" + manifest.NormalizedName),
		Kind:     NodeKind("python-project"),
		Name:     manifest.Name,
		Manifest: manifest.Path,
		Root:     root,
		Provider: "python",
	}
	var sourceCandidates []string
	if manifest.PackageLayout {
		for _, packageRoot := range manifest.PackageRoots {
			sourceCandidates = append(sourceCandidates, filepath.FromSlash(packageRoot))
		}
	} else {
		packageDir := strings.ReplaceAll(manifest.NormalizedName, "-", "_")
		sourceCandidates = []string{filepath.Join(root, "src"), filepath.Join(root, packageDir)}
	}
	for _, candidate := range sourceCandidates {
		if pythonFilesUnder(files, candidate) {
			node.SourceRoots = append(node.SourceRoots, candidate)
		}
	}
	for _, candidate := range []string{filepath.Join(root, "tests"), filepath.Join(root, "test")} {
		if pythonFilesUnder(files, candidate) {
			node.TestSourceRoots = append(node.TestSourceRoots, candidate)
		}
	}
	node.SourceRoots = uniqueSortedStrings(node.SourceRoots)
	node.TestSourceRoots = uniqueSortedStrings(node.TestSourceRoots)
	return node
}

func pythonFilesUnder(files []scanner.FileInfo, root string) bool {
	for _, file := range files {
		if repoPathContains(root, file.Path) {
			return true
		}
	}
	return false
}

func pythonMembers(files []scanner.FileInfo, nodes []Node) map[ID][]string {
	members := make(map[ID][]string)
	sortedNodes := append([]Node(nil), nodes...)
	sort.Slice(sortedNodes, func(i, j int) bool {
		leftDepth := repoPathDepth(sortedNodes[i].Root)
		rightDepth := repoPathDepth(sortedNodes[j].Root)
		if leftDepth != rightDepth {
			return leftDepth > rightDepth
		}
		return sortedNodes[i].ID < sortedNodes[j].ID
	})
	for _, file := range files {
		var owner *Node
		for i := range sortedNodes {
			if repoPathContains(sortedNodes[i].Root, file.Path) {
				owner = &sortedNodes[i]
				break
			}
		}
		if owner == nil {
			continue
		}
		roots := append(append([]string(nil), owner.SourceRoots...), owner.TestSourceRoots...)
		for _, sourceRoot := range roots {
			if repoPathContains(sourceRoot, file.Path) {
				members[owner.ID] = append(members[owner.ID], filepath.Clean(file.Path))
				break
			}
		}
	}
	return members
}

func pythonWorkspaces(manifests []pythonManifest, nodes map[string]Node) ([]pythonWorkspace, []Issue) {
	var workspaces []pythonWorkspace
	var issues []Issue
	for _, manifest := range manifests {
		if len(manifest.WorkspaceMembers) == 0 {
			continue
		}
		workspace := pythonWorkspace{
			RootManifest: manifest.Path,
			Root:         manifest.Root,
			Members:      make(map[string]bool),
		}
		if _, ok := nodes[manifest.Path]; ok {
			workspace.Members[manifest.Path] = true
		}
		for _, candidate := range manifests {
			relative, err := filepath.Rel(manifest.Root, candidate.Root)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				continue
			}
			relative = filepath.ToSlash(relative)
			included, valid := matchesAnyPythonPattern(manifest.WorkspaceMembers, relative)
			if !valid {
				issues = append(issues, pythonIssue("invalid-workspace-pattern",
					fmt.Sprintf("%s has an invalid workspace member pattern", manifest.Path)))
				break
			}
			excluded, valid := matchesAnyPythonPattern(manifest.WorkspaceExclude, relative)
			if !valid {
				issues = append(issues, pythonIssue("invalid-workspace-pattern",
					fmt.Sprintf("%s has an invalid workspace exclude pattern", manifest.Path)))
				break
			}
			if included && !excluded {
				workspace.Members[candidate.Path] = true
			}
		}
		workspaces = append(workspaces, workspace)
	}
	sort.Slice(workspaces, func(i, j int) bool {
		if repoPathDepth(workspaces[i].Root) != repoPathDepth(workspaces[j].Root) {
			return repoPathDepth(workspaces[i].Root) > repoPathDepth(workspaces[j].Root)
		}
		return workspaces[i].RootManifest < workspaces[j].RootManifest
	})
	return workspaces, issues
}

func matchesAnyPythonPattern(patterns []string, candidate string) (bool, bool) {
	for _, patternValue := range patterns {
		patternValue = strings.TrimPrefix(filepath.ToSlash(filepath.Clean(patternValue)), "./")
		matched, err := path.Match(patternValue, candidate)
		if err != nil {
			return false, false
		}
		if matched {
			return true, true
		}
	}
	return false, true
}

func resolvePythonSource(
	root string,
	manifest pythonManifest,
	dependency string,
	source pythonLocalSource,
	workspaces []pythonWorkspace,
	nodes map[string]Node,
) ([]ID, []Issue) {
	if source.Workspace {
		for _, workspace := range workspaces {
			if manifest.Path != workspace.RootManifest && !workspace.Members[manifest.Path] {
				continue
			}
			var candidates []ID
			for manifestPath := range workspace.Members {
				node, ok := nodes[manifestPath]
				if ok && normalizePythonProjectName(node.Name) == dependency {
					candidates = append(candidates, node.ID)
				}
			}
			candidates = uniqueSortedIDs(candidates)
			switch len(candidates) {
			case 1:
				return candidates, nil
			case 0:
				return nil, []Issue{pythonIssue("missing-local-source",
					fmt.Sprintf("%s cannot resolve workspace dependency %q", manifest.Path, dependency))}
			default:
				issue := pythonIssue("ambiguous-local-dependency",
					fmt.Sprintf("%s has multiple workspace projects named %q", manifest.Path, dependency))
				issue.Candidates = candidates
				return nil, []Issue{issue}
			}
		}
		return nil, []Issue{pythonIssue("missing-local-source",
			fmt.Sprintf("%s is not contained in a uv workspace for dependency %q", manifest.Path, dependency))}
	}
	if source.Path == "" {
		return nil, nil
	}
	targetManifest, issue := resolvePythonPath(root, manifest, source.Path)
	if issue != nil {
		return nil, []Issue{*issue}
	}
	target, ok := nodes[targetManifest]
	if !ok {
		return nil, []Issue{pythonIssue("missing-local-source",
			fmt.Sprintf("%s local source %q is not a configured Python project", manifest.Path, source.Path))}
	}
	if normalizePythonProjectName(target.Name) != dependency {
		return nil, []Issue{pythonIssue("local-source-name-mismatch",
			fmt.Sprintf("%s dependency %q points to project %q", manifest.Path, dependency, target.Name))}
	}
	return []ID{target.ID}, nil
}

func resolvePythonPath(root string, manifest pythonManifest, sourcePath string) (string, *Issue) {
	if filepath.IsAbs(sourcePath) {
		issue := pythonIssue("invalid-local-source",
			fmt.Sprintf("%s local source %q must be repository-relative", manifest.Path, sourcePath))
		return "", &issue
	}
	joined := filepath.Join(manifest.Root, filepath.FromSlash(sourcePath))
	relative, err := normalizeRepoPath(root, joined)
	if err != nil {
		issue := pythonIssue("invalid-local-source",
			fmt.Sprintf("%s local source %q: %v", manifest.Path, sourcePath, err))
		return "", &issue
	}
	absolute := filepath.Join(root, relative)
	info, err := os.Stat(absolute)
	if err != nil {
		issue := pythonIssue("missing-local-source",
			fmt.Sprintf("%s local source %q does not exist", manifest.Path, sourcePath))
		return "", &issue
	}
	realRoot, rootErr := filepath.EvalSymlinks(root)
	realTarget, targetErr := filepath.EvalSymlinks(absolute)
	if rootErr != nil || targetErr != nil {
		issue := pythonIssue("invalid-local-source",
			fmt.Sprintf("%s local source %q could not be resolved safely", manifest.Path, sourcePath))
		return "", &issue
	}
	realRelative, err := filepath.Rel(realRoot, realTarget)
	if err != nil || realRelative == ".." || strings.HasPrefix(realRelative, ".."+string(filepath.Separator)) {
		issue := pythonIssue("invalid-local-source",
			fmt.Sprintf("%s local source %q escapes the repository", manifest.Path, sourcePath))
		return "", &issue
	}
	if info.IsDir() {
		relative = filepath.Join(relative, "pyproject.toml")
	} else if filepath.Base(relative) != "pyproject.toml" {
		issue := pythonIssue("invalid-local-source",
			fmt.Sprintf("%s local source %q is not a Python project", manifest.Path, sourcePath))
		return "", &issue
	}
	return filepath.Clean(relative), nil
}

func appendPythonEdges(edges []Edge, from ID, targets []ID, manifest string, conditional bool) []Edge {
	for _, target := range targets {
		edges = append(edges, Edge{
			From:        from,
			To:          target,
			Kind:        EdgeDependency,
			Scope:       EdgeScope("runtime"),
			Evidence:    Evidence{Manifest: manifest},
			Conditional: conditional,
		})
	}
	return edges
}

func repoPathContains(root, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	if root == "." {
		return candidate != ".." && !strings.HasPrefix(candidate, ".."+string(filepath.Separator))
	}
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func repoPathDepth(root string) int {
	root = filepath.Clean(root)
	if root == "." {
		return 0
	}
	return len(strings.Split(filepath.ToSlash(root), "/"))
}
