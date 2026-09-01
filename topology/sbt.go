package topology

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type sbtBuild struct {
	manifest   string
	root       string
	rootName   string
	projects   map[string]*sbtProject
	references []sbtReference
	issues     []Issue
}

type sbtProject struct {
	name            string
	root            string
	sourceRoots     []string
	testSourceRoots []string
}

type sbtReference struct {
	source string
	target string
	scope  string
	kind   EdgeKind
	line   int
}

var (
	sbtProjectPattern        = regexp.MustCompile(`^\s*(?:lazy\s+)?val\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*\(?\s*(?:project|rootProject)\b(.*)$`)
	sbtProjectPathPattern    = regexp.MustCompile(`(?:project\.in\s*\(\s*file\s*\(\s*|project\s+in\s+file\s*\(\s*)["']([^"']+)["']`)
	sbtNamePattern           = regexp.MustCompile(`^\s*(?:ThisBuild\s*/\s*)?name\s*:=\s*["']([^"']+)["']`)
	sbtReferencePattern      = regexp.MustCompile(`\.(dependsOn|aggregate)\s*\(([^)]*)\)`)
	sbtSourcePattern         = regexp.MustCompile(`(?:unmanagedSourceDirectories|sourceDirectories|scalaSource|javaSource|kotlinSource)\b(.*)$`)
	sbtProjectNamePattern    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	sbtStringPattern         = regexp.MustCompile(`["']([^"']+)["']`)
	sbtDynamicProjectPattern = regexp.MustCompile(`\b(?:Seq|List|Vector|Set)\b.*\.map\s*\(.*\bProject\s*\(`)
)

func buildSBTFragment(ctx context.Context, inventory Inventory, manifests []string) (Fragment, error) {
	builds := make([]*sbtBuild, 0, len(manifests))
	for _, manifest := range manifests {
		if err := ctx.Err(); err != nil {
			return Fragment{}, err
		}
		build, err := parseSBTBuild(inventory.Root, manifest)
		if err != nil {
			return Fragment{}, err
		}
		builds = append(builds, build)
	}

	fragment := Fragment{
		Provider: "jvm",
		Members:  make(map[ID][]string),
		Coverage: Coverage{Status: CoverageComplete},
	}
	for _, build := range builds {
		for _, name := range sortedSBTProjectNames(build.projects) {
			project := build.projects[name]
			id := sbtID(build.manifest, name)
			fragment.Nodes = append(fragment.Nodes, Node{
				ID:              id,
				Kind:            NodeKind("sbt-project"),
				Name:            projectDisplayName(build.rootName, project),
				Manifest:        build.manifest,
				Root:            project.root,
				SourceRoots:     uniqueSortedStrings(project.sourceRoots),
				TestSourceRoots: uniqueSortedStrings(project.testSourceRoots),
				Provider:        "jvm",
			})
			fragment.Members[id] = membersForRoots(inventory.Files, append(
				append([]string(nil), project.sourceRoots...), project.testSourceRoots...,
			))
		}
		fragment.Coverage.Issues = append(fragment.Coverage.Issues, build.issues...)
		for _, reference := range build.references {
			target := build.projects[reference.target]
			if target == nil {
				fragment.Coverage.Issues = append(fragment.Coverage.Issues, Issue{
					Provider: "jvm",
					Code:     "unknown-sbt-project",
					Message:  fmt.Sprintf("%s:%d references unknown project %s", build.manifest, reference.line, reference.target),
				})
				continue
			}
			fragment.Edges = append(fragment.Edges, Edge{
				From:     sbtID(build.manifest, reference.source),
				To:       sbtID(build.manifest, reference.target),
				Kind:     reference.kind,
				Scope:    EdgeScope(reference.scope),
				Evidence: Evidence{Manifest: build.manifest, Line: reference.line},
			})
		}
	}
	if len(fragment.Coverage.Issues) > 0 {
		fragment.Coverage.Status = CoveragePartial
	}
	return fragment, nil
}

func parseSBTBuild(root, manifest string) (*sbtBuild, error) {
	data, err := os.ReadFile(filepath.Join(root, manifest))
	if err != nil {
		return nil, err
	}
	buildRoot := filepath.Dir(filepath.Clean(manifest))
	if buildRoot == "." {
		buildRoot = "."
	}
	build := &sbtBuild{
		manifest: filepath.Clean(manifest),
		root:     buildRoot,
		rootName: filepath.Base(filepath.Clean(buildRoot)),
		projects: make(map[string]*sbtProject),
	}
	if build.rootName == "." || build.rootName == string(filepath.Separator) {
		build.rootName = "root"
	}

	currentProject := ""
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	inBlockComment := false
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := stripSBTComments(scanner.Text(), &inBlockComment)
		if match := sbtNamePattern.FindStringSubmatch(line); match != nil {
			build.rootName = match[1]
		}
		if match := sbtProjectPattern.FindStringSubmatch(line); match != nil {
			name := match[1]
			projectRoot := name
			if pathMatch := sbtProjectPathPattern.FindStringSubmatch(match[2]); pathMatch != nil {
				projectRoot = pathMatch[1]
			}
			if name == "root" && projectRoot == name {
				projectRoot = "."
			}
			if projectRoot == "." {
				projectRoot = "."
			}
			project := &sbtProject{
				name: name,
				root: filepath.Clean(filepath.Join(build.root, filepath.FromSlash(projectRoot))),
			}
			project.sourceRoots, project.testSourceRoots = conventionalSBTRoots(project.root)
			if _, exists := build.projects[name]; exists {
				build.issues = append(build.issues, Issue{Provider: "jvm", Code: "duplicate-sbt-project", Message: fmt.Sprintf("%s:%d defines project %s more than once", build.manifest, lineNumber, name)})
			} else {
				build.projects[name] = project
			}
			currentProject = name
		}
		if sbtDynamicProjectPattern.MatchString(line) {
			build.issues = append(build.issues, Issue{Provider: "jvm", Code: "dynamic-sbt-project", Message: fmt.Sprintf("%s:%d project list is generated dynamically", build.manifest, lineNumber)})
		}
		if currentProject == "" {
			continue
		}
		for _, match := range sbtReferencePattern.FindAllStringSubmatch(line, -1) {
			kind := EdgeDependency
			if match[1] == "aggregate" {
				kind = EdgeBuildBoundary
			}
			arguments := splitSBTArguments(match[2])
			if len(arguments) == 0 {
				build.issues = append(build.issues, Issue{Provider: "jvm", Code: "dynamic-sbt-reference", Message: fmt.Sprintf("%s:%d %s has no literal project names", build.manifest, lineNumber, match[1])})
				continue
			}
			for _, argument := range arguments {
				name, scope := parseSBTReferenceArgument(argument)
				if name == "" {
					build.issues = append(build.issues, Issue{Provider: "jvm", Code: "dynamic-sbt-reference", Message: fmt.Sprintf("%s:%d %s contains a non-literal project reference", build.manifest, lineNumber, match[1])})
					continue
				}
				if kind == EdgeBuildBoundary && !strings.Contains(argument, "%") {
					scope = ""
				}
				build.references = append(build.references, sbtReference{source: currentProject, target: name, scope: scope, kind: kind, line: lineNumber})
			}
		}
		if match := sbtSourcePattern.FindStringSubmatch(line); match != nil {
			literals := sbtStringPattern.FindAllStringSubmatch(match[1], -1)
			if len(literals) == 0 {
				build.issues = append(build.issues, Issue{Provider: "jvm", Code: "dynamic-sbt-source-root", Message: fmt.Sprintf("%s:%d source root is not a literal path", build.manifest, lineNumber)})
				continue
			}
			for _, literal := range literals {
				path := filepath.Clean(filepath.Join(build.projects[currentProject].root, filepath.FromSlash(literal[1])))
				if strings.Contains(strings.ToLower(line), "test") {
					build.projects[currentProject].testSourceRoots = append(build.projects[currentProject].testSourceRoots, path)
				} else {
					build.projects[currentProject].sourceRoots = append(build.projects[currentProject].sourceRoots, path)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if _, ok := build.projects["root"]; !ok {
		rootProject := &sbtProject{name: "root", root: build.root}
		rootProject.sourceRoots, rootProject.testSourceRoots = conventionalSBTRoots(rootProject.root)
		build.projects["root"] = rootProject
	}
	for _, project := range build.projects {
		project.sourceRoots = uniqueSortedStrings(project.sourceRoots)
		project.testSourceRoots = uniqueSortedStrings(project.testSourceRoots)
	}
	return build, nil
}

func sbtID(manifest, project string) ID {
	return ID("sbt:" + filepath.ToSlash(filepath.Clean(manifest)) + ":" + project)
}

func projectDisplayName(rootName string, project *sbtProject) string {
	if project.name == "root" {
		return rootName
	}
	return project.name
}

func conventionalSBTRoots(root string) ([]string, []string) {
	var sourceRoots, testRoots []string
	for _, language := range []string{"java", "kotlin", "scala"} {
		sourceRoots = append(sourceRoots, filepath.Join(root, "src", "main", language))
		testRoots = append(testRoots, filepath.Join(root, "src", "test", language))
	}
	return sourceRoots, testRoots
}

func sortedSBTProjectNames(projects map[string]*sbtProject) []string {
	result := make([]string, 0, len(projects))
	for name := range projects {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func splitSBTArguments(text string) []string {
	var result []string
	start := 0
	depth := 0
	for index, char := range text {
		switch char {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				result = append(result, strings.TrimSpace(text[start:index]))
				start = index + 1
			}
		}
	}
	if tail := strings.TrimSpace(text[start:]); tail != "" {
		result = append(result, tail)
	}
	return result
}

func parseSBTReferenceArgument(argument string) (string, string) {
	argument = strings.TrimSpace(argument)
	parts := strings.SplitN(argument, "%", 2)
	name := strings.TrimSpace(parts[0])
	if !sbtProjectNamePattern.MatchString(name) {
		return "", ""
	}
	if len(parts) == 1 {
		return name, "compile"
	}
	scope := strings.TrimSpace(parts[1])
	if match := sbtStringPattern.FindStringSubmatch(scope); match != nil {
		scope = match[1]
	}
	if index := strings.IndexAny(scope, "->;"); index >= 0 {
		scope = scope[:index]
	}
	return name, strings.ToLower(strings.TrimSpace(scope))
}

func stripSBTComments(line string, inBlockComment *bool) string {
	inString, escaped := byte(0), false
	var result strings.Builder
	for index := 0; index < len(line); index++ {
		char := line[index]
		if *inBlockComment {
			if char == '*' && index+1 < len(line) && line[index+1] == '/' {
				*inBlockComment = false
				index++
			}
			continue
		}
		if inString != 0 {
			result.WriteByte(char)
			if escaped {
				escaped = false
			} else if char == '\\' {
				escaped = true
			} else if char == inString {
				inString = 0
			}
			continue
		}
		if char == '\'' || char == '"' {
			inString = char
			result.WriteByte(char)
			continue
		}
		if char == '/' && index+1 < len(line) && line[index+1] == '/' {
			return result.String()
		}
		if char == '/' && index+1 < len(line) && line[index+1] == '*' {
			*inBlockComment = true
			index++
			continue
		}
		result.WriteByte(char)
	}
	return result.String()
}
