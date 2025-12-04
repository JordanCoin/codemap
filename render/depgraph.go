package render

import (
	"fmt"
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
		if len(w) > 0 {
			r := []rune(w)
			r[0] = unicode.ToUpper(r[0])
			words[i] = string(r)
		}
	}
	return strings.Join(words, " ")
}

// Language compatibility groups
var langGroups = map[string]string{
	"python":     "python",
	"go":         "go",
	"javascript": "js",
	"typescript": "js",
	"rust":       "rust",
	"ruby":       "ruby",
	"c":          "c",
	"cpp":        "c",
	"java":       "java",
	"swift":      "swift",
	"bash":       "bash",
	"kotlin":     "kotlin",
	"c_sharp":    "c_sharp",
	"php":        "php",
	"dart":       "dart",
	"r":          "r",
}

// Standard library names to filter out
var stdlibNames = map[string]bool{
	// Go stdlib
	"errors": true, "fmt": true, "io": true, "os": true, "path": true, "sync": true, "time": true, "context": true, "http": true,
	"net": true, "bytes": true, "strings": true, "strconv": true, "sort": true, "flag": true, "log": true, "bufio": true,
	"encoding": true, "testing": true, "runtime": true, "unsafe": true, "reflect": true, "regexp": true,
	// Python stdlib
	"logging": true, "typing": true, "collections": true, "datetime": true, "json": true, "sys": true, "re": true,
	"pathlib": true, "hashlib": true, "base64": true, "asyncio": true, "enum": true, "functools": true, "random": true,
	"math": true, "copy": true, "itertools": true, "contextlib": true,
	// JS/TS common
	"fs": true, "util": true, "events": true, "stream": true, "crypto": true, "https": true,
	"react": true, "filepath": true, "embed": true,
}

// normalizeImport normalizes an import string
func normalizeImport(imp, lang string) string {
	imp = strings.Trim(imp, "\"'")
	if strings.Contains(imp, "/") {
		parts := strings.Split(imp, "/")
		imp = parts[len(parts)-1]
	}
	if strings.Contains(imp, ".") && !strings.HasPrefix(imp, ".") {
		parts := strings.Split(imp, ".")
		imp = parts[len(parts)-1]
	}
	// Remove file extensions
	extPattern := regexp.MustCompile(`\.(py|go|js|ts|jsx|tsx|rb|rs|c|h|cpp|hpp|java|swift)$`)
	imp = extPattern.ReplaceAllString(imp, "")
	return strings.ToLower(imp)
}

// findInternalDeps finds which files import which other files
func findInternalDeps(files []scanner.FileAnalysis) map[string][]string {
	// Build lookup: name -> list of (path, language_group)
	type fileInfo struct {
		path      string
		langGroup string
	}
	nameToInfos := make(map[string][]fileInfo)

	for _, f := range files {
		langGroup := langGroups[f.Language]
		if langGroup == "" {
			langGroup = f.Language
		}
		basename := filepath.Base(f.Path)
		extPattern := regexp.MustCompile(`\.[^.]+$`)
		name := strings.ToLower(extPattern.ReplaceAllString(basename, ""))
		nameToInfos[name] = append(nameToInfos[name], fileInfo{f.Path, langGroup})
	}

	deps := make(map[string][]string)

	for _, f := range files {
		srcLang := f.Language
		srcGroup := langGroups[srcLang]
		if srcGroup == "" {
			srcGroup = srcLang
		}

		for _, imp := range f.Imports {
			// Skip stdlib-looking imports
			if !strings.Contains(imp, "/") && !strings.Contains(imp, ".") {
				if stdlibNames[strings.ToLower(imp)] {
					continue
				}
			}

			norm := normalizeImport(imp, srcLang)
			if stdlibNames[norm] {
				continue
			}

			if infos, ok := nameToInfos[norm]; ok {
				srcBasename := filepath.Base(f.Path)
				for _, info := range infos {
					if info.path == f.Path {
						continue // Skip self
					}
					if srcGroup != info.langGroup {
						continue // Skip cross-language
					}
					targetName := filepath.Base(info.path)
					if targetName == srcBasename {
						continue // Skip same-basename
					}
					// Check if already added
					found := false
					for _, d := range deps[f.Path] {
						if d == targetName {
							found = true
							break
						}
					}
					if !found {
						deps[f.Path] = append(deps[f.Path], targetName)
					}
				}
			}
		}
	}

	return deps
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
func Depgraph(project scanner.DepsProject) {
	files := project.Files
	externalDeps := project.ExternalDeps
	projectName := filepath.Base(project.Root)

	if len(files) == 0 {
		fmt.Println("  No source files found.")
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

	internalDeps := findInternalDeps(files)

	// Count dependencies on each file
	depCounts := make(map[string]int)
	for _, targets := range internalDeps {
		for _, target := range targets {
			depCounts[target]++
		}
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

	fmt.Println()

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
	langOrder := []string{"go", "javascript", "python", "swift", "rust", "ruby", "bash", "kotlin", "c_sharp", "php", "dart", "r"}

	for _, lang := range langOrder {
		if names, ok := extByLang[lang]; ok {
			label := scanner.LangDisplay[lang].Short
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
	fmt.Printf("╭%s╮\n", strings.Repeat("─", innerWidth))
	titlePadded := CenterString(title, innerWidth)
	fmt.Printf("│%s│\n", titlePadded)

	if len(depLines) > 0 {
		fmt.Printf("├%s┤\n", strings.Repeat("─", innerWidth))
		contentWidth := innerWidth - 2

		for _, line := range depLines {
			for len(line) > contentWidth {
				breakAt := strings.LastIndex(line[:contentWidth], ", ")
				if breakAt == -1 {
					breakAt = contentWidth - 1
				} else {
					breakAt++
				}
				fmt.Printf("│ %-*s │\n", contentWidth, line[:breakAt])
				line = "    " + strings.TrimLeft(line[breakAt:], " ")
			}
			fmt.Printf("│ %-*s │\n", contentWidth, line)
		}
	}

	fmt.Printf("╰%s╯\n", strings.Repeat("─", innerWidth))
	fmt.Println()

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
		fmt.Printf("%s %s\n", systemName, strings.Repeat("═", headerLen))

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
					fmt.Printf("  %s\n", chain)
				} else {
					fmt.Printf("  %s ───▶ %s\n", nameNoExt, tName)
				}
			} else {
				var targetStrs []string
				for _, t := range targets {
					targetStrs = append(targetStrs, extPattern.ReplaceAllString(t, ""))
				}

				if len(targets) <= 4 {
					fmt.Printf("  %s ───▶ %s\n", nameNoExt, strings.Join(targetStrs, ", "))
				} else {
					fmt.Printf("  %s ──┬──▶ %s\n", nameNoExt, targetStrs[0])
					for _, t := range targetStrs[1 : len(targetStrs)-1] {
						fmt.Printf("  %s   ├──▶ %s\n", strings.Repeat(" ", len(nameNoExt)), t)
					}
					fmt.Printf("  %s   └──▶ %s\n", strings.Repeat(" ", len(nameNoExt)), targetStrs[len(targetStrs)-1])
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
			fmt.Printf("  +%d standalone files\n", standaloneCount)
		}

		fmt.Println()
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
			fmt.Println(strings.Repeat("─", 61))
			var hubStrs []string
			for _, h := range hubs {
				hubStrs = append(hubStrs, fmt.Sprintf("%s (%d←)", extPattern.ReplaceAllString(h.name, ""), h.count))
			}
			fmt.Printf("HUBS: %s\n", strings.Join(hubStrs, ", "))
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
	fmt.Printf("%d files · %d functions · %d deps\n", len(files), totalFuncs, internalCount)
	fmt.Println()
}
