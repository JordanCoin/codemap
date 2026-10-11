package render

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"

	"codemap/analysis"
	"codemap/scanner"
)

// titleCase capitalizes the first letter of each word
func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		words[i] = string(r)
	}
	return strings.Join(words, " ")
}

// getSystemName infers a system/component name from directory path
func getSystemName(dirPath string) string {
	parts := strings.Split(strings.ReplaceAll(dirPath, "\\", "/"), "/")
	skip := map[string]bool{"src": true, "lib": true, "app": true, "internal": true, "pkg": true, ".": true, "": true}

	var meaningful []string
	for _, p := range parts {
		if !skip[strings.ToLower(p)] {
			meaningful = append(meaningful, p)
		}
	}

	if len(meaningful) > 0 {
		name := meaningful[0]
		name = strings.ReplaceAll(name, "_", " ")
		name = strings.ReplaceAll(name, "-", " ")
		return titleCase(name)
	}

	if len(parts) > 0 {
		return titleCase(parts[len(parts)-1])
	}
	return "Root"
}

var depgraphExtPattern = regexp.MustCompile(`\.[^.]+$`)

// Depgraph renders the dependency flow visualization. The file graph is built
// from the caller's analyses (BuildFileGraphFromAnalyses) rather than by
// re-running the ast-grep scan, and ctx propagates cancellation through that
// build so MCP and CLI callers are not left with an unobservable second scan.
func Depgraph(ctx context.Context, w io.Writer, project scanner.DepsProject) {
	files := project.Files
	externalDeps := project.ExternalDeps
	projectName := filepath.Base(project.Root)

	if len(files) == 0 {
		fmt.Fprintln(w, "  No source files found.")
		renderCoverageLine(w, project.Coverage)
		return
	}

	// Build internal names lookup
	internalNames := make(map[string]bool)
	extPattern := depgraphExtPattern
	for _, f := range files {
		basename := filepath.Base(f.Path)
		name := strings.ToLower(extPattern.ReplaceAllString(basename, ""))
		internalNames[name] = true
	}

	// Build the graph from the analyses the caller already produced instead of
	// re-scanning with BuildFileGraph, which would double the ast-grep work.
	graphFilters := scanner.ConfiguredFilters(project.Root)
	if project.EffectiveFilters != nil {
		graphFilters = *project.EffectiveFilters
	}
	fg, err := scanner.BuildFileGraphFromAnalyses(ctx, project.Root, files, graphFilters)
	var internalDeps map[string][]string
	var depCounts map[string]int
	depImporters := make(map[string][]string)
	if err == nil && fg != nil {
		// Build set of files we're displaying (may be filtered by --diff)
		displayedFiles := make(map[string]bool)
		for _, f := range files {
			displayedFiles[f.Path] = true
		}

		// Filter imports to only include displayed files
		internalDeps = make(map[string][]string)
		for file, imports := range fg.Imports {
			if !displayedFiles[file] {
				continue
			}
			var filtered []string
			for _, imp := range imports {
				if displayedFiles[imp] {
					filtered = append(filtered, imp)
				}
			}
			if len(filtered) > 0 {
				internalDeps[file] = filtered
			}
		}

		// Count importers only among displayed files
		depCounts = make(map[string]int)
		for file, importers := range fg.Importers {
			if !displayedFiles[file] {
				continue
			}
			var shown []string
			for _, imp := range importers {
				if displayedFiles[imp] {
					shown = append(shown, imp)
				}
			}
			if len(shown) > 0 {
				depCounts[file] = len(shown)
				depImporters[file] = shown
			}
		}
	} else {
		internalDeps = make(map[string][]string)
		depCounts = make(map[string]int)
	}

	// Group by top-level system
	systems := make(map[string][]scanner.FileAnalysis)
	for _, f := range files {
		parts := strings.Split(strings.ReplaceAll(f.Path, "\\", "/"), "/")
		system := "."
		if len(parts) > 1 {
			system = parts[0]
		}
		systems[system] = append(systems[system], f)
	}

	fmt.Fprintln(w)

	// Build external deps by language
	extByLang := make(map[string][]string)
	versionPattern := regexp.MustCompile(`^v\d+$`)

	for lang, deps := range externalDeps {
		if len(deps) == 0 {
			continue
		}
		seen := make(map[string]bool)
		var names []string
		for _, d := range deps {
			parts := strings.Split(d, "/")
			name := parts[len(parts)-1]
			if versionPattern.MatchString(name) && len(parts) > 1 {
				name = parts[len(parts)-2]
			}
			if !versionPattern.MatchString(name) && len(name) > 1 && !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
		if len(names) > 0 {
			extByLang[lang] = names
		}
	}

	// Calculate box width
	title := fmt.Sprintf("%s - Dependency Flow", projectName)
	maxWidth := len(title) + 6

	// Format dep lines
	var depLines []string
	langOrder := []string{"go", "cue", "javascript", "python", "swift", "dart", "rust", "ruby", "bash", "kotlin", "csharp", "php", "lua", "scala", "elixir", "solidity"}

	for _, lang := range langOrder {
		if names, ok := extByLang[lang]; ok {
			label := scanner.LangDisplay[lang]
			if label == "" {
				label = titleCase(lang)
			}
			line := fmt.Sprintf("%s: %s", label, strings.Join(names, ", "))
			depLines = append(depLines, line)
			if len(line)+4 > maxWidth {
				maxWidth = len(line) + 4
			}
		}
	}

	// Cap at 80
	if maxWidth > 80 {
		maxWidth = 80
	}
	innerWidth := maxWidth - 2

	// Print header box
	fmt.Fprintf(w, "╭%s╮\n", strings.Repeat("─", innerWidth))
	titlePadded := CenterString(title, innerWidth)
	fmt.Fprintf(w, "│%s│\n", titlePadded)

	if len(depLines) > 0 {
		fmt.Fprintf(w, "├%s┤\n", strings.Repeat("─", innerWidth))
		contentWidth := innerWidth - 2

		for _, line := range depLines {
			for len(line) > contentWidth {
				breakAt := strings.LastIndex(line[:contentWidth], ", ")
				if breakAt == -1 {
					breakAt = contentWidth - 1
				} else {
					breakAt++
				}
				fmt.Fprintf(w, "│ %-*s │\n", contentWidth, line[:breakAt])
				line = "    " + strings.TrimLeft(line[breakAt:], " ")
			}
			fmt.Fprintf(w, "│ %-*s │\n", contentWidth, line)
		}
	}

	fmt.Fprintf(w, "╰%s╯\n", strings.Repeat("─", innerWidth))
	fmt.Fprintln(w)

	// Sort systems
	var systemNames []string
	for name := range systems {
		systemNames = append(systemNames, name)
	}
	sort.Strings(systemNames)

	// Render each system
	for _, system := range systemNames {
		sysFiles := systems[system]
		systemName := getSystemName(system)

		// Check if system has content
		hasContent := false
		for _, f := range sysFiles {
			if len(internalDeps[f.Path]) > 0 || len(f.Functions) > 0 {
				hasContent = true
				break
			}
		}
		if !hasContent {
			continue
		}

		// Section header
		headerLen := 60 - len(systemName) - 1
		if headerLen < 1 {
			headerLen = 1
		}
		fmt.Fprintf(w, "%s %s\n", systemName, strings.Repeat("═", headerLen))

		rendered := make(map[string]bool)

		for _, f := range sysFiles {
			basename := filepath.Base(f.Path)
			nameNoExt := extPattern.ReplaceAllString(basename, "")

			if rendered[basename] {
				continue
			}

			targets := internalDeps[f.Path]
			if len(targets) == 0 {
				continue
			}

			// Go imports name packages, so one import of a multi-file package
			// is one edge per file in the graph (and in --json). The text view
			// draws it once, as "dir/ (N files)", so the output grows with the
			// number of imports rather than with how packages are split up.
			targetStrs := depgraphTargetNames(targets)

			if len(targets) == 1 {
				t := targets[0]
				tName := extPattern.ReplaceAllString(t, "")

				// Check for sub-deps
				var tPath string
				for _, ff := range files {
					if filepath.Base(ff.Path) == t {
						tPath = ff.Path
						break
					}
				}

				subTargets := internalDeps[tPath]
				if len(subTargets) > 0 {
					var subNames []string
					for i, s := range subTargets {
						if i >= 3 {
							break
						}
						subNames = append(subNames, extPattern.ReplaceAllString(s, ""))
					}
					chain := fmt.Sprintf("%s ───▶ %s ───▶ %s", nameNoExt, tName, strings.Join(subNames, ", "))
					if len(subTargets) > 3 {
						chain += fmt.Sprintf(" +%d", len(subTargets)-3)
					}
					fmt.Fprintf(w, "  %s\n", chain)
				} else {
					fmt.Fprintf(w, "  %s ───▶ %s\n", nameNoExt, tName)
				}
			} else if len(targetStrs) == 1 {
				fmt.Fprintf(w, "  %s ───▶ %s\n", nameNoExt, targetStrs[0])
			} else {
				if len(targetStrs) <= 4 {
					fmt.Fprintf(w, "  %s ───▶ %s\n", nameNoExt, strings.Join(targetStrs, ", "))
				} else {
					fmt.Fprintf(w, "  %s ──┬──▶ %s\n", nameNoExt, targetStrs[0])
					for _, t := range targetStrs[1 : len(targetStrs)-1] {
						fmt.Fprintf(w, "  %s   ├──▶ %s\n", strings.Repeat(" ", len(nameNoExt)), t)
					}
					fmt.Fprintf(w, "  %s   └──▶ %s\n", strings.Repeat(" ", len(nameNoExt)), targetStrs[len(targetStrs)-1])
				}
			}

			rendered[basename] = true
		}

		// Count standalone files
		standaloneCount := 0
		for _, f := range sysFiles {
			basename := filepath.Base(f.Path)
			if !rendered[basename] && len(f.Functions) > 0 {
				standaloneCount++
			}
		}

		if standaloneCount > 0 {
			fmt.Fprintf(w, "  +%d standalone files\n", standaloneCount)
		}

		fmt.Fprintln(w)
	}

	// HUBS section
	if len(depCounts) > 0 {
		hubs := depgraphHubs(depCounts, depImporters)
		if len(hubs) > 6 {
			hubs = hubs[:6]
		}

		if len(hubs) > 0 {
			fmt.Fprintln(w, strings.Repeat("─", 61))
			var hubStrs []string
			for _, h := range hubs {
				hubStrs = append(hubStrs, h.String())
			}
			fmt.Fprintf(w, "HUBS: %s\n", strings.Join(hubStrs, ", "))
		}
	}

	// Summary
	totalFuncs := 0
	for _, f := range files {
		totalFuncs += len(f.Functions)
	}
	internalCount := 0
	for _, targets := range internalDeps {
		internalCount += len(depgraphTargetNames(targets))
	}
	fmt.Fprintf(w, "%d files · %d functions · %d deps\n", len(files), totalFuncs, internalCount)
	renderCoverageLine(w, project.Coverage)
	fmt.Fprintln(w)
}

// depgraphTargetNames is the text form of one file's resolved imports: the
// files of a multi-file Go package collapse to one "dir/ (N files)" entry and
// every other path loses its extension. The graph itself (and --json) keeps
// the per-file edges.
func depgraphTargetNames(targets []string) []string {
	names := make([]string, 0, len(targets))
	for _, t := range scanner.CollapseGoPackageFiles(targets) {
		if strings.HasSuffix(t, " files)") {
			names = append(names, t)
			continue
		}
		names = append(names, depgraphExtPattern.ReplaceAllString(t, ""))
	}
	return names
}

type depgraphHub struct {
	name  string
	count int
	files int // non-zero when name is a multi-file Go package
}

func (h depgraphHub) String() string {
	if h.files > 0 {
		return fmt.Sprintf("%s (%d←, %d files)", h.name, h.count, h.files)
	}
	return fmt.Sprintf("%s (%d←)", h.name, h.count)
}

// depgraphHubs ranks hubs by displayed importer count. The files of one
// multi-file Go package all carry the same importers (an import names the
// package), so they are reported once, as the package, with the number of
// distinct files importing it; a single-file package stays a file. Ties break
// on name so the head of the list is stable across runs.
func depgraphHubs(depCounts map[string]int, depImporters map[string][]string) []depgraphHub {
	goFilesPerDir := make(map[string]int)
	for name := range depCounts {
		if strings.EqualFold(filepath.Ext(name), ".go") {
			goFilesPerDir[pathDir(name)]++
		}
	}
	packageImporters := make(map[string]map[string]bool)
	var hubs []depgraphHub
	for name, count := range depCounts {
		if strings.EqualFold(filepath.Ext(name), ".go") && goFilesPerDir[pathDir(name)] >= 2 {
			dir := pathDir(name)
			if packageImporters[dir] == nil {
				packageImporters[dir] = make(map[string]bool)
			}
			for _, imp := range depImporters[name] {
				packageImporters[dir][imp] = true
			}
			continue
		}
		if count >= 2 {
			hubs = append(hubs, depgraphHub{name: depgraphExtPattern.ReplaceAllString(name, ""), count: count})
		}
	}
	for dir, importers := range packageImporters {
		if len(importers) >= 2 {
			label := dir
			if label == "." {
				label = ""
			}
			hubs = append(hubs, depgraphHub{name: label + "/", count: len(importers), files: goFilesPerDir[dir]})
		}
	}
	sort.Slice(hubs, func(i, j int) bool {
		if hubs[i].count != hubs[j].count {
			return hubs[i].count > hubs[j].count
		}
		return hubs[i].name < hubs[j].name
	})
	return hubs
}

func pathDir(path string) string {
	dir := filepath.ToSlash(filepath.Dir(filepath.FromSlash(path)))
	if dir == "" {
		return "."
	}
	return dir
}

// renderCoverageLine surfaces degraded scan coverage in text output so a
// fail-closed or partial scan never reads as a complete, empty graph.
func renderCoverageLine(w io.Writer, coverage analysis.Coverage) {
	if coverage.Status == "" || coverage.Status == analysis.CoverageComplete {
		return
	}
	var details []string
	for _, source := range coverage.Sources {
		// NormalizeCoverage sorts Sources by name/status/detail but does not
		// deduplicate them; two distinct sources can still carry the same
		// caveat, so render each detail once to keep the warning single.
		if source.Detail != "" && !slices.Contains(details, source.Detail) {
			details = append(details, source.Detail)
		}
	}
	if len(details) == 0 {
		fmt.Fprintf(w, "  Coverage: %s\n", coverage.Status)
		return
	}
	fmt.Fprintf(w, "  Coverage: %s — %s\n", coverage.Status, strings.Join(details, "; "))
}
