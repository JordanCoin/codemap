package topology

import (
	"bufio"
	"codemap/scanner"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

type gradleBuild struct {
	settings      string
	root          string
	rootName      string
	projects      map[string]*gradleProject
	includeBuilds []gradleIncludeBuild
	issues        []Issue
}

type gradleProject struct {
	path            string
	root            string
	sourceRoots     []string
	testSourceRoots []string
}

type gradleIncludeBuild struct {
	path string
	line int
}

var (
	gradleRootNamePattern           = regexp.MustCompile(`^\s*rootProject\.name\s*=\s*["']([^"']+)["']`)
	gradleIncludeCallPattern        = regexp.MustCompile(`^\s*include\s*\((.*)\)\s*$`)
	gradleIncludeGroovyPattern      = regexp.MustCompile(`^\s*include\s+(.+)$`)
	gradleIncludeBuildPattern       = regexp.MustCompile(`^\s*includeBuild\s*\(\s*["']([^"']+)["']\s*\)`)
	gradleIncludeBuildGroovy        = regexp.MustCompile(`^\s*includeBuild\s+["']([^"']+)["']`)
	gradleProjectDirPattern         = regexp.MustCompile(`^\s*project\(\s*["'](:[^"']*)["']\s*\)\.projectDir\s*=\s*(?:file\(\s*["']([^"']+)["']\s*\)|new File\(\s*rootDir\s*,\s*["']([^"']+)["']\s*\))`)
	gradleProjectDependency         = regexp.MustCompile(`^\s*([A-Za-z][A-Za-z0-9_]*)\s*\(\s*project\(\s*["'](:[^"']+)["']\s*\)\s*\)`)
	gradleProjectDependencyGroovy   = regexp.MustCompile(`^\s*([A-Za-z][A-Za-z0-9_]*)\s+project\(\s*["'](:[^"']+)["']\s*\)`)
	gradleAccessorDependency        = regexp.MustCompile(`^\s*([A-Za-z][A-Za-z0-9_]*)\s*\(\s*(projects(?:\.[A-Za-z_][A-Za-z0-9_]*)+)\s*\)`)
	gradleAccessorDependencyGroovy  = regexp.MustCompile(`^\s*([A-Za-z][A-Za-z0-9_]*)\s+(projects(?:\.[A-Za-z_][A-Za-z0-9_]*)+)`)
	gradleSourceRootCall            = regexp.MustCompile(`(?:java|kotlin|scala)\.srcDirs?\s*\((.*)\)`)
	gradleSourceRootGroovy          = regexp.MustCompile(`(?:java|kotlin|scala)\.srcDirs?\s*(?:=)?\s*(.+)$`)
	gradleStringLiteralPattern      = regexp.MustCompile(`["']([^"']+)["']`)
	gradleIncludeStartPattern       = regexp.MustCompile(`^\s*include\s*\(`)
	gradleSharedProjectBlockPattern = regexp.MustCompile(`\b(?:subprojects|allprojects)\b[^\{]*\{`)
)

func buildGradleFragment(ctx context.Context, inventory Inventory, manifests []string) (Fragment, error) {
	settingsPaths := filterManifestBasenames(manifests, "settings.gradle", "settings.gradle.kts")
	if len(settingsPaths) == 0 {
		return Fragment{
			Provider: "jvm",
			Coverage: Coverage{
				Status: CoveragePartial,
				Issues: []Issue{{Provider: "jvm", Code: "gradle-settings-missing", Message: "Gradle build files were found without a settings manifest"}},
			},
		}, nil
	}

	builds := make([]*gradleBuild, 0, len(settingsPaths))
	for _, settings := range settingsPaths {
		if err := ctx.Err(); err != nil {
			return Fragment{}, err
		}
		build, err := parseGradleSettings(inventory.Root, settings)
		if err != nil {
			return Fragment{}, err
		}
		builds = append(builds, build)
	}
	sort.Slice(builds, func(i, j int) bool { return builds[i].settings < builds[j].settings })

	buildByRoot := make(map[string]*gradleBuild)
	for _, build := range builds {
		buildByRoot[filepath.Clean(build.root)] = build
	}

	fragment := Fragment{
		Provider: "jvm",
		Members:  make(map[ID][]string),
		Coverage: Coverage{Status: CoverageComplete},
	}
	for _, build := range builds {
		for _, projectPath := range sortedGradleProjectPaths(build.projects) {
			project := build.projects[projectPath]
			node := gradleNode(build, project)
			fragment.Nodes = append(fragment.Nodes, node)
		}
		fragment.Coverage.Issues = append(fragment.Coverage.Issues, build.issues...)
	}

	for _, build := range builds {
		accessors := make(map[string][]string)
		for projectPath := range build.projects {
			accessor := gradleAccessorForProject(projectPath)
			accessors[accessor] = append(accessors[accessor], projectPath)
		}
		for _, projectPath := range sortedGradleProjectPaths(build.projects) {
			project := build.projects[projectPath]
			buildFile := gradleBuildFileForProject(manifests, project.root)
			if buildFile == "" {
				continue
			}
			edges, sourceRoots, testRoots, issues, err := parseGradleBuildFile(inventory.Root, build, projectPath, buildFile, accessors)
			if err != nil {
				return Fragment{}, err
			}
			project.sourceRoots = uniqueSortedStrings(append(project.sourceRoots, sourceRoots...))
			project.testSourceRoots = uniqueSortedStrings(append(project.testSourceRoots, testRoots...))
			fragment.Edges = append(fragment.Edges, edges...)
			fragment.Coverage.Issues = append(fragment.Coverage.Issues, issues...)
		}
	}

	for i, node := range fragment.Nodes {
		build := buildForSettings(builds, node.Manifest)
		if build == nil {
			continue
		}
		project := build.projects[gradlePathFromID(node.ID)]
		if project == nil {
			continue
		}
		node.SourceRoots = uniqueSortedStrings(project.sourceRoots)
		node.TestSourceRoots = uniqueSortedStrings(project.testSourceRoots)
		fragment.Nodes[i] = node
		fragment.Members[node.ID] = membersForRoots(inventory.Files, append(
			append([]string(nil), node.SourceRoots...),
			node.TestSourceRoots...,
		))
	}

	for _, build := range builds {
		sourceID := gradleID(build.settings, ":")
		for _, include := range build.includeBuilds {
			targetRoot := filepath.Clean(filepath.Join(build.root, filepath.FromSlash(include.path)))
			target := buildByRoot[targetRoot]
			if target == nil {
				fragment.Coverage.Issues = append(fragment.Coverage.Issues, Issue{
					Provider: "jvm",
					Code:     "unresolved-gradle-include-build",
					Message:  fmt.Sprintf("%s:%d includeBuild %q has no local settings manifest", build.settings, include.line, include.path),
				})
				continue
			}
			fragment.Edges = append(fragment.Edges, Edge{
				From:     sourceID,
				To:       gradleID(target.settings, ":"),
				Kind:     EdgeBuildBoundary,
				Evidence: Evidence{Manifest: build.settings, Line: include.line},
			})
		}
	}

	if len(fragment.Coverage.Issues) > 0 {
		fragment.Coverage.Status = CoveragePartial
	}
	return fragment, nil
}

func parseGradleSettings(root, settings string) (*gradleBuild, error) {
	data, err := os.ReadFile(filepath.Join(root, settings))
	if err != nil {
		return nil, err
	}
	buildRoot := filepath.Dir(settings)
	if buildRoot == "." {
		buildRoot = "."
	}
	build := &gradleBuild{
		settings: settings,
		root:     buildRoot,
		rootName: filepath.Base(filepath.Clean(buildRoot)),
		projects: map[string]*gradleProject{
			":": newGradleProject(buildRoot),
		},
	}
	if build.rootName == "." || build.rootName == string(filepath.Separator) {
		build.rootName = "root"
	}

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	inBlockComment := false
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := stripSBTComments(scanner.Text(), &inBlockComment)
		if match := gradleRootNamePattern.FindStringSubmatch(line); match != nil {
			build.rootName = match[1]
			continue
		}
		if match := gradleIncludeBuildPattern.FindStringSubmatch(line); match != nil {
			build.includeBuilds = append(build.includeBuilds, gradleIncludeBuild{path: filepath.FromSlash(match[1]), line: lineNumber})
			continue
		}
		if match := gradleIncludeBuildGroovy.FindStringSubmatch(line); match != nil {
			build.includeBuilds = append(build.includeBuilds, gradleIncludeBuild{path: filepath.FromSlash(match[1]), line: lineNumber})
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "includeBuild") {
			build.issues = append(build.issues, Issue{
				Provider: "jvm",
				Code:     "dynamic-gradle-include-build",
				Message:  fmt.Sprintf("%s:%d includeBuild is not a literal path", settings, lineNumber),
			})
			continue
		}
		if match := gradleIncludeCallPattern.FindStringSubmatch(line); match != nil {
			includes := gradleStringLiterals(match[1])
			if len(includes) == 0 || gradleHasDynamicStringLiteral(match[1]) {
				build.issues = append(build.issues, Issue{Provider: "jvm", Code: "dynamic-gradle-include", Message: fmt.Sprintf("%s:%d include has no literal project paths", settings, lineNumber)})
			}
			if len(includes) > 0 {
				addGradleProjects(build, includes)
			}
			continue
		}
		if match := gradleIncludeGroovyPattern.FindStringSubmatch(line); match != nil {
			includes := gradleStringLiterals(match[1])
			if len(includes) == 0 || gradleHasDynamicStringLiteral(match[1]) {
				build.issues = append(build.issues, Issue{Provider: "jvm", Code: "dynamic-gradle-include", Message: fmt.Sprintf("%s:%d include has no literal project paths", settings, lineNumber)})
			}
			if len(includes) > 0 {
				addGradleProjects(build, includes)
			}
			continue
		}
		if gradleIncludeStartPattern.MatchString(line) {
			build.issues = append(build.issues, Issue{Provider: "jvm", Code: "dynamic-gradle-include", Message: fmt.Sprintf("%s:%d include spans multiple lines", settings, lineNumber)})
			continue
		}
		if match := gradleProjectDirPattern.FindStringSubmatch(line); match != nil {
			project := build.projects[canonicalGradlePath(match[1])]
			if project == nil {
				build.issues = append(build.issues, Issue{Provider: "jvm", Code: "unknown-gradle-project-dir", Message: fmt.Sprintf("%s:%d projectDir references unknown project %s", settings, lineNumber, match[1])})
				continue
			}
			dir := match[2]
			if dir == "" {
				dir = match[3]
			}
			project.root = filepath.Clean(filepath.Join(build.root, filepath.FromSlash(dir)))
			project.sourceRoots, project.testSourceRoots = conventionalGradleRoots(project.root)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return build, nil
}

func parseGradleBuildFile(root string, build *gradleBuild, projectPath, manifest string, accessors map[string][]string) ([]Edge, []string, []string, []Issue, error) {
	data, err := os.ReadFile(filepath.Join(root, manifest))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	project := build.projects[projectPath]
	sourceID := gradleID(build.settings, projectPath)
	var edges []Edge
	var sourceRoots, testRoots []string
	var issues []Issue
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	inBlockComment := false
	sharedProjectBlockDepth := 0
	sharedProjectIssue := false
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := stripSBTComments(scanner.Text(), &inBlockComment)
		if sharedProjectBlockDepth > 0 {
			sharedProjectBlockDepth += gradleBraceDelta(line)
			if sharedProjectBlockDepth < 0 {
				sharedProjectBlockDepth = 0
			}
			continue
		}
		if gradleSharedProjectBlockPattern.MatchString(line) {
			sharedProjectBlockDepth = gradleBraceDelta(line)
			if sharedProjectBlockDepth < 0 {
				sharedProjectBlockDepth = 0
			}
			if !sharedProjectIssue {
				issues = append(issues, Issue{Provider: "jvm", Code: "dynamic-gradle-scope", Message: fmt.Sprintf("%s:%d project-wide Gradle block is not attributed to individual projects", manifest, lineNumber)})
				sharedProjectIssue = true
			}
			continue
		}
		if match := gradleProjectDependency.FindStringSubmatch(line); match != nil {
			targetPath := canonicalGradlePath(match[2])
			target := build.projects[targetPath]
			if target == nil {
				issues = append(issues, Issue{Provider: "jvm", Code: "unknown-gradle-project", Message: fmt.Sprintf("%s:%d references unknown project %s", manifest, lineNumber, match[2])})
				continue
			}
			edges = append(edges, Edge{
				From:     sourceID,
				To:       gradleID(build.settings, targetPath),
				Kind:     EdgeDependency,
				Scope:    EdgeScope(match[1]),
				Evidence: Evidence{Manifest: manifest, Line: lineNumber},
			})
			continue
		}
		if match := gradleProjectDependencyGroovy.FindStringSubmatch(line); match != nil {
			targetPath := canonicalGradlePath(match[2])
			target := build.projects[targetPath]
			if target == nil {
				issues = append(issues, Issue{Provider: "jvm", Code: "unknown-gradle-project", Message: fmt.Sprintf("%s:%d references unknown project %s", manifest, lineNumber, match[2])})
				continue
			}
			edges = append(edges, Edge{
				From:     sourceID,
				To:       gradleID(build.settings, targetPath),
				Kind:     EdgeDependency,
				Scope:    EdgeScope(match[1]),
				Evidence: Evidence{Manifest: manifest, Line: lineNumber},
			})
			continue
		}
		if match := gradleAccessorDependency.FindStringSubmatch(line); match != nil {
			targetPaths := uniqueSortedStrings(accessors[match[2]])
			if len(targetPaths) == 0 {
				issues = append(issues, Issue{Provider: "jvm", Code: "unknown-gradle-accessor", Message: fmt.Sprintf("%s:%d references unknown accessor %s", manifest, lineNumber, match[2])})
				continue
			}
			if len(targetPaths) > 1 {
				candidates := make([]ID, 0, len(targetPaths))
				for _, targetPath := range targetPaths {
					candidates = append(candidates, gradleID(build.settings, targetPath))
				}
				issues = append(issues, Issue{
					Provider:   "jvm",
					Code:       "ambiguous-gradle-accessor",
					Message:    fmt.Sprintf("%s:%d accessor %s maps to multiple projects", manifest, lineNumber, match[2]),
					Candidates: candidates,
				})
				continue
			}
			targetPath := targetPaths[0]
			edges = append(edges, Edge{
				From:     sourceID,
				To:       gradleID(build.settings, targetPath),
				Kind:     EdgeDependency,
				Scope:    EdgeScope(match[1]),
				Evidence: Evidence{Manifest: manifest, Line: lineNumber},
			})
			continue
		}
		if match := gradleAccessorDependencyGroovy.FindStringSubmatch(line); match != nil {
			targetPaths := uniqueSortedStrings(accessors[match[2]])
			if len(targetPaths) == 0 {
				issues = append(issues, Issue{Provider: "jvm", Code: "unknown-gradle-accessor", Message: fmt.Sprintf("%s:%d references unknown accessor %s", manifest, lineNumber, match[2])})
				continue
			}
			if len(targetPaths) > 1 {
				candidates := make([]ID, 0, len(targetPaths))
				for _, targetPath := range targetPaths {
					candidates = append(candidates, gradleID(build.settings, targetPath))
				}
				issues = append(issues, Issue{
					Provider:   "jvm",
					Code:       "ambiguous-gradle-accessor",
					Message:    fmt.Sprintf("%s:%d accessor %s maps to multiple projects", manifest, lineNumber, match[2]),
					Candidates: candidates,
				})
				continue
			}
			targetPath := targetPaths[0]
			edges = append(edges, Edge{
				From:     sourceID,
				To:       gradleID(build.settings, targetPath),
				Kind:     EdgeDependency,
				Scope:    EdgeScope(match[1]),
				Evidence: Evidence{Manifest: manifest, Line: lineNumber},
			})
			continue
		}
		if strings.Contains(line, "project(") || strings.Contains(line, "projects.") {
			issues = append(issues, Issue{Provider: "jvm", Code: "dynamic-gradle-dependency", Message: fmt.Sprintf("%s:%d project dependency is not a supported literal form", manifest, lineNumber)})
		}
		for _, literal := range gradleSourceRootLiterals(line) {
			path := filepath.Clean(filepath.Join(project.root, filepath.FromSlash(literal)))
			if strings.Contains(strings.ToLower(line), "test") {
				testRoots = append(testRoots, path)
			} else {
				sourceRoots = append(sourceRoots, path)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, nil, nil, err
	}
	return edges, sourceRoots, testRoots, issues, nil
}

func gradleSourceRootLiterals(line string) []string {
	if match := gradleSourceRootCall.FindStringSubmatch(line); match != nil {
		return gradleStringLiterals(match[1])
	}
	if match := gradleSourceRootGroovy.FindStringSubmatch(line); match != nil {
		return gradleStringLiterals(match[1])
	}
	return nil
}

func newGradleProject(root string) *gradleProject {
	source, test := conventionalGradleRoots(root)
	return &gradleProject{root: root, sourceRoots: source, testSourceRoots: test}
}

func addGradleProjects(build *gradleBuild, includes []string) {
	for _, include := range includes {
		projectPath := canonicalGradlePath(include)
		if projectPath == ":" {
			continue
		}
		relative := strings.TrimPrefix(projectPath, ":")
		projectRoot := filepath.Join(build.root, filepath.FromSlash(strings.ReplaceAll(relative, ":", "/")))
		project := newGradleProject(filepath.Clean(projectRoot))
		project.path = projectPath
		build.projects[projectPath] = project
	}
}

func conventionalGradleRoots(root string) ([]string, []string) {
	var sourceRoots, testRoots []string
	for _, language := range []string{"java", "kotlin", "scala"} {
		sourceRoots = append(sourceRoots, filepath.Join(root, "src", "main", language))
		testRoots = append(testRoots, filepath.Join(root, "src", "test", language))
	}
	return sourceRoots, testRoots
}

func gradleNode(build *gradleBuild, project *gradleProject) Node {
	projectPath := project.path
	if projectPath == "" {
		projectPath = ":"
	}
	name := build.rootName
	if projectPath != ":" {
		parts := strings.Split(strings.TrimPrefix(projectPath, ":"), ":")
		name = parts[len(parts)-1]
	}
	return Node{
		ID:              gradleID(build.settings, projectPath),
		Kind:            NodeKind("gradle-project"),
		Name:            name,
		Manifest:        build.settings,
		Root:            filepath.Clean(project.root),
		SourceRoots:     uniqueSortedStrings(project.sourceRoots),
		TestSourceRoots: uniqueSortedStrings(project.testSourceRoots),
		Provider:        "jvm",
	}
}

func gradleID(settings, projectPath string) ID {
	return ID("gradle:" + filepath.ToSlash(filepath.Clean(settings)) + ":" + canonicalGradlePath(projectPath))
}

func gradlePathFromID(id ID) string {
	text := string(id)
	last := strings.LastIndex(text, "::")
	if last >= 0 {
		return text[last+1:]
	}
	parts := strings.Split(text, ":")
	if len(parts) < 3 {
		return ":"
	}
	return ":" + strings.Join(parts[3:], ":")
}

func gradleAccessorForProject(projectPath string) string {
	segments := strings.Split(strings.TrimPrefix(canonicalGradlePath(projectPath), ":"), ":")
	if len(segments) == 1 && segments[0] == "" {
		return "projects"
	}
	for i, segment := range segments {
		segments[i] = lowerCamelGradleSegment(segment)
	}
	return "projects." + strings.Join(segments, ".")
}

func lowerCamelGradleSegment(segment string) string {
	var builder strings.Builder
	upperNext := false
	for _, char := range segment {
		if char == '_' || char == '-' {
			upperNext = true
			continue
		}
		if upperNext {
			builder.WriteRune(unicode.ToUpper(char))
			upperNext = false
		} else {
			builder.WriteRune(char)
		}
	}
	return builder.String()
}

func canonicalGradlePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || path == ":" {
		return ":"
	}
	if !strings.HasPrefix(path, ":") {
		path = ":" + path
	}
	return strings.TrimRight(path, ":")
}

func gradleStringLiterals(text string) []string {
	matches := gradleStringLiteralPattern.FindAllStringSubmatch(text, -1)
	result := make([]string, 0, len(matches))
	for _, match := range matches {
		if strings.Contains(match[1], "$") {
			continue
		}
		result = append(result, match[1])
	}
	return result
}

func gradleHasDynamicStringLiteral(text string) bool {
	for _, match := range gradleStringLiteralPattern.FindAllStringSubmatch(text, -1) {
		if strings.Contains(match[1], "$") {
			return true
		}
	}
	return false
}

func stripGradleLineComment(line string) string {
	inBlockComment := false
	return stripSBTComments(line, &inBlockComment)
}

func gradleBraceDelta(line string) int {
	inString, escaped := byte(0), false
	delta := 0
	for index := 0; index < len(line); index++ {
		char := line[index]
		if inString != 0 {
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
			continue
		}
		switch char {
		case '{':
			delta++
		case '}':
			delta--
		}
	}
	return delta
}

func sortedGradleProjectPaths(projects map[string]*gradleProject) []string {
	paths := make([]string, 0, len(projects))
	for path := range projects {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func filterManifestBasenames(manifests []string, names ...string) []string {
	allowed := make(map[string]bool, len(names))
	for _, name := range names {
		allowed[name] = true
	}
	var result []string
	for _, manifest := range manifests {
		if allowed[filepath.Base(manifest)] {
			result = append(result, filepath.Clean(manifest))
		}
	}
	sort.Strings(result)
	return result
}

func gradleBuildFileForProject(manifests []string, projectRoot string) string {
	for _, name := range []string{"build.gradle.kts", "build.gradle"} {
		candidate := filepath.Clean(filepath.Join(projectRoot, name))
		for _, manifest := range manifests {
			if filepath.Clean(manifest) == candidate {
				return candidate
			}
		}
	}
	return ""
}

func buildForSettings(builds []*gradleBuild, settings string) *gradleBuild {
	for _, build := range builds {
		if build.settings == settings {
			return build
		}
	}
	return nil
}

func membersForRoots(files []scanner.FileInfo, roots []string) []string {
	var members []string
	for _, file := range files {
		for _, root := range roots {
			if pathWithin(file.Path, root) {
				members = append(members, filepath.Clean(file.Path))
				break
			}
		}
	}
	return uniqueSortedStrings(members)
}

func pathWithin(path, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}
