package render

import (
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

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

// Depgraph renders the dependency flow visualization
func Depgraph(w io.Writer, project scanner.DepsProject) {
	files := project.Files
	externalDeps := project.ExternalDeps
	projectName := filepath.Base(project.Root)

	if len(files) == 0 {
		fmt.Fprintln(w, "  No source files found.")
		return
	}

	// Build internal names lookup
	internalNames := make(map[string]bool)
	extPattern := regexp.MustCompile(`\.[^.]+$`)
	for _, f := range files {
		basename := filepath.Base(f.Path)
		name := strings.ToLower(extPattern.ReplaceAllString(basename, ""))
		internalNames[name] = true
	}

	// Use BuildFileGraph for accurate file-level dependency resolution
	fg, err := scanner.BuildFileGraph(project.Root)
	var internalDeps map[string][]string
	var depCounts map[string]int
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
			count := 0
			for _, imp := range importers {
				if displayedFiles[imp] {
					count++
				}
			}
			if count > 0 {
				depCounts[file] = count
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
	langOrder := []string{"go", "javascript", "python", "swift", "rust", "ruby", "bash", "kotlin", "csharp", "php", "lua", "scala", "elixir", "solidity"}

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
			} else {
				var targetStrs []string
				for _, t := range targets {
					targetStrs = append(targetStrs, extPattern.ReplaceAllString(t, ""))
				}

				if len(targets) <= 4 {
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
		type hub struct {
			name  string
			count int
		}
		var hubs []hub
		for name, count := range depCounts {
			if count >= 2 {
				hubs = append(hubs, hub{name, count})
			}
		}
		sort.Slice(hubs, func(i, j int) bool {
			return hubs[i].count > hubs[j].count
		})
		if len(hubs) > 6 {
			hubs = hubs[:6]
		}

		if len(hubs) > 0 {
			fmt.Fprintln(w, strings.Repeat("─", 61))
			var hubStrs []string
			for _, h := range hubs {
				hubStrs = append(hubStrs, fmt.Sprintf("%s (%d←)", extPattern.ReplaceAllString(h.name, ""), h.count))
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
		internalCount += len(targets)
	}
	fmt.Fprintf(w, "%d files · %d functions · %d deps\n", len(files), totalFuncs, internalCount)
	fmt.Fprintln(w)
}
