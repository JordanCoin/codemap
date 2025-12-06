package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"codemap/scanner"
)

// RunHook executes the named hook with the given project root
func RunHook(hookName, root string) error {
	switch hookName {
	case "session-start":
		return hookSessionStart(root)
	case "pre-edit":
		return hookPreEdit(root)
	case "post-edit":
		return hookPostEdit(root)
	case "prompt-submit":
		return hookPromptSubmit(root)
	case "pre-compact":
		return hookPreCompact(root)
	case "session-stop":
		return hookSessionStop(root)
	default:
		return fmt.Errorf("unknown hook: %s\nAvailable: session-start, pre-edit, post-edit, prompt-submit, pre-compact, session-stop", hookName)
	}
}

// hookSessionStart shows project structure and hub file warnings
func hookSessionStart(root string) error {
	fmt.Println("📍 Project Context:")
	fmt.Println()

	// Show project structure (limited to 40 lines)
	gitCache := scanner.NewGitIgnoreCache(root)
	files, err := scanner.ScanFiles(root, gitCache)
	if err != nil {
		return err
	}

	// Build and render a simple tree
	project := scanner.Project{
		Root:  root,
		Mode:  "tree",
		Files: files,
	}

	// Import render package would create circular dep, so just print summary
	fmt.Printf("Files: %d\n", len(files))

	// Count by extension
	extCounts := make(map[string]int)
	for _, f := range files {
		ext := f.Ext
		if ext == "" {
			ext = "(no ext)"
		}
		extCounts[ext]++
	}

	// Show top extensions
	fmt.Print("Top types: ")
	count := 0
	for ext, n := range extCounts {
		if count > 0 {
			fmt.Print(", ")
		}
		fmt.Printf("%s(%d)", ext, n)
		count++
		if count >= 5 {
			break
		}
	}
	fmt.Println()
	fmt.Println()

	// Show hub files
	fg, err := scanner.BuildFileGraph(root)
	if err == nil {
		hubs := fg.HubFiles()
		if len(hubs) > 0 {
			fmt.Println("⚠️  High-impact files (hubs):")
			for i, hub := range hubs {
				if i >= 10 {
					fmt.Printf("   ... and %d more\n", len(hubs)-10)
					break
				}
				importers := len(fg.Importers[hub])
				fmt.Printf("   ⚠️  HUB FILE: %s (imported by %d files)\n", hub, importers)
			}
		}
	}

	_ = project // silence unused warning
	return nil
}

// hookPreEdit warns before editing hub files (reads JSON from stdin)
func hookPreEdit(root string) error {
	filePath, err := extractFilePathFromStdin()
	if err != nil || filePath == "" {
		return nil // silently skip if no file path
	}

	return checkFileImporters(root, filePath)
}

// hookPostEdit shows impact after editing (reads JSON from stdin)
func hookPostEdit(root string) error {
	filePath, err := extractFilePathFromStdin()
	if err != nil || filePath == "" {
		return nil
	}

	return checkFileImporters(root, filePath)
}

// hookPromptSubmit detects file mentions in user prompt
func hookPromptSubmit(root string) error {
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil
	}

	// Extract prompt from JSON
	var data map[string]interface{}
	if err := json.Unmarshal(input, &data); err != nil {
		return nil
	}

	prompt, ok := data["prompt"].(string)
	if !ok || prompt == "" {
		return nil
	}

	// Look for file patterns in the prompt
	var filesMentioned []string

	// Check for common source file extensions
	extensions := []string{"go", "ts", "js", "py", "rs", "rb", "java", "swift", "kt", "c", "cpp", "h"}
	for _, ext := range extensions {
		pattern := regexp.MustCompile(`[a-zA-Z0-9_/-]+\.` + ext)
		matches := pattern.FindAllString(prompt, 3)
		filesMentioned = append(filesMentioned, matches...)
	}

	if len(filesMentioned) == 0 {
		return nil
	}

	fmt.Println()
	fmt.Println("📍 Context for mentioned files:")

	fg, err := scanner.BuildFileGraph(root)
	if err != nil {
		return nil
	}

	for _, file := range filesMentioned {
		// Try to find the file in the graph
		if importers := fg.Importers[file]; len(importers) > 0 {
			if len(importers) >= 3 {
				fmt.Printf("   ⚠️  %s is a HUB (imported by %d files)\n", file, len(importers))
			} else {
				fmt.Printf("   📍 %s (imported by %d files)\n", file, len(importers))
			}
		}
	}
	fmt.Println()

	return nil
}

// hookPreCompact saves hub state before context compaction
func hookPreCompact(root string) error {
	codemapDir := filepath.Join(root, ".codemap")
	if err := os.MkdirAll(codemapDir, 0755); err != nil {
		return err
	}

	fg, err := scanner.BuildFileGraph(root)
	if err != nil {
		return nil // silently skip if deps unavailable
	}

	hubs := fg.HubFiles()
	if len(hubs) == 0 {
		return nil
	}

	// Write hub state
	hubsFile := filepath.Join(codemapDir, "hubs.txt")
	f, err := os.Create(hubsFile)
	if err != nil {
		return err
	}
	defer f.Close()

	fmt.Fprintf(f, "# Hub files at %s\n", time.Now().Format(time.RFC3339))
	for _, hub := range hubs {
		fmt.Fprintln(f, hub)
	}

	fmt.Println()
	fmt.Printf("💾 Saved hub state to .codemap/hubs.txt before compact\n")
	fmt.Printf("   (%d hub files tracked)\n", len(hubs))
	fmt.Println()

	return nil
}

// hookSessionStop summarizes what changed in the session
func hookSessionStop(root string) error {
	fmt.Println()
	fmt.Println("📊 Session Summary")
	fmt.Println("==================")

	// Get modified files from git
	cmd := exec.Command("git", "diff", "--name-only")
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
		return nil // not a git repo or no changes
	}

	modified := strings.TrimSpace(string(output))
	if modified == "" {
		fmt.Println("No files modified.")
		return nil
	}

	fg, _ := scanner.BuildFileGraph(root) // best effort

	fmt.Println()
	fmt.Println("Files modified:")
	scanner := bufio.NewScanner(strings.NewReader(modified))
	count := 0
	for scanner.Scan() {
		file := scanner.Text()
		count++
		if count > 10 {
			fmt.Printf("  ... and more\n")
			break
		}

		if fg != nil && fg.IsHub(file) {
			importers := len(fg.Importers[file])
			fmt.Printf("  ⚠️  %s (HUB - imported by %d files)\n", file, importers)
		} else {
			fmt.Printf("  • %s\n", file)
		}
	}

	// Show new untracked files
	cmd = exec.Command("git", "ls-files", "--others", "--exclude-standard")
	cmd.Dir = root
	output, err = cmd.Output()
	if err == nil {
		untracked := strings.TrimSpace(string(output))
		if untracked != "" {
			fmt.Println()
			fmt.Println("New files created:")
			scanner := bufio.NewScanner(strings.NewReader(untracked))
			count := 0
			for scanner.Scan() {
				count++
				if count > 5 {
					fmt.Printf("  ... and more\n")
					break
				}
				fmt.Printf("  + %s\n", scanner.Text())
			}
		}
	}

	fmt.Println()
	return nil
}

// extractFilePathFromStdin reads JSON from stdin and extracts file_path
func extractFilePathFromStdin() (string, error) {
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", err
	}

	var data map[string]interface{}
	if err := json.Unmarshal(input, &data); err != nil {
		// Try regex fallback for non-JSON or partial JSON
		re := regexp.MustCompile(`"file_path"\s*:\s*"([^"]+)"`)
		matches := re.FindSubmatch(input)
		if len(matches) >= 2 {
			return string(matches[1]), nil
		}
		return "", err
	}

	filePath, ok := data["file_path"].(string)
	if !ok {
		return "", nil
	}

	return filePath, nil
}

// checkFileImporters checks if a file is a hub and shows its importers
func checkFileImporters(root, filePath string) error {
	fg, err := scanner.BuildFileGraph(root)
	if err != nil {
		return nil // silently skip if deps unavailable
	}

	// Handle absolute paths - convert to relative
	if filepath.IsAbs(filePath) {
		if rel, err := filepath.Rel(root, filePath); err == nil {
			filePath = rel
		}
	}

	importers := fg.Importers[filePath]
	if len(importers) >= 3 {
		fmt.Println()
		fmt.Printf("⚠️  HUB FILE: %s\n", filePath)
		fmt.Printf("   Imported by %d files - changes have wide impact!\n", len(importers))
		fmt.Println()
		fmt.Println("   Dependents:")
		for i, imp := range importers {
			if i >= 5 {
				fmt.Printf("   ... and %d more\n", len(importers)-5)
				break
			}
			fmt.Printf("   • %s\n", imp)
		}
		fmt.Println()
	} else if len(importers) > 0 {
		fmt.Println()
		fmt.Printf("📍 File: %s\n", filePath)
		fmt.Printf("   Imported by %d file(s): %s\n", len(importers), strings.Join(importers, ", "))
		fmt.Println()
	}

	// Also check if this file imports any hubs
	imports := fg.Imports[filePath]
	var hubImports []string
	for _, imp := range imports {
		if fg.IsHub(imp) {
			hubImports = append(hubImports, imp)
		}
	}
	if len(hubImports) > 0 {
		fmt.Printf("   Imports %d hub(s): %s\n", len(hubImports), strings.Join(hubImports, ", "))
		fmt.Println()
	}

	return nil
}
