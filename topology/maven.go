package topology

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type mavenPOM struct {
	manifest     string
	root         string
	raw          mavenProjectXML
	parent       *mavenPOM
	groupID      string
	artifactID   string
	version      string
	properties   map[string]string
	sourceRoots  []string
	testRoots    []string
	resolving    bool
	resolved     bool
	resolveIssue []Issue
}

type mavenProjectXML struct {
	ModelVersion string               `xml:"modelVersion"`
	GroupID      string               `xml:"groupId"`
	ArtifactID   string               `xml:"artifactId"`
	Version      string               `xml:"version"`
	Packaging    string               `xml:"packaging"`
	Parent       *mavenParentXML      `xml:"parent"`
	Modules      []string             `xml:"modules>module"`
	Subprojects  []string             `xml:"subprojects>subproject"`
	Dependencies []mavenDependencyXML `xml:"dependencies>dependency"`
	Build        mavenBuildXML        `xml:"build"`
	Profiles     []mavenProfileXML    `xml:"profiles>profile"`
	Properties   mavenProperties      `xml:"properties"`
}

type mavenParentXML struct {
	GroupID      string  `xml:"groupId"`
	ArtifactID   string  `xml:"artifactId"`
	Version      string  `xml:"version"`
	RelativePath *string `xml:"relativePath"`
}

type mavenDependencyXML struct {
	GroupID    string `xml:"groupId"`
	ArtifactID string `xml:"artifactId"`
	Version    string `xml:"version"`
	Scope      string `xml:"scope"`
	Optional   bool   `xml:"optional"`
}

type mavenBuildXML struct {
	SourceDirectory     string           `xml:"sourceDirectory"`
	TestSourceDirectory string           `xml:"testSourceDirectory"`
	Sources             []mavenSourceXML `xml:"sources>source"`
}

type mavenSourceXML struct {
	Scope     string `xml:"scope"`
	Directory string `xml:"directory"`
}

type mavenProfileXML struct {
	Modules      []string             `xml:"modules>module"`
	Subprojects  []string             `xml:"subprojects>subproject"`
	Dependencies []mavenDependencyXML `xml:"dependencies>dependency"`
}

type mavenProperties map[string]string

func (properties *mavenProperties) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	*properties = make(map[string]string)
	for {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch token := token.(type) {
		case xml.StartElement:
			var value string
			if err := decoder.DecodeElement(&value, &token); err != nil {
				return err
			}
			(*properties)[token.Name.Local] = strings.TrimSpace(value)
		case xml.EndElement:
			if token.Name == start.Name {
				return nil
			}
		}
	}
}

func buildMavenFragment(ctx context.Context, inventory Inventory, manifests []string) (Fragment, error) {
	poms := make(map[string]*mavenPOM, len(manifests))
	for _, manifest := range manifests {
		if err := ctx.Err(); err != nil {
			return Fragment{}, err
		}
		data, err := os.ReadFile(filepath.Join(inventory.Root, manifest))
		if err != nil {
			return Fragment{}, err
		}
		var project mavenProjectXML
		if err := xml.Unmarshal(data, &project); err != nil {
			return Fragment{}, fmt.Errorf("%s: %w", manifest, err)
		}
		clean := filepath.Clean(manifest)
		poms[clean] = &mavenPOM{
			manifest: clean,
			root:     filepath.Dir(clean),
			raw:      project,
		}
	}

	for _, pom := range sortedMavenPOMs(poms) {
		linkMavenParentByPath(pom, poms)
	}
	for _, pom := range sortedMavenPOMs(poms) {
		resolveMavenPOM(pom)
	}
	for _, pom := range sortedMavenPOMs(poms) {
		if len(mavenSubprojects(pom.raw)) == 0 && pom.raw.Packaging == "pom" && pom.raw.ModelVersion == "4.1.0" {
			for _, child := range sortedMavenPOMs(poms) {
				if child == pom || filepath.Dir(child.root) != pom.root {
					continue
				}
				pom.raw.Subprojects = append(pom.raw.Subprojects, filepath.Base(child.root))
			}
		}
	}

	fragment := Fragment{
		Provider: "jvm",
		Members:  make(map[ID][]string),
		Coverage: Coverage{Status: CoverageComplete},
	}
	coordinateIndex := make(map[string][]ID)
	for _, pom := range sortedMavenPOMs(poms) {
		id := mavenID(pom.manifest, pom.groupID, pom.artifactID)
		coordinateIndex[mavenCoordinate(pom.groupID, pom.artifactID)] = append(
			coordinateIndex[mavenCoordinate(pom.groupID, pom.artifactID)],
			id,
		)
		fragment.Nodes = append(fragment.Nodes, Node{
			ID:              id,
			Kind:            NodeKind("maven-project"),
			Name:            pom.artifactID,
			Manifest:        pom.manifest,
			Root:            pom.root,
			SourceRoots:     pom.sourceRoots,
			TestSourceRoots: pom.testRoots,
			Provider:        "jvm",
		})
		fragment.Members[id] = membersForRoots(
			inventory.Files,
			append(append([]string(nil), pom.sourceRoots...), pom.testRoots...),
		)
		fragment.Coverage.Issues = append(fragment.Coverage.Issues, pom.resolveIssue...)
		if mavenProfilesChangeTopology(pom.raw.Profiles) {
			fragment.Coverage.Issues = append(fragment.Coverage.Issues, Issue{
				Provider: "jvm",
				Code:     "maven-profile-topology",
				Message:  fmt.Sprintf("%s contains profile-controlled modules or dependencies", pom.manifest),
			})
		}
	}
	for coordinate := range coordinateIndex {
		coordinateIndex[coordinate] = uniqueSortedIDs(coordinateIndex[coordinate])
	}

	for _, pom := range sortedMavenPOMs(poms) {
		sourceID := mavenID(pom.manifest, pom.groupID, pom.artifactID)
		if parentID, ok := localMavenParentID(pom, coordinateIndex); ok {
			fragment.Edges = append(fragment.Edges, Edge{
				From: sourceID, To: parentID, Kind: EdgeInheritance,
				Evidence: Evidence{Manifest: pom.manifest},
			})
		}
		for _, module := range mavenSubprojects(pom.raw) {
			moduleManifest := filepath.Clean(filepath.Join(pom.root, filepath.FromSlash(strings.TrimSpace(module)), "pom.xml"))
			child := poms[moduleManifest]
			if child == nil {
				fragment.Coverage.Issues = append(fragment.Coverage.Issues, Issue{
					Provider: "jvm",
					Code:     "unresolved-maven-module",
					Message:  fmt.Sprintf("%s declares missing local module %q", pom.manifest, module),
				})
				continue
			}
			childID := mavenID(child.manifest, child.groupID, child.artifactID)
			fragment.Edges = append(fragment.Edges, Edge{
				From: sourceID, To: childID, Kind: EdgeBuildBoundary,
				Evidence: Evidence{Manifest: pom.manifest},
			})
		}
		for _, dependency := range pom.raw.Dependencies {
			groupID, groupOK := resolveMavenValue(dependency.GroupID, pom.properties)
			artifactID, artifactOK := resolveMavenValue(dependency.ArtifactID, pom.properties)
			if !groupOK || !artifactOK {
				fragment.Coverage.Issues = append(fragment.Coverage.Issues, Issue{
					Provider: "jvm",
					Code:     "unresolved-maven-property",
					Message:  fmt.Sprintf("%s contains an unresolved dependency coordinate", pom.manifest),
				})
				continue
			}
			targets := coordinateIndex[mavenCoordinate(groupID, artifactID)]
			if len(targets) == 0 {
				continue
			}
			scope := strings.TrimSpace(dependency.Scope)
			if scope == "" {
				scope = "compile"
			}
			if dependency.Optional {
				scope = "optional"
			}
			resolution := ReferenceResolution{Targets: targets}
			switch len(targets) {
			case 1:
				resolution.Status = ResolutionResolved
			default:
				resolution.Status = ResolutionAmbiguous
				resolution.Candidates = targets
				resolution.Note = fmt.Sprintf("%s dependency %s is ambiguous", pom.manifest, mavenCoordinate(groupID, artifactID))
			}
			edges, issue := ExpandReference(sourceID, Edge{
				Kind: EdgeDependency, Scope: EdgeScope(scope),
				Evidence: Evidence{Manifest: pom.manifest},
			}, resolution)
			fragment.Edges = append(fragment.Edges, edges...)
			if issue != nil {
				issue.Provider = "jvm"
				issue.Code = "ambiguous-maven-coordinate"
				fragment.Coverage.Issues = append(fragment.Coverage.Issues, *issue)
			}
		}
	}

	if len(fragment.Coverage.Issues) > 0 {
		fragment.Coverage.Status = CoveragePartial
	}
	return fragment, nil
}

func linkMavenParentByPath(pom *mavenPOM, poms map[string]*mavenPOM) {
	if pom.raw.Parent == nil {
		return
	}
	relative := "../pom.xml"
	if pom.raw.Parent.RelativePath != nil {
		relative = strings.TrimSpace(*pom.raw.Parent.RelativePath)
		if relative == "" {
			return
		}
	}
	pom.parent = poms[filepath.Clean(filepath.Join(pom.root, filepath.FromSlash(relative)))]
}

func resolveMavenPOM(pom *mavenPOM) {
	if pom.resolved {
		return
	}
	if pom.resolving {
		pom.resolveIssue = append(pom.resolveIssue, Issue{
			Provider: "jvm", Code: "maven-parent-cycle",
			Message: fmt.Sprintf("%s participates in a local parent cycle", pom.manifest),
		})
		return
	}
	pom.resolving = true
	if pom.parent != nil {
		resolveMavenPOM(pom.parent)
	}
	properties := make(map[string]string)
	if pom.parent != nil {
		for key, value := range pom.parent.properties {
			properties[key] = value
		}
	}
	for key, value := range pom.raw.Properties {
		properties[key] = value
	}

	groupID := pom.raw.GroupID
	version := pom.raw.Version
	if pom.parent != nil {
		if groupID == "" {
			groupID = pom.parent.groupID
		}
		if version == "" {
			version = pom.parent.version
		}
	} else if pom.raw.Parent != nil {
		if groupID == "" {
			groupID = pom.raw.Parent.GroupID
		}
		if version == "" {
			version = pom.raw.Parent.Version
		}
	}
	properties["project.groupId"] = groupID
	properties["pom.groupId"] = groupID
	properties["project.artifactId"] = pom.raw.ArtifactID
	properties["pom.artifactId"] = pom.raw.ArtifactID
	properties["project.version"] = version
	properties["pom.version"] = version
	properties["project.basedir"] = "."
	properties["basedir"] = "."

	pom.groupID, _ = resolveMavenValue(groupID, properties)
	pom.artifactID, _ = resolveMavenValue(pom.raw.ArtifactID, properties)
	pom.version, _ = resolveMavenValue(version, properties)
	for range len(properties) {
		changed := false
		for key, value := range properties {
			if resolved, ok := resolveMavenValue(value, properties); ok && resolved != value {
				properties[key] = resolved
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	pom.properties = properties
	pom.sourceRoots, pom.testRoots = conventionalMavenRoots(pom.root)
	if pom.raw.Build.SourceDirectory != "" {
		if source, ok := resolveMavenValue(pom.raw.Build.SourceDirectory, properties); ok {
			pom.sourceRoots = append(pom.sourceRoots, filepath.Clean(filepath.Join(pom.root, filepath.FromSlash(source))))
		} else {
			pom.resolveIssue = append(pom.resolveIssue, Issue{Provider: "jvm", Code: "unresolved-maven-property", Message: fmt.Sprintf("%s contains an unresolved source directory", pom.manifest)})
		}
	}
	if pom.raw.Build.TestSourceDirectory != "" {
		if source, ok := resolveMavenValue(pom.raw.Build.TestSourceDirectory, properties); ok {
			pom.testRoots = append(pom.testRoots, filepath.Clean(filepath.Join(pom.root, filepath.FromSlash(source))))
		} else {
			pom.resolveIssue = append(pom.resolveIssue, Issue{Provider: "jvm", Code: "unresolved-maven-property", Message: fmt.Sprintf("%s contains an unresolved test source directory", pom.manifest)})
		}
	}
	for _, source := range pom.raw.Build.Sources {
		if source.Directory == "" {
			continue
		}
		resolved, ok := resolveMavenValue(source.Directory, properties)
		if !ok {
			pom.resolveIssue = append(pom.resolveIssue, Issue{Provider: "jvm", Code: "unresolved-maven-property", Message: fmt.Sprintf("%s contains an unresolved source directory", pom.manifest)})
			continue
		}
		path := filepath.Clean(filepath.Join(pom.root, filepath.FromSlash(resolved)))
		if strings.EqualFold(strings.TrimSpace(source.Scope), "test") {
			pom.testRoots = append(pom.testRoots, path)
		} else {
			pom.sourceRoots = append(pom.sourceRoots, path)
		}
	}
	pom.sourceRoots = uniqueSortedStrings(pom.sourceRoots)
	pom.testRoots = uniqueSortedStrings(pom.testRoots)
	pom.resolving = false
	pom.resolved = true
}

func mavenSubprojects(project mavenProjectXML) []string {
	return uniqueSortedStrings(append(append([]string(nil), project.Modules...), project.Subprojects...))
}

func localMavenParentID(pom *mavenPOM, coordinateIndex map[string][]ID) (ID, bool) {
	if pom.raw.Parent == nil {
		return "", false
	}
	if pom.parent != nil {
		return mavenID(pom.parent.manifest, pom.parent.groupID, pom.parent.artifactID), true
	}
	groupID, groupOK := resolveMavenValue(pom.raw.Parent.GroupID, pom.properties)
	artifactID, artifactOK := resolveMavenValue(pom.raw.Parent.ArtifactID, pom.properties)
	if !groupOK || !artifactOK {
		return "", false
	}
	targets := coordinateIndex[mavenCoordinate(groupID, artifactID)]
	if len(targets) != 1 {
		return "", false
	}
	return targets[0], true
}

func mavenID(manifest, groupID, artifactID string) ID {
	return ID("maven:" + filepath.ToSlash(filepath.Clean(manifest)) + ":" + groupID + ":" + artifactID)
}

func mavenCoordinate(groupID, artifactID string) string {
	return strings.TrimSpace(groupID) + ":" + strings.TrimSpace(artifactID)
}

func resolveMavenValue(value string, properties map[string]string) (string, bool) {
	value = strings.TrimSpace(value)
	for range 8 {
		start := strings.Index(value, "${")
		if start < 0 {
			return value, value != ""
		}
		end := strings.Index(value[start+2:], "}")
		if end < 0 {
			return value, false
		}
		end += start + 2
		key := value[start+2 : end]
		replacement, ok := properties[key]
		if !ok || replacement == value[start:end+1] {
			return value, false
		}
		value = value[:start] + replacement + value[end+1:]
	}
	return value, !strings.Contains(value, "${")
}

func conventionalMavenRoots(root string) ([]string, []string) {
	var sourceRoots, testRoots []string
	for _, language := range []string{"java", "kotlin", "scala"} {
		sourceRoots = append(sourceRoots, filepath.Join(root, "src", "main", language))
		testRoots = append(testRoots, filepath.Join(root, "src", "test", language))
	}
	return sourceRoots, testRoots
}

func sortedMavenPOMs(poms map[string]*mavenPOM) []*mavenPOM {
	manifests := make([]string, 0, len(poms))
	for manifest := range poms {
		manifests = append(manifests, manifest)
	}
	sort.Strings(manifests)
	result := make([]*mavenPOM, 0, len(manifests))
	for _, manifest := range manifests {
		result = append(result, poms[manifest])
	}
	return result
}

func mavenProfilesChangeTopology(profiles []mavenProfileXML) bool {
	for _, profile := range profiles {
		if len(profile.Modules) > 0 || len(profile.Subprojects) > 0 || len(profile.Dependencies) > 0 {
			return true
		}
	}
	return false
}
